package cluster

import (
	"bytes"
	"os"
	"testing"
	"time"
)

func TestLeaderElectionReady(t *testing.T) {
	cfg := loadConfig(t)
	leader := waitForLeader(t, cfg, defaultLeaderWaitTimeout)
	if leader == "" {
		t.Fatal("leader address is empty")
	}
	t.Logf("leader elected: %s", leader)
}

func TestOpenSmoke(t *testing.T) {
	cfg := loadConfig(t)
	leader := waitForLeader(t, cfg, defaultLeaderWaitTimeout)
	t.Logf("leader ready: %s", leader)

	client := newClient(t, cfg)

	path := uniquePath("open-smoke")
	payloadA := []byte("hello, sandstore")
	payloadB := []byte(" + appended")

	fd, err := client.Open(path, os.O_CREATE|os.O_RDWR)
	if err != nil {
		t.Fatalf("open(create): %v", err)
	}

	initial, err := client.Read(fd, 64)
	if err != nil {
		t.Fatalf("read(initial): %v", err)
	}
	if len(initial) != 0 {
		t.Fatalf("expected empty initial read, got %d bytes", len(initial))
	}

	written, err := client.Write(fd, payloadA)
	if err != nil {
		t.Fatalf("write(payloadA): %v", err)
	}
	if written != len(payloadA) {
		t.Fatalf("write(payloadA): expected %d bytes, got %d", len(payloadA), written)
	}

	if err := client.Fsync(fd); err != nil {
		t.Fatalf("fsync(payloadA): %v", err)
	}

	written, err = client.Write(fd, payloadB)
	if err != nil {
		t.Fatalf("write(payloadB): %v", err)
	}
	if written != len(payloadB) {
		t.Fatalf("write(payloadB): expected %d bytes, got %d", len(payloadB), written)
	}

	if err := client.Close(fd); err != nil {
		t.Fatalf("close: %v", err)
	}

	expected := append(append([]byte(nil), payloadA...), payloadB...)
	got := readPath(t, client, path, len(expected)+32)
	if !bytes.Equal(got, expected) {
		t.Fatalf("read mismatch: expected %q, got %q", string(expected), string(got))
	}

	if err := client.Remove(path); err != nil {
		t.Fatalf("remove: %v", err)
	}

	expectPathMissing(t, client, path)
}

func TestDurabilitySmoke(t *testing.T) {
	cfg := loadConfig(t)
	kube := newKubectlClient(t, cfg)

	t.Run("DataSurvivesFullClusterRestart", func(t *testing.T) {
		waitForLeader(t, cfg, defaultLeaderWaitTimeout)
		client := newClient(t, cfg)

		path := uniquePath("durable-restart")
		payload := []byte("cluster restart durability payload")
		createAndWriteFile(t, client, path, payload)

		kube.scaleStatefulSet(t, cfg, 0)
		kube.scaleStatefulSet(t, cfg, 3)

		waitForLeader(t, cfg, defaultLeaderWaitTimeout)
		client = newClient(t, cfg)

		got := readPath(t, client, path, len(payload)+32)
		if !bytes.Equal(got, payload) {
			t.Fatalf("data after restart mismatch: expected %q, got %q", string(payload), string(got))
		}
	})

	t.Run("InterruptedCreateRecoversAfterLeaderDeletion", func(t *testing.T) {
		leaderAddr := waitForLeader(t, cfg, defaultLeaderWaitTimeout)
		leaderPod := serviceFromLeaderAddr(leaderAddr)
		client := newClient(t, cfg)

		path := uniquePath("interrupted-create")
		resultCh := make(chan error, 1)
		go func() {
			fd, err := openWithTimeout(client, path, os.O_CREATE|os.O_RDWR, 5*time.Second)
			if err == nil {
				_ = client.Close(fd)
			}
			resultCh <- err
		}()

		time.Sleep(150 * time.Millisecond)
		kube.deletePod(t, leaderPod)
		waitForLeader(t, cfg, defaultLeaderWaitTimeout)

		select {
		case <-resultCh:
		case <-time.After(15 * time.Second):
			t.Fatal("interrupted create did not return after leader deletion")
		}

		client = newClient(t, cfg)
		fd, err := client.Open(path, os.O_RDWR)
		if err == nil {
			if err := client.Close(fd); err != nil {
				t.Fatalf("close recovered path: %v", err)
			}
			t.Logf("interrupted create resolved to a visible path: %s", path)
		} else {
			t.Logf("interrupted create resolved to an absent path: %s (%v)", path, err)
		}

		verificationPath := uniquePath("post-failover")
		createAndWriteFile(t, client, verificationPath, []byte("post failover write"))
		mustPathVisible(t, client, verificationPath)
	})

	t.Run("ScaledDownNodeRejoinsAfterSnapshotPressure", func(t *testing.T) {
		waitForLeader(t, cfg, defaultLeaderWaitTimeout)
		client := newClient(t, cfg)

		kube.scaleStatefulSet(t, cfg, 2)
		waitForLeader(t, cfg, defaultLeaderWaitTimeout)

		lastPath := createBurst(t, client, "snapshot-catchup", cfg.snapshotFileCount)

		kube.scaleStatefulSet(t, cfg, 3)
		waitForLeader(t, cfg, defaultLeaderWaitTimeout)

		client = newClient(t, cfg)
		mustPathVisible(t, client, lastPath)

		verificationPath := uniquePath("rejoined-cluster")
		createAndWriteFile(t, client, verificationPath, []byte("cluster still writable after rejoin"))
		mustPathVisible(t, client, verificationPath)
	})
}
