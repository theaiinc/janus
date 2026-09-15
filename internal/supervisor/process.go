package supervisor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

var ErrNotRunning = errors.New("process is not running")

var logNameSanitizer = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

type ProcessStatus struct {
	Running   bool
	PID       int
	StartedAt time.Time
	LastExit  string
}

type ProcessSupervisor struct {
	command string
	name    string

	mu        sync.Mutex
	cmd       *exec.Cmd
	logFile   *os.File
	done      chan struct{}
	startedAt time.Time
	lastExit  string
	restarts  atomic.Uint64
}

// name is used only to pick a log file (/tmp/janus-tunnel-<name>.log,
// truncated on each Start) for the child's own stdout/stderr — previously
// nothing captured a supervised process's output at all, which made a
// silent, repeated spawn/exec failure indistinguishable from "healthy" apart
// from processRunning staying false. An empty name falls back to a
// command-derived one so this never panics on a zero-value NewProcessSupervisor("").
func NewProcessSupervisor(command string, name string) *ProcessSupervisor {
	if name == "" {
		name = command
	}
	return &ProcessSupervisor{command: command, name: logNameSanitizer.ReplaceAllString(name, "-")}
}

func (s *ProcessSupervisor) Start(_ context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.runningLocked() {
		return nil
	}

	// Deliberately NOT exec.CommandContext(ctx, ...): callers (recovery's
	// "restart" action in particular) pass a step-scoped context that gets
	// canceled via `defer cancel()` the instant this function returns —
	// which is near-immediate, since starting a process doesn't wait for it
	// to exit. exec.CommandContext kills the child the moment its context
	// is done, so binding to that context meant every recovery-triggered
	// restart killed the process it had just started, milliseconds later —
	// a tight, silent restart loop instead of a running service. Stop()
	// already owns process shutdown explicitly (SIGTERM, then a timeout,
	// then SIGKILL), so lifetime here isn't context-governed at all.
	cmd := shellCommand(s.command)
	logFile, logErr := os.Create(filepath.Join(os.TempDir(), fmt.Sprintf("janus-tunnel-%s.log", s.name)))
	if logErr == nil {
		cmd.Stdout = logFile
		cmd.Stderr = logFile
	}
	if err := cmd.Start(); err != nil {
		s.lastExit = err.Error()
		if logFile != nil {
			_ = logFile.Close()
		}
		return err
	}
	s.logFile = logFile

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
	if s.logFile != nil {
		_ = s.logFile.Close()
		s.logFile = nil
	}
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

func shellCommand(command string) *exec.Cmd {
	if runtime.GOOS == "windows" {
		return exec.Command("cmd", "/C", command)
	}
	return exec.Command("sh", "-c", command)
}

func signalProcess(process *os.Process) error {
	if runtime.GOOS == "windows" {
		return process.Kill()
	}
	return process.Signal(syscall.SIGTERM)
}
