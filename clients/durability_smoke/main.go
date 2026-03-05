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

var defaultNodeServices = []string{"node-1", "node-2", "node-3"}

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
		return []string{"node-1:8080", "node-2:8080", "node-3:8080"}
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
		return []string{"node-1:8080", "node-2:8080", "node-3:8080"}
	}
	return addrs
}

func runDockerCmd(args ...string) error {
	cmd := exec.Command("docker", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker %s failed: %w (%s)", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
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

func waitForLeader(comm *grpccomm.GRPCCommunicator, timeout time.Duration) (*sandlib.SandstoreClient, string, error) {
	deadline := time.Now().Add(timeout)
	probe := fmt.Sprintf("/durability_probe_%d", time.Now().UnixNano())
	for time.Now().Before(deadline) {
		for _, addr := range nodeAddresses() {
			c := sandlib.NewSandstoreClient(addr, comm)
			fd, err := c.Open(probe, os.O_CREATE|os.O_RDWR)
			if err == nil {
				_ = c.Close(fd)
				return c, addr, nil
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	return nil, "", fmt.Errorf("leader not discovered within %s", timeout)
}

func createFiles(comm *grpccomm.GRPCCommunicator, client *sandlib.SandstoreClient, prefix string, count int) (*sandlib.SandstoreClient, string, error) {
	lastPath := ""
	for i := 0; i < count; i++ {
		path := fmt.Sprintf("/%s_%06d", prefix, i)
		var lastErr error
		for attempt := 0; attempt < 8; attempt++ {
			fd, err := client.Open(path, os.O_CREATE|os.O_RDWR)
			if err == nil {
				if err := client.Close(fd); err != nil {
					lastErr = fmt.Errorf("close %s: %w", path, err)
					time.Sleep(200 * time.Millisecond)
					continue
				}
				lastErr = nil
				break
			}
			lastErr = err

			nextClient, _, leaderErr := waitForLeader(comm, 20*time.Second)
			if leaderErr == nil {
				client = nextClient
			}
			time.Sleep(200 * time.Millisecond)
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
		c := sandlib.NewSandstoreClient(addr, comm)
		fd, err := c.Open(path, os.O_RDWR)
		if err == nil {
			_ = c.Close(fd)
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("path %s not visible on %s within %s", path, addr, timeout)
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

	client, leaderAddr, err := waitForLeader(comm, 60*time.Second)
	must(err)
	leaderService := serviceFromAddr(leaderAddr)
	log.Printf("Leader discovered at %s", leaderAddr)

	// Test 1: Election Amnesia
	log.Println("--- Test Case 1: Election Amnesia ---")
	testPath := fmt.Sprintf("/amnesia_test_%d.txt", time.Now().UnixNano())
	fd, err := client.Open(testPath, os.O_CREATE|os.O_RDWR)
	must(err)
	must(client.Close(fd))

	must(dockerAction("kill", defaultNodeServices...))
	time.Sleep(2 * time.Second)
	must(dockerAction("start", defaultNodeServices...))
	client, _, err = waitForLeader(comm, 60*time.Second)
	must(err)

	fd, err = client.Open(testPath, os.O_RDWR)
	must(err)
	must(client.Close(fd))
	log.Println("PASS: Election Amnesia")

	// Test 2: Uncommitted Write Isolation
	log.Println("--- Test Case 2: Uncommitted Write Isolation ---")
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

	client, leaderAddr, err = waitForLeader(comm, 60*time.Second)
	must(err)
	leaderService = serviceFromAddr(leaderAddr)

	if fd, err = client.Open(partialPath, os.O_RDWR); err == nil {
		_ = client.Close(fd)
		log.Fatalf("FAIL: uncommitted write %s became visible", partialPath)
	}
	log.Println("PASS: Uncommitted Write Isolation")

	// Test 3: Snapshot Integrity Under Crash
	log.Println("--- Test Case 3: Snapshot Integrity ---")
	crashFollower := "node-2"
	if crashFollower == leaderService {
		crashFollower = "node-3"
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
	lagger := "node-3"
	if lagger == leaderService {
		lagger = "node-2"
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
