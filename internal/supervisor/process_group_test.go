//go:build !windows

package supervisor

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
)

// A wrapper script that backgrounds its real work — the shape of oasis-tunnel-alias.sh. Stopping
// the supervisor must take the background child down with the shell.
func TestProcessSupervisorStopKillsBackgroundChildren(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	s := NewProcessSupervisor(fmt.Sprintf("sleep 60 & echo $! > %s; wait", pidFile), "test-children-stop")
	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	child := readChildPID(t, pidFile)

	stopCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := s.Stop(stopCtx); err != nil {
		t.Fatalf("Stop returned error: %v", err)
	}
	eventually(t, 2*time.Second, func() bool { return !processAlive(child) })
}

func TestProcessSupervisorRestartDoesNotAccumulateChildren(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	s := NewProcessSupervisor(fmt.Sprintf("sleep 60 & echo $! > %s; wait", pidFile), "test-children-restart")
	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	first := readChildPID(t, pidFile)
	_ = os.Remove(pidFile)

	if err := s.Restart(context.Background()); err != nil {
		t.Fatalf("Restart returned error: %v", err)
	}
	second := readChildPID(t, pidFile)
	eventually(t, 2*time.Second, func() bool { return !processAlive(first) })
	if !processAlive(second) {
		t.Fatal("expected the restarted command's child to be running")
	}

	stopCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = s.Stop(stopCtx)
	eventually(t, 2*time.Second, func() bool { return !processAlive(second) })
}

// The script exits on its own (set -e, a crash) and leaves its background child behind.
func TestProcessSupervisorReapsChildrenWhenCommandExits(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	s := NewProcessSupervisor(fmt.Sprintf("sleep 60 & echo $! > %s; sleep 0.2; exit 3", pidFile), "test-children-exit")
	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	child := readChildPID(t, pidFile)

	eventually(t, 3*time.Second, func() bool { return !s.Status().Running })
	eventually(t, 2*time.Second, func() bool { return !processAlive(child) })
}

func readChildPID(t *testing.T, path string) int {
	t.Helper()
	var pid int
	eventually(t, 2*time.Second, func() bool {
		data, err := os.ReadFile(path)
		if err != nil {
			return false
		}
		pid, err = strconv.Atoi(strings.TrimSpace(string(data)))
		return err == nil && pid > 0
	})
	return pid
}

func processAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
