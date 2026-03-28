package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	sandlib "github.com/AnishMulay/sandstore/clients/library"
	grpccomm "github.com/AnishMulay/sandstore/internal/communication/grpc"
	logservice "github.com/AnishMulay/sandstore/internal/log_service"
	locallog "github.com/AnishMulay/sandstore/internal/log_service/localdisc"
)

var defaultNodeServices = []string{"sandstore-hyperconverged-1", "sandstore-hyperconverged-2", "sandstore-hyperconverged-3"}

func envInt(name string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v <= 0 {
		return fallback
	}
	return v
}

func nodeAddresses() []string {
	raw := strings.TrimSpace(os.Getenv("SANDSTORE_NODE_ADDRS"))
	if raw == "" {
		return []string{"127.0.0.1:8080", "127.0.0.1:8081", "127.0.0.1:8082"}
	}
	parts := strings.Split(raw, ",")
	addrs := make([]string, 0, len(parts))
	for _, p := range parts {
		trimmed := strings.TrimSpace(p)
		if trimmed != "" {
			addrs = append(addrs, trimmed)
		}
	}
	if len(addrs) == 0 {
		return []string{"127.0.0.1:8080", "127.0.0.1:8081", "127.0.0.1:8082"}
	}
	return addrs
}

func runDockerCmd(args ...string) error {
	var lastErr error
	for attempt := 0; attempt < 5; attempt++ {
		cmd := exec.Command("docker", args...)
		out, err := cmd.CombinedOutput()
		if err == nil {
			return nil
		}

		msg := strings.TrimSpace(string(out))
		lastErr = fmt.Errorf("docker %s failed: %w (%s)", strings.Join(args, " "), err, msg)
		if !isRetriableDockerErr(msg) {
			return lastErr
		}

		time.Sleep(time.Duration(attempt+1) * 300 * time.Millisecond)
	}
	return lastErr
}

func isRetriableDockerErr(msg string) bool {
	msg = strings.ToLower(msg)
	return strings.Contains(msg, "unexpected eof") ||
		strings.Contains(msg, "error response from daemon: handle request") ||
		strings.Contains(msg, "cannot connect to the docker daemon") ||
		strings.Contains(msg, "connection reset by peer") ||
		strings.Contains(msg, "broken pipe")
}

func listContainerNames() ([]string, error) {
	cmd := exec.Command("docker", "ps", "-a", "--format", "{{.Names}}")
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	names := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			names = append(names, line)
		}
	}
	return names, nil
}

func resolveContainerName(service string) (string, error) {
	names, err := listContainerNames()
	if err != nil {
		return "", err
	}

	suffix := "-" + service + "-1"
	for _, name := range names {
		if strings.HasSuffix(name, suffix) {
			return name, nil
		}
	}
	for _, name := range names {
		if strings.Contains(name, service) {
			return name, nil
		}
	}
	return "", fmt.Errorf("container for service %q not found", service)
}

func serviceFromAddr(addr string) string {
	host := strings.TrimSpace(addr)
	if idx := strings.Index(host, ":"); idx > 0 {
		host = host[:idx]
	}
	return host
}

func newClientForAddr(addr string, comm *grpccomm.GRPCCommunicator) (*sandlib.SandstoreClient, error) {
	return sandlib.NewSandstoreClient([]string{addr}, comm)
}

func newClient(comm *grpccomm.GRPCCommunicator) (*sandlib.SandstoreClient, error) {
	return sandlib.NewSandstoreClient(nodeAddresses(), comm)
}

func bootstrapClient(comm *grpccomm.GRPCCommunicator, timeout time.Duration) (*sandlib.SandstoreClient, error) {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		client, err := newClient(comm)
		if err == nil {
			return client, nil
		}
		lastErr = err
		time.Sleep(500 * time.Millisecond)
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("client bootstrap timed out after %s", timeout)
	}
	return nil, lastErr
}

