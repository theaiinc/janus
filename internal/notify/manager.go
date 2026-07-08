package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/theaiinc/janus/internal/config"
)

type Event struct {
	TunnelID string            `json:"tunnelId,omitempty"`
	Provider string            `json:"provider,omitempty"`
	Severity string            `json:"severity"`
	Message  string            `json:"message"`
	Metadata map[string]string `json:"metadata,omitempty"`
	Time     time.Time         `json:"time"`
}

type Manager struct {
	client   *http.Client
	webhooks []config.WebhookConfig
}

func NewManager(cfg config.NotificationsConfig) *Manager {
	return &Manager{
		client:   &http.Client{},
		webhooks: cfg.Webhooks,
	}
}

func (m *Manager) Send(ctx context.Context, enabled map[string]bool, event Event) error {
	var firstErr error
	for _, hook := range m.webhooks {
		if len(enabled) > 0 && !enabled[hook.Name] && !enabled[hook.Provider] {
			continue
		}
		if err := m.sendWebhook(ctx, hook, event); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (m *Manager) sendWebhook(ctx context.Context, hook config.WebhookConfig, event Event) error {
	timeout := hook.Timeout.Duration
	if timeout == 0 {
		timeout = 5 * time.Second
	}
	sendCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	event.Provider = hook.Provider
	event.Time = time.Now().UTC()
	payload, err := json.Marshal(event)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(sendCtx, http.MethodPost, hook.URL, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range hook.Headers {
		req.Header.Set(k, v)
	}

	res, err := m.client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return fmt.Errorf("webhook %q returned status %d", hook.Name, res.StatusCode)
	}
	return nil
}
