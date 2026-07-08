package e2e

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestLiveDaemonAndMCP(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping live daemon e2e in short mode")
	}

	origin := healthServer(t)
	tunnel := healthServer(t)
	apiAddress := freeAddress(t)
	apiBaseURL := "http://" + apiAddress
	tempDir := t.TempDir()
	binary := filepath.Join(tempDir, binaryName())
	configPath := filepath.Join(tempDir, "janus.yaml")
	registryPath := filepath.Join(tempDir, "registry.json")

	buildJanus(t, binary)
	writeConfig(t, configPath, registryPath, apiAddress, origin.URL, tunnel.URL)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	daemon := exec.CommandContext(ctx, binary, "run", "--config", configPath)
	var daemonOutput bytes.Buffer
	daemon.Stdout = &daemonOutput
	daemon.Stderr = &daemonOutput
	if err := daemon.Start(); err != nil {
		t.Fatalf("failed to start daemon: %v", err)
	}
	t.Cleanup(func() {
		cancel()
		_ = daemon.Wait()
	})

	waitForDaemon(t, apiBaseURL, &daemonOutput)
	status := getJSON(t, apiBaseURL+"/api/status")
	if healthy, _ := status["healthy"].(bool); !healthy {
		t.Fatalf("expected healthy daemon status, got %#v", status)
	}

	services := getJSONArray(t, apiBaseURL+"/api/services")
	if len(services) != 1 {
		t.Fatalf("expected one service, got %#v", services)
	}
	service := services[0].(map[string]any)
	if service["activeTunnel"] != "primary" {
		t.Fatalf("expected primary active tunnel, got %#v", service)
	}
	if service["hostname"] != "grafana.janus.dev" {
		t.Fatalf("expected grafana hostname, got %#v", service)
	}

	mcpResponse := callMCPStatusTool(t, binary, apiBaseURL)
	if !strings.Contains(mcpResponse, "grafana.janus.dev") {
		t.Fatalf("MCP status response missing service hostname: %s", mcpResponse)
	}
	if !strings.Contains(mcpResponse, `"isError":false`) {
		t.Fatalf("MCP status response errored: %s", mcpResponse)
	}
}

func healthServer(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte("ok"))
	}))
	t.Cleanup(server.Close)
	return server
}

func buildJanus(t *testing.T, binary string) {
	t.Helper()
	cmd := exec.Command("go", "build", "-o", binary, "../../cmd/janus")
	cmd.Dir = filepath.Join("..", "e2e")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go build failed: %v\n%s", err, output)
	}
}

func writeConfig(t *testing.T, path, registryPath, apiAddress, originURL, tunnelURL string) {
	t.Helper()
	config := fmt.Sprintf(`
server:
  address: %s
registry:
  path: %s
  refreshInterval: 1s
  timeout: 1s
services:
  - service:
      name: grafana
    local:
      url: %s
    public:
      hostname: grafana.janus.dev
    health:
      path: /health
    tunnels:
      - id: primary
        url: %s
`, apiAddress, registryPath, originURL, tunnelURL)
	if err := os.WriteFile(path, []byte(config), 0o600); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}
}

func waitForDaemon(t *testing.T, baseURL string, output *bytes.Buffer) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(baseURL + "/api/status")
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("daemon did not become ready; output:\n%s", output.String())
}

func getJSON(t *testing.T, rawURL string) map[string]any {
	t.Helper()
	resp, err := http.Get(rawURL)
	if err != nil {
		t.Fatalf("GET %s failed: %v", rawURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET %s returned %d: %s", rawURL, resp.StatusCode, body)
	}
	var decoded map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		t.Fatalf("failed to decode %s: %v", rawURL, err)
	}
	return decoded
}

func getJSONArray(t *testing.T, rawURL string) []any {
	t.Helper()
	resp, err := http.Get(rawURL)
	if err != nil {
		t.Fatalf("GET %s failed: %v", rawURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET %s returned %d: %s", rawURL, resp.StatusCode, body)
	}
	var decoded []any
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		t.Fatalf("failed to decode %s: %v", rawURL, err)
	}
	return decoded
}

func callMCPStatusTool(t *testing.T, binary, baseURL string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, binary, "mcp", "--base-url", baseURL, "--timeout", "2s")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("failed to open MCP stdin: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("failed to open MCP stdout: %v", err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start MCP server: %v", err)
	}

	_, err = stdin.Write(frame(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"janus_get_status","arguments":{}}}`))
	if err != nil {
		t.Fatalf("failed to write MCP request: %v", err)
	}
	if err := stdin.Close(); err != nil {
		t.Fatalf("failed to close MCP stdin: %v", err)
	}

	body, err := readFrame(bufio.NewReader(stdout))
	if err != nil {
		t.Fatalf("failed to read MCP response: %v; stderr: %s", err, stderr.String())
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("MCP server exited with error: %v; stderr: %s", err, stderr.String())
	}
	return string(body)
}

func frame(payload string) []byte {
	return []byte(fmt.Sprintf("Content-Length: %d\r\n\r\n%s", len(payload), payload))
}

func readFrame(reader *bufio.Reader) ([]byte, error) {
	contentLength := -1
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		name, value, ok := strings.Cut(line, ":")
		if !ok {
			return nil, fmt.Errorf("invalid header %q", line)
		}
		if strings.EqualFold(strings.TrimSpace(name), "Content-Length") {
			parsed, err := strconv.Atoi(strings.TrimSpace(value))
			if err != nil {
				return nil, err
			}
			contentLength = parsed
		}
	}
	if contentLength < 0 {
		return nil, fmt.Errorf("missing Content-Length")
	}
	body := make([]byte, contentLength)
	_, err := io.ReadFull(reader, body)
	return body, err
}

func freeAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to allocate free port: %v", err)
	}
	defer listener.Close()
	return listener.Addr().String()
}

func binaryName() string {
	if runtime.GOOS == "windows" {
		return "janus.exe"
	}
	return "janus"
}