func currentLeaderService(comm *grpccomm.GRPCCommunicator, fallback string) string {
	counts := make(map[string]int)
	bestAddr := fallback
	bestCount := 0

	for _, addr := range nodeAddresses() {
		client, err := newClientForAddr(addr, comm)
		if err != nil || client.ServerAddr == "" {
			continue
		}
		counts[client.ServerAddr]++
		if counts[client.ServerAddr] > bestCount {
			bestCount = counts[client.ServerAddr]
			bestAddr = client.ServerAddr
		}
	}

	return serviceFromAddr(bestAddr)
}

func dockerAction(action string, services ...string) error {
	args := []string{action}
	for _, svc := range services {
		name, err := resolveContainerName(svc)
		if err != nil {
			return err
		}
		args = append(args, name)
	}
	return runDockerCmd(args...)
}

func createFiles(comm *grpccomm.GRPCCommunicator, client *sandlib.SandstoreClient, prefix string, count int) (*sandlib.SandstoreClient, string, error) {
	lastPath := ""
	for i := 0; i < count; i++ {
		path := fmt.Sprintf("/%s_%06d", prefix, i)
		var lastErr error
		for attempt := 0; attempt < 20; attempt++ {
			fd, err := client.Open(path, os.O_CREATE|os.O_RDWR)
			if err == nil {
				if err := client.Close(fd); err != nil {
					lastErr = fmt.Errorf("close %s: %w", path, err)
					time.Sleep(500 * time.Millisecond)
					continue
				}
				lastErr = nil
				break
			}
			lastErr = err

			nextClient, initErr := bootstrapClient(comm, 20*time.Second)
			if initErr == nil {
				client = nextClient
			}
			time.Sleep(500 * time.Millisecond)
		}
		if lastErr != nil {
			return client, "", fmt.Errorf("create %s: %w", path, lastErr)
		}

		lastPath = path
	}
	return client, lastPath, nil
}

