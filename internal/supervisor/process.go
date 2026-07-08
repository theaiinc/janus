package supervisor

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

var ErrNotRunning = errors.New("process is not running")

type ProcessStatus struct {
	Running   bool
	PID       int
	StartedAt time.Time
	LastExit  string
}

type ProcessSupervisor struct {
	command string

	mu        sync.Mutex
	cmd       *exec.Cmd
	done      chan struct{}
	startedAt time.Time
	lastExit  string
	restarts  atomic.Uint64
}

func NewProcessSupervisor(command string) *ProcessSupervisor {
	return &ProcessSupervisor{command: command}
}

func (s *ProcessSupervisor) Start(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.runningLocked() {
		return nil
	}

	cmd := shellCommand(ctx, s.command)
	if err := cmd.Start(); err != nil {
		s.lastExit = err.Error()
		return err
	}

	s.cmd = cmd
	s.done = make(chan struct{})
	s.startedAt = time.Now().UTC()
	s.lastExit = ""

	go s.wait(cmd)
	return nil
}

func (s *ProcessSupervisor) Stop(ctx context.Context) error {
	s.mu.Lock()
	cmd := s.cmd
	done := s.done
	s.mu.Unlock()

	if cmd == nil || cmd.Process == nil {
		return ErrNotRunning
	}

	if err := signalProcess(cmd.Process); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}

	select {
	case <-ctx.Done():
		_ = cmd.Process.Kill()
		return ctx.Err()
	case <-time.After(5 * time.Second):
		_ = cmd.Process.Kill()
		return nil
	case <-done:
		return nil
	}
}

func (s *ProcessSupervisor) Restart(ctx context.Context) error {
	stopCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	_ = s.Stop(stopCtx)
	cancel()

	if err := s.Start(ctx); err != nil {
		return err
	}
	s.restarts.Add(1)
	return nil
}

func (s *ProcessSupervisor) Status() ProcessStatus {
	s.mu.Lock()
	defer s.mu.Unlock()

	status := ProcessStatus{
		Running:   s.runningLocked(),
		StartedAt: s.startedAt,
		LastExit:  s.lastExit,
	}
	if s.cmd != nil && s.cmd.Process != nil {
		status.PID = s.cmd.Process.Pid
	}
	return status
}

func (s *ProcessSupervisor) RestartTotal() uint64 {
	return s.restarts.Load()
}

func (s *ProcessSupervisor) wait(cmd *exec.Cmd) {
	err := cmd.Wait()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cmd != cmd {
		return
	}
	close(s.done)
	if err != nil {
		s.lastExit = err.Error()
	} else {
		s.lastExit = "exited"
	}
	s.cmd = nil
	s.done = nil
}

func (s *ProcessSupervisor) runningLocked() bool {
	return s.cmd != nil && s.cmd.Process != nil && s.cmd.ProcessState == nil
}

func shellCommand(ctx context.Context, command string) *exec.Cmd {
	if runtime.GOOS == "windows" {
		return exec.CommandContext(ctx, "cmd", "/C", command)
	}
	return exec.CommandContext(ctx, "sh", "-c", command)
}

func signalProcess(process *os.Process) error {
	if runtime.GOOS == "windows" {
		return process.Kill()
	}
	return process.Signal(syscall.SIGTERM)
}
