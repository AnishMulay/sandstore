package cluster

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	sandlib "github.com/AnishMulay/sandstore/clients/library"
	clienttopology "github.com/AnishMulay/sandstore/clients/library/topology"
	"github.com/AnishMulay/sandstore/internal/communication"
	grpccomm "github.com/AnishMulay/sandstore/internal/communication/grpc"
	logservice "github.com/AnishMulay/sandstore/internal/log_service"
	locallog "github.com/AnishMulay/sandstore/internal/log_service/localdisc"
	ps "github.com/AnishMulay/sandstore/internal/server"
)

const (
	defaultLeaderWaitTimeout = 2 * time.Minute
	defaultRolloutTimeout    = 3 * time.Minute
)

type suiteConfig struct {
	namespace         string
	seeds             []string
	nodeAddrs         []string
	statefulSet       string
	snapshotFileCount int
}

type kubectlClient struct {
	baseArgs []string
}

func loadConfig(t *testing.T) suiteConfig {
	t.Helper()

	namespace := strings.TrimSpace(os.Getenv("POD_NAMESPACE"))
	if namespace == "" {
		t.Fatal("POD_NAMESPACE is required")
	}

	seeds := splitNonEmpty(strings.TrimSpace(os.Getenv("SANDSTORE_SEEDS")))
	if len(seeds) == 0 {
		seeds = []string{
			"sandstore-0.sandstore-headless:8080",
			"sandstore-1.sandstore-headless:8080",
			"sandstore-2.sandstore-headless:8080",
		}
	}

	nodeAddrs := splitNonEmpty(strings.TrimSpace(os.Getenv("SANDSTORE_NODE_ADDRS")))
	if len(nodeAddrs) == 0 {
		nodeAddrs = append([]string(nil), seeds...)
	}

	statefulSet := strings.TrimSpace(os.Getenv("SANDSTORE_STATEFULSET"))
	if statefulSet == "" {
		statefulSet = "sandstore"
	}

	snapshotFileCount := 260
	if raw := strings.TrimSpace(os.Getenv("DURABILITY_SNAPSHOT_FILE_COUNT")); raw != "" {
		var parsed int
		if _, err := fmt.Sscanf(raw, "%d", &parsed); err == nil && parsed > 0 {
			snapshotFileCount = parsed
		}
	}

	return suiteConfig{
		namespace:         namespace,
		seeds:             seeds,
		nodeAddrs:         nodeAddrs,
		statefulSet:       statefulSet,
		snapshotFileCount: snapshotFileCount,
	}
}

