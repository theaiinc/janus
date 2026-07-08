package recovery

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"sync/atomic"
	"time"

	"github.com/theaiinc/janus/internal/config"
	"github.com/theaiinc/janus/internal/events"
	"github.com/theaiinc/janus/internal/notify"
)

type Supervisor interface {
	Restart(context.Context) error
}

type HealthRetrier interface {
	RetryHealthCheck(context.Context) error
}

type Engine struct {
	supervisor    Supervisor
	healthRetrier HealthRetrier
	notifications *notify.Manager
	recorder      *events.Recorder
	total         atomic.Uint64
}

func NewEngine(supervisor Supervisor, retrier HealthRetrier, notifications *notify.Manager, recorder *events.Recorder) *Engine {
	return &Engine{
		supervisor:    supervisor,
		healthRetrier: retrier,
		notifications: notifications,
		recorder:      recorder,
	}
}

func (e *Engine) Total() uint64 {
	return e.total.Load()
}

func (e *Engine) Run(ctx context.Context, tunnel config.TunnelConfig, tunnelID, reason string) error {
	e.total.Add(1)
	if e.recorder != nil {
		e.recorder.Add(events.TypeRecovery, tunnelID, "recovery started", map[string]string{"reason": reason})
	}

	var firstErr error
	for _, step := range tunnel.Recovery {
		if err := e.runStep(ctx, tunnel, tunnelID, step, reason); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			if e.recorder != nil {
				e.recorder.Add(events.TypeWarning, tunnelID, "recovery step failed", map[string]string{"action": step.Action, "error": err.Error()})
			}
			continue
		}
		if e.recorder != nil {
			e.recorder.Add(events.TypeInfo, tunnelID, "recovery step completed", map[string]string{"action": step.Action})
		}
	}
	return firstErr
}

func (e *Engine) runStep(ctx context.Context, tunnel config.TunnelConfig, tunnelID string, step config.RecoveryStep, reason string) error {
	action := strings.TrimSpace(step.Action)
	switch action {
	case "retry", "retry-health-check":
		if e.healthRetrier == nil {
			return nil
		}
		return e.healthRetrier.RetryHealthCheck(ctx)
	case "restart", "reconnect":
		if e.supervisor == nil {
			return nil
		}
		stepCtx, cancel := stepContext(ctx, step)
		defer cancel()
		return e.supervisor.Restart(stepCtx)
	case "custom-script", "restart-network", "restart-docker-container", "restart-podman-container", "reboot":
		if strings.TrimSpace(step.Command) == "" {
			return fmt.Errorf("%s requires an explicit command", action)
		}
		stepCtx, cancel := stepContext(ctx, step)
		defer cancel()
		return runCommand(stepCtx, step.Command, step.Args)
	case "notify":
		if e.notifications == nil {
			return nil
		}
		return e.notifications.Send(ctx, tunnel.Notifications, notify.Event{
			TunnelID: tunnelID,
			Severity: "warning",
			Message:  "recovery notification: " + reason,
			Metadata: map[string]string{"action": action},
		})
	default:
		return fmt.Errorf("unsupported recovery action %q", action)
	}
}

func stepContext(ctx context.Context, step config.RecoveryStep) (context.Context, context.CancelFunc) {
	timeout := step.Timeout.Duration
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return context.WithTimeout(ctx, timeout)
}

func runCommand(ctx context.Context, command string, args []string) error {
	var cmd *exec.Cmd
	if len(args) > 0 {
		cmd = exec.CommandContext(ctx, command, args...)
	} else if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(ctx, "cmd", "/C", command)
	} else {
		cmd = exec.CommandContext(ctx, "sh", "-c", command)
	}
	return cmd.Run()
}
