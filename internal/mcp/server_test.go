package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestServerInitializeToolsAndResource(t *testing.T) {
	client, err := NewJanusClient("http://127.0.0.1:1", time.Second)
	if err != nil {
		t.Fatalf("NewJanusClient returned error: %v", err)
	}
	server := NewServer(client)

	var input bytes.Buffer
	input.Write(frame(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`))
	input.Write(frame(`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`))
	input.Write(frame(`{"jsonrpc":"2.0","id":3,"method":"resources/list","params":{}}`))
	input.Write(frame(`{"jsonrpc":"2.0","id":4,"method":"resources/read","params":{"uri":"janus://agent-guide"}}`))

	var output bytes.Buffer
	if err := server.Serve(context.Background(), &input, &output); err != nil {
		t.Fatalf("Serve returned error: %v", err)
	}

	responses := readResponses(t, &output)
	if len(responses) != 4 {
		t.Fatalf("expected 4 responses, got %d", len(responses))
	}
	if !strings.Contains(string(responses[0]), `"protocolVersion":"2024-11-05"`) {
		t.Fatalf("initialize response missing protocol version: %s", responses[0])
	}
	if !strings.Contains(string(responses[1]), `"name":"janus_get_status"`) {
		t.Fatalf("tools/list missing status tool: %s", responses[1])
	}
	if !strings.Contains(string(responses[2]), `"uri":"janus://agent-guide"`) {
		t.Fatalf("resources/list missing guide: %s", responses[2])
	}
	if !strings.Contains(string(responses[3]), "Janus supervises Cloudflared tunnels") {
		t.Fatalf("resources/read missing guide text: %s", responses[3])
	}
}

func TestServerToolCallStatus(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/status" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"healthy":true,"services":[],"tunnels":[]}`))
	}))
	defer api.Close()

	client, err := NewJanusClient(api.URL, time.Second)
	if err != nil {
		t.Fatalf("NewJanusClient returned error: %v", err)
	}
	server := NewServer(client)

	var input bytes.Buffer
	input.Write(frame(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"janus_get_status","arguments":{}}}`))

	var output bytes.Buffer
	if err := server.Serve(context.Background(), &input, &output); err != nil {
		t.Fatalf("Serve returned error: %v", err)
	}

	responses := readResponses(t, &output)
	if len(responses) != 1 {
		t.Fatalf("expected one response, got %d", len(responses))
	}
	if !strings.Contains(string(responses[0]), `\"healthy\": true`) {
		t.Fatalf("tool result missing status JSON: %s", responses[0])
	}
	if strings.Contains(string(responses[0]), `"isError":true`) {
		t.Fatalf("tool result unexpectedly errored: %s", responses[0])
	}
}

func TestServerToolCallRegisterService(t *testing.T) {
	var body map[string]any
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/services" || r.Method != http.MethodPost {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("Decode returned error: %v", err)
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"grafana","hostname":"grafana.janus.dev"}`))
	}))
	defer api.Close()

	client, err := NewJanusClient(api.URL, time.Second)
	if err != nil {
		t.Fatalf("NewJanusClient returned error: %v", err)
	}
	server := NewServer(client)

	var input bytes.Buffer
	input.Write(frame(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"janus_register_service","arguments":{"name":"grafana","hostname":"grafana.janus.dev","localUrl":"http://localhost:3000","tunnels":[{"id":"primary","url":"https://abc123.trycloudflare.com"}]}}}`))

	var output bytes.Buffer
	if err := server.Serve(context.Background(), &input, &output); err != nil {
		t.Fatalf("Serve returned error: %v", err)
	}
	if body["name"] != "grafana" || body["hostname"] != "grafana.janus.dev" {
		t.Fatalf("unexpected register body: %#v", body)
	}
	responses := readResponses(t, &output)
	if !strings.Contains(string(responses[0]), "grafana.janus.dev") {
		t.Fatalf("tool response missing registered service: %s", responses[0])
	}
}

func TestServerToolCallMissingRequiredArgument(t *testing.T) {
	client, err := NewJanusClient("http://127.0.0.1:1", time.Second)
	if err != nil {
		t.Fatalf("NewJanusClient returned error: %v", err)
	}
	server := NewServer(client)

	var input bytes.Buffer
	input.Write(frame(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"janus_get_service","arguments":{}}}`))

	var output bytes.Buffer
	if err := server.Serve(context.Background(), &input, &output); err != nil {
		t.Fatalf("Serve returned error: %v", err)
	}
	responses := readResponses(t, &output)
	if !strings.Contains(string(responses[0]), `"isError":true`) {
		t.Fatalf("expected tool error result: %s", responses[0])
	}
	if !strings.Contains(string(responses[0]), `missing required argument`) {
		t.Fatalf("expected missing argument message: %s", responses[0])
	}
}

func readResponses(t *testing.T, output *bytes.Buffer) [][]byte {
	t.Helper()
	reader := bufio.NewReader(output)
	var responses [][]byte
	for {
		body, err := readMessage(reader)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("readMessage returned error: %v", err)
		}
		responses = append(responses, body)
	}
	return responses
}
