package health

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/theaiinc/janus/internal/config"
	"github.com/theaiinc/janus/internal/supervisor"
)

type ProcessStatusProvider interface {
	Status() supervisor.ProcessStatus
}

type Check struct {
	Name      string        `json:"name"`
	Healthy   bool          `json:"healthy"`
	Latency   time.Duration `json:"latency"`
	Error     string        `json:"error,omitempty"`
	CheckedAt time.Time     `json:"checkedAt"`
}

type Result struct {
	Healthy       bool          `json:"healthy"`
	Score         int           `json:"score"`
	Latency       time.Duration `json:"latency"`
	Error         string        `json:"error,omitempty"`
	ProcessStatus supervisor.ProcessStatus
	Checks        []Check `json:"checks"`
}

type Checker struct {
	client  *http.Client
	tunnel  config.TunnelConfig
	process ProcessStatusProvider
}

func NewChecker(tunnel config.TunnelConfig, process ProcessStatusProvider) *Checker {
	return &Checker{
		client:  &http.Client{},
		tunnel:  tunnel,
		process: process,
	}
}

func (c *Checker) Check(ctx context.Context) Result {
	timeout := c.tunnel.Health.Timeout.Duration
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	checkCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var checks []Check
	processStatus := supervisor.ProcessStatus{Running: c.tunnel.Mode == "external"}
	if c.process != nil {
		processStatus = c.process.Status()
	}
	checks = append(checks, Check{
		Name:      "process",
		Healthy:   c.tunnel.Mode == "external" || processStatus.Running,
		CheckedAt: time.Now().UTC(),
	})

	for _, endpoint := range []struct {
		name string
		url  string
	}{
		{name: "local-http", url: endpointURL(c.tunnel.Health.Local)},
		{name: "remote-http", url: endpointURL(c.tunnel.Health.Remote)},
	} {
		if endpoint.url == "" {
			continue
		}
		checks = append(checks, c.checkHTTP(checkCtx, endpoint.name, endpoint.url))
	}

	for _, host := range c.tunnel.Health.DNS {
		if strings.TrimSpace(host) == "" {
			continue
		}
		checks = append(checks, c.checkDNS(checkCtx, host))
	}

	return summarize(checks, processStatus)
}

func (c *Checker) checkHTTP(ctx context.Context, name, rawURL string) Check {
	start := time.Now()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return failedCheck(name, start, err)
	}

	res, err := c.client.Do(req)
	if err != nil {
		return failedCheck(name, start, err)
	}
	defer res.Body.Close()

	expectedStatus := c.tunnel.Health.ExpectedStatus
	if expectedStatus == 0 {
		expectedStatus = http.StatusOK
	}
	if res.StatusCode != expectedStatus {
		return failedCheck(name, start, fmt.Errorf("expected status %d, got %d", expectedStatus, res.StatusCode))
	}

	if expected := c.tunnel.Health.ExpectedBodyContains; expected != "" {
		body, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
		if err != nil {
			return failedCheck(name, start, err)
		}
		if !strings.Contains(string(body), expected) {
			return failedCheck(name, start, fmt.Errorf("response body missing %q", expected))
		}
	}

	return Check{Name: name, Healthy: true, Latency: time.Since(start), CheckedAt: time.Now().UTC()}
}

func (c *Checker) checkDNS(ctx context.Context, host string) Check {
	start := time.Now()
	_, err := net.DefaultResolver.LookupHost(ctx, host)
	if err != nil {
		return failedCheck("dns:"+host, start, err)
	}
	return Check{Name: "dns:" + host, Healthy: true, Latency: time.Since(start), CheckedAt: time.Now().UTC()}
}

func summarize(checks []Check, processStatus supervisor.ProcessStatus) Result {
	if len(checks) == 0 {
		return Result{Healthy: true, Score: 100, ProcessStatus: processStatus}
	}

	failures := 0
	var latency time.Duration
	var firstErr string
	for _, check := range checks {
		latency += check.Latency
		if !check.Healthy {
			failures++
			if firstErr == "" {
				firstErr = check.Error
			}
		}
	}

	score := ((len(checks) - failures) * 100) / len(checks)
	return Result{
		Healthy:       failures == 0,
		Score:         score,
		Latency:       latency,
		Error:         firstErr,
		ProcessStatus: processStatus,
		Checks:        checks,
	}
}

func failedCheck(name string, start time.Time, err error) Check {
	return Check{Name: name, Healthy: false, Latency: time.Since(start), Error: err.Error(), CheckedAt: time.Now().UTC()}
}

func endpointURL(endpoint config.EndpointConfig) string {
	if endpoint.HTTP != "" {
		return endpoint.HTTP
	}
	return endpoint.HTTPS
}