func splitNonEmpty(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func newCommunicator(t *testing.T, label string) *grpccomm.GRPCCommunicator {
	t.Helper()

	logDir := filepath.Join(t.TempDir(), label)
	ls := locallog.NewLocalDiscLogService(logDir, label, logservice.InfoLevel)
	return grpccomm.NewGRPCCommunicator(":0", ls)
}

func newClient(t *testing.T, cfg suiteConfig) *sandlib.SandstoreClient {
	t.Helper()

	comm := newCommunicator(t, "cluster-client")
	client, err := sandlib.NewSandstoreClient(cfg.seeds, comm)
	if err != nil {
		t.Fatalf("bootstrap client: %v", err)
	}
	return client
}

func waitForLeader(t *testing.T, cfg suiteConfig, timeout time.Duration) string {
	t.Helper()

	comm := newCommunicator(t, "leader-wait")
	deadline := time.Now().Add(timeout)
	lastLeader := ""
	stableCount := 0

	for time.Now().Before(deadline) {
		counts := make(map[string]int)
		for _, seed := range cfg.seeds {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			leader, err := queryLeader(ctx, comm, seed)
			cancel()
			if err == nil && leader != "" {
				counts[leader]++
			}
		}

		bestLeader := ""
		bestCount := 0
		for leader, count := range counts {
			if count > bestCount {
				bestLeader = leader
				bestCount = count
			}
		}

		if bestLeader != "" && bestCount >= 2 {
			if bestLeader == lastLeader {
				stableCount++
			} else {
				lastLeader = bestLeader
				stableCount = 1
			}

			if stableCount >= 3 {
				return bestLeader
			}
		} else {
			lastLeader = ""
			stableCount = 0
		}

		time.Sleep(2 * time.Second)
	}

	t.Fatalf("leader was not elected within %s", timeout)
	return ""
}

func queryLeader(ctx context.Context, comm *grpccomm.GRPCCommunicator, seed string) (string, error) {
	resp, err := comm.Send(ctx, seed, communication.Message{
		From: "cluster-tests",
		Type: ps.MsgTopologyRequest,
		Payload: clienttopology.MsgTopologyRequest{
			TopologyType: "converged",
		},
	})
	if err != nil {
		return "", err
	}
	if resp == nil || resp.Code != communication.CodeOK {
		return "", fmt.Errorf("topology request to %s returned %v", seed, resp)
	}

	var topo clienttopology.MsgTopologyResponse
	if err := json.Unmarshal(resp.Body, &topo); err != nil {
		return "", err
	}

	leader := strings.TrimSpace(string(topo.TopologyData))
	if leader == "" {
		return "", fmt.Errorf("seed %s reported no leader", seed)
	}
	return leader, nil
}

func createAndWriteFile(t *testing.T, client *sandlib.SandstoreClient, path string, data []byte) {
	t.Helper()

	fd, err := client.Open(path, os.O_CREATE|os.O_RDWR)
	if err != nil {
		t.Fatalf("open %s for write: %v", path, err)
	}
	defer func() {
		if err := client.Close(fd); err != nil {
			t.Fatalf("close %s: %v", path, err)
		}
	}()

	written, err := client.Write(fd, data)
	if err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	if written != len(data) {
		t.Fatalf("write %s: expected %d bytes, got %d", path, len(data), written)
	}

	if err := client.Fsync(fd); err != nil {
		t.Fatalf("fsync %s: %v", path, err)
	}
}

func readPath(t *testing.T, client *sandlib.SandstoreClient, path string, length int) []byte {
	t.Helper()

	fd, err := client.Open(path, os.O_RDWR)
	if err != nil {
		t.Fatalf("open %s for read: %v", path, err)
	}
	defer func() {
		if err := client.Close(fd); err != nil {
			t.Fatalf("close %s after read: %v", path, err)
		}
	}()

	data, err := client.Read(fd, length)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return data
}

func mustPathVisible(t *testing.T, client *sandlib.SandstoreClient, path string) {
	t.Helper()

	fd, err := client.Open(path, os.O_RDWR)
	if err != nil {
		t.Fatalf("expected path %s to exist: %v", path, err)
	}
	if err := client.Close(fd); err != nil {
		t.Fatalf("close visible path %s: %v", path, err)
	}
}

func expectPathMissing(t *testing.T, client *sandlib.SandstoreClient, path string) {
	t.Helper()

	fd, err := client.Open(path, os.O_RDWR)
	if err == nil {
		_ = client.Close(fd)
		t.Fatalf("expected path %s to be missing", path)
	}
}

func createBurst(t *testing.T, client *sandlib.SandstoreClient, prefix string, count int) string {
	t.Helper()

	lastPath := ""
	for i := 0; i < count; i++ {
		path := fmt.Sprintf("/%s-%04d.txt", prefix, i)
		createAndWriteFile(t, client, path, []byte(fmt.Sprintf("payload-%04d", i)))
		lastPath = path
	}
	return lastPath
}

func openWithTimeout(client *sandlib.SandstoreClient, path string, flags int, timeout time.Duration) (int, error) {
	type result struct {
		fd  int
		err error
	}

	ch := make(chan result, 1)
	go func() {
		fd, err := client.Open(path, flags)
		ch <- result{fd: fd, err: err}
	}()

	select {
	case res := <-ch:
		return res.fd, res.err
	case <-time.After(timeout):
		return -1, fmt.Errorf("open timed out after %s", timeout)
	}
}

func uniquePath(prefix string) string {
	return fmt.Sprintf("/%s-%d.txt", prefix, time.Now().UnixNano())
}

func serviceFromLeaderAddr(addr string) string {
	host := strings.TrimSpace(addr)
	if idx := strings.Index(host, ":"); idx >= 0 {
		host = host[:idx]
	}
	if idx := strings.Index(host, "."); idx >= 0 {
		host = host[:idx]
	}
	return host
}

func newKubectlClient(t *testing.T, cfg suiteConfig) *kubectlClient {
	t.Helper()

	host := strings.TrimSpace(os.Getenv("KUBERNETES_SERVICE_HOST"))
	port := strings.TrimSpace(os.Getenv("KUBERNETES_SERVICE_PORT_HTTPS"))
	if port == "" {
		port = strings.TrimSpace(os.Getenv("KUBERNETES_SERVICE_PORT"))
	}
	if host == "" || port == "" {
		t.Fatal("in-cluster Kubernetes service env vars are required")
	}

	token, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/token")
	if err != nil {
		t.Fatalf("read service account token: %v", err)
	}

	baseArgs := []string{
		fmt.Sprintf("--server=https://%s:%s", host, port),
		fmt.Sprintf("--token=%s", strings.TrimSpace(string(token))),
		"--certificate-authority=/var/run/secrets/kubernetes.io/serviceaccount/ca.crt",
		"-n", cfg.namespace,
	}

	return &kubectlClient{baseArgs: baseArgs}
}

func (k *kubectlClient) run(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "kubectl", append(append([]string(nil), k.baseArgs...), args...)...)
	return cmd.CombinedOutput()
}

func (k *kubectlClient) mustRun(t *testing.T, ctx context.Context, args ...string) []byte {
	t.Helper()

	out, err := k.run(ctx, args...)
	if err != nil {
		t.Fatalf("kubectl %s failed: %v\n%s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return out
}

func (k *kubectlClient) scaleStatefulSet(t *testing.T, cfg suiteConfig, replicas int) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), defaultRolloutTimeout)
	defer cancel()

	k.mustRun(t, ctx, "scale", fmt.Sprintf("statefulset/%s", cfg.statefulSet), fmt.Sprintf("--replicas=%d", replicas))

	if replicas == 0 {
		deadline := time.Now().Add(defaultRolloutTimeout)
		for time.Now().Before(deadline) {
			out := k.mustRun(t, context.Background(), "get", "pods", "-l", "app=sandstore", "--no-headers")
			if bytes.TrimSpace(out) == nil || len(bytes.TrimSpace(out)) == 0 {
				return
			}
			time.Sleep(2 * time.Second)
		}
		t.Fatalf("pods for %s did not scale down to zero in time", cfg.statefulSet)
	}

	k.mustRun(t, ctx, "rollout", "status", fmt.Sprintf("statefulset/%s", cfg.statefulSet), fmt.Sprintf("--timeout=%ds", int(defaultRolloutTimeout.Seconds())))
}

func (k *kubectlClient) deletePod(t *testing.T, podName string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), defaultRolloutTimeout)
	defer cancel()
	k.mustRun(t, ctx, "delete", "pod", podName, "--wait=true")
}
