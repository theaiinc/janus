package supervisor

import (
	"context"
	"runtime"
	"testing"
	"time"
)

func TestProcessSupervisorStartRestartStop(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell sleep command is Unix-specific")
	}

	ctx := context.Background()
	s := NewProcessSupervisor("sleep 60")
	if err := s.Start(ctx); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	if !s.Status().Running {
		t.Fatal("expected process to be running")
	}
	if err := s.Restart(ctx); err != nil {
		t.Fatalf("Restart returned error: %v", err)
	}
	if s.RestartTotal() != 1 {
		t.Fatalf("expected restart total 1, got %d", s.RestartTotal())
	}
	if !s.Status().Running {
		t.Fatal("expected process to be running after restart")
	}
	stopCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := s.Stop(stopCtx); err != nil {
		t.Fatalf("Stop returned error: %v", err)
	}
	eventually(t, time.Second, func() bool {
		return !s.Status().Running
	})
}

func TestProcessSupervisorDetectsExitedProcess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell exit command is Unix-specific")
	}

	s := NewProcessSupervisor("exit 7")
	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	eventually(t, time.Second, func() bool {
		return !s.Status().Running && s.Status().LastExit != ""
	})
}

func eventually(t *testing.T, timeout time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition was not met before timeout")
}