func waitForPathOnNode(comm *grpccomm.GRPCCommunicator, addr string, path string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		visible, err := pathVisibleOnNode(comm, addr, path)
		if err == nil && visible {
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("path %s not visible on %s within %s", path, addr, timeout)
}

func pathVisibleOnNode(comm *grpccomm.GRPCCommunicator, addr string, path string) (bool, error) {
	c, err := newClientForAddr(addr, comm)
	if err != nil {
		return false, nil
	}
	fd, err := c.Open(path, os.O_RDWR)
	if err != nil {
		return false, nil
	}
	if err := c.Close(fd); err != nil {
		return false, fmt.Errorf("close %s on %s: %w", path, addr, err)
	}
	return true, nil
}

func waitForConvergedPathState(comm *grpccomm.GRPCCommunicator, path string, timeout time.Duration) (bool, error) {
	deadline := time.Now().Add(timeout)
	addrs := nodeAddresses()
	for time.Now().Before(deadline) {
		var converged bool
		var expectedVisible bool
		for i, addr := range addrs {
			visible, err := pathVisibleOnNode(comm, addr, path)
			if err != nil {
				return false, err
			}
			if i == 0 {
				expectedVisible = visible
				converged = true
				continue
			}
			if visible != expectedVisible {
				converged = false
				break
			}
		}
		if converged {
			return expectedVisible, nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return false, fmt.Errorf("path %s did not converge across nodes within %s", path, timeout)
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

func must(err error) {
	if err != nil {
		log.Fatal(err)
	}
}

func main() {
	ls := locallog.NewLocalDiscLogService("run/durability/logs", "durability", logservice.InfoLevel)
	comm := grpccomm.NewGRPCCommunicator(":0", ls)

	snapshotFileCount := envInt("DURABILITY_SNAPSHOT_FILE_COUNT", 600)
	log.Printf("Starting durability smoke tests (snapshot file count=%d)", snapshotFileCount)

	must(runDockerCmd("version"))

	client, err := bootstrapClient(comm, 60*time.Second)
	must(err)
	leaderService := currentLeaderService(comm, client.ServerAddr)
	log.Printf("Client bootstrapped with leader route %s", client.ServerAddr)

	// Test 1: Election Amnesia
	log.Println("--- Test Case 1: Election Amnesia ---")
	testPath := fmt.Sprintf("/amnesia_test_%d.txt", time.Now().UnixNano())
	fd, err := client.Open(testPath, os.O_CREATE|os.O_RDWR)
	must(err)
	must(client.Close(fd))

	must(dockerAction("kill", defaultNodeServices...))
	time.Sleep(2 * time.Second)
	must(dockerAction("start", defaultNodeServices...))
	client, err = bootstrapClient(comm, 60*time.Second)
	must(err)
	leaderService = currentLeaderService(comm, client.ServerAddr)

	fd, err = client.Open(testPath, os.O_RDWR)
	must(err)
	must(client.Close(fd))
	log.Println("PASS: Election Amnesia")

	// Test 2: Interrupted Metadata Create Recovery
	log.Println("--- Test Case 2: Interrupted Metadata Create Recovery ---")
	partialPath := fmt.Sprintf("/partial_write_%d.txt", time.Now().UnixNano())
	followers := make([]string, 0, 2)
	for _, svc := range defaultNodeServices {
		if svc != leaderService {
			followers = append(followers, svc)
		}
	}
	must(dockerAction("pause", followers...))
	go func() {
		time.Sleep(20 * time.Millisecond)
		_ = dockerAction("kill", leaderService)
	}()
	_, _ = openWithTimeout(client, partialPath, os.O_CREATE|os.O_RDWR, 1500*time.Millisecond)
	time.Sleep(500 * time.Millisecond)
	must(dockerAction("unpause", followers...))
	must(dockerAction("start", leaderService))

	client, err = bootstrapClient(comm, 60*time.Second)
	must(err)
	leaderService = currentLeaderService(comm, client.ServerAddr)

	expectedVisible, err := waitForConvergedPathState(comm, partialPath, 45*time.Second)
	must(err)
	if expectedVisible {
		log.Printf("PASS: Interrupted create converged to visible state for %s", partialPath)
	} else {
		log.Printf("PASS: Interrupted create converged to absent state for %s", partialPath)
	}

	// Test 3: Snapshot Integrity Under Crash
	log.Println("--- Test Case 3: Snapshot Integrity ---")
	crashFollower := "sandstore-hyperconverged-2"
	if crashFollower == leaderService {
		crashFollower = "sandstore-hyperconverged-3"
	}
	go func() {
		time.Sleep(1 * time.Second)
		_ = dockerAction("kill", crashFollower)
		time.Sleep(2 * time.Second)
		_ = dockerAction("start", crashFollower)
	}()
	client, lastPath, err := createFiles(comm, client, fmt.Sprintf("snapshot_integrity_%d", time.Now().UnixNano()), snapshotFileCount)
	must(err)
	crashFollowerAddr := crashFollower + ":8080"
	must(waitForPathOnNode(comm, crashFollowerAddr, lastPath, 90*time.Second))
	log.Println("PASS: Snapshot Integrity")

	// Test 4: Catch-Up via InstallSnapshot
	log.Println("--- Test Case 4: InstallSnapshot Catch-Up ---")
	lagger := "sandstore-hyperconverged-3"
	if lagger == leaderService {
		lagger = "sandstore-hyperconverged-2"
	}
	must(dockerAction("pause", lagger))
	client, catchupPath, err := createFiles(comm, client, fmt.Sprintf("install_snapshot_%d", time.Now().UnixNano()), snapshotFileCount)
	must(err)
	must(dockerAction("unpause", lagger))

	laggerAddr := lagger + ":8080"
	must(waitForPathOnNode(comm, laggerAddr, catchupPath, 120*time.Second))
	log.Println("PASS: InstallSnapshot Catch-Up")

	log.Println("All durability smoke tests passed")
}
