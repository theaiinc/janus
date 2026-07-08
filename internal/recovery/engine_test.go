package recovery

import (
	"context"
	"errors"
	"testing"

	"github.com/theaiinc/janus/internal/config"
	"github.com/theaiinc/janus/internal/events"
)

type fakeSupervisor struct {
	restarts int
	err      error
}

func (f *fakeSupervisor) Restart(context.Context) error {
	f.restarts++
	return f.err
}

type fakeRetrier struct {
	retries int
	err     error
}

func (f *fakeRetrier) RetryHealthCheck(context.Context) error {
	f.retries++
	return f.err
}

func TestEngineRunsConfiguredSteps(t *testing.T) {
	supervisor := &fakeSupervisor{}
	retrier := &fakeRetrier{}
	recorder := events.NewRecorder(10)
	engine := NewEngine(supervisor, retrier, nil, recorder)

	err := engine.Run(context.Background(), config.TunnelConfig{
		Name: "production",
		Recovery: []config.RecoveryStep{
			{Action: "retry-health-check"},
			{Action: "restart"},
		},
	}, "production", "test")
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if retrier.retries != 1 {
		t.Fatalf("expected one retry, got %d", retrier.retries)
	}
	if supervisor.restarts != 1 {
		t.Fatalf("expected one restart, got %d", supervisor.restarts)
	}
	if engine.Total() != 1 {
		t.Fatalf("expected total 1, got %d", engine.Total())
	}
	if len(recorder.List()) != 3 {
		t.Fatalf("expected 3 events, got %d", len(recorder.List()))
	}
}

func TestEngineReturnsFirstStepErrorAndContinues(t *testing.T) {
	expected := errors.New("still unhealthy")
	supervisor := &fakeSupervisor{}
	retrier := &fakeRetrier{err: expected}
	engine := NewEngine(supervisor, retrier, nil, events.NewRecorder(10))

	err := engine.Run(context.Background(), config.TunnelConfig{
		Name: "production",
		Recovery: []config.RecoveryStep{
			{Action: "retry-health-check"},
			{Action: "restart"},
		},
	}, "production", "test")
	if !errors.Is(err, expected) {
		t.Fatalf("expected first error, got %v", err)
	}
	if supervisor.restarts != 1 {
		t.Fatalf("expected restart after retry failure, got %d", supervisor.restarts)
	}
}

func TestEngineRejectsUnknownAction(t *testing.T) {
	engine := NewEngine(nil, nil, nil, events.NewRecorder(10))
	err := engine.Run(context.Background(), config.TunnelConfig{
		Name: "production",
		Recovery: []config.RecoveryStep{
			{Action: "nope"},
		},
	}, "production", "test")
	if err == nil {
		t.Fatal("expected error")
	}
}
