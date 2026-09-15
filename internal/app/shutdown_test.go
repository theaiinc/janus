//go:build !windows

package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/theaiinc/janus/internal/config"
)

// Supervised commands run in their own process groups, so the daemon exiting doesn't take them
// down with it. Run must stop them on shutdown, or every daemon restart leaves the previous
// tunnels running beside the new ones.
func TestRunStopsSupervisedProcessesOnShutdown(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "child.pid")
	cfg, err := config.Parse([]byte(fmt.Sprintf(`
registry:
  path: %s
tunnels:
  - name: wrapper
    command: sleep 60 & echo $! > %s; wait
    health:
      interval: 1h
`, filepath.Join(dir, "registry.json"), pidFile)))
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	daemon, err := New(cfg, "")
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- daemon.Run(ctx) }()

	child := waitForPID(t, pidFile)
	cancel()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("Run did not return after its context was canceled")
	}

	deadline := time.Now().Add(2 * time.Second)
	for processAlive(child) {
		if time.Now().After(deadline) {
			t.Fatalf("supervised child %d still running after shutdown", child)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func waitForPID(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(path); err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil && pid > 0 {
				return pid
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no pid written to %s", path)
	return 0
}

func processAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
