package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
)

type Server struct {
	client *JanusClient
}

func NewServer(client *JanusClient) *Server {
	return &Server{client: client}
}

func (s *Server) Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	reader := bufio.NewReader(in)
	for {
		body, err := readMessage(reader)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}

		var req request
		if err := json.Unmarshal(body, &req); err != nil {
			if writeErr := writeMessage(out, response{JSONRPC: "2.0", Error: &rpcError{Code: -32700, Message: "parse error"}}); writeErr != nil {
				return writeErr
			}
			continue
		}
		if len(req.ID) == 0 {
			continue
		}

		result, rpcErr := s.handle(ctx, req)
		res := response{JSONRPC: "2.0", ID: req.ID, Result: result, Error: rpcErr}
		if rpcErr != nil {
			res.Result = nil
		}
		if err := writeMessage(out, res); err != nil {
			return err
		}
	}
}

func (s *Server) handle(ctx context.Context, req request) (any, *rpcError) {
	switch req.Method {
	case "initialize":
		return map[string]any{
			"protocolVersion": protocolVersion,
			"capabilities": map[string]any{
				"tools":     map[string]any{},
				"resources": map[string]any{},
			},
			"serverInfo": map[string]any{
				"name":    "janus",
				"version": "dev",
			},
		}, nil
	case "tools/list":
		return map[string]any{"tools": tools()}, nil
	case "tools/call":
		return s.handleToolCall(ctx, req.Params)
	case "resources/list":
		return map[string]any{"resources": []resource{guideResource()}}, nil
	case "resources/read":
		return s.handleResourceRead(req.Params)
	default:
		return nil, &rpcError{Code: -32601, Message: "method not found"}
	}
}

func (s *Server) handleToolCall(ctx context.Context, raw json.RawMessage) (any, *rpcError) {
	var params struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	if err := json.Unmarshal(raw, &params); err != nil {
		return nil, &rpcError{Code: -32602, Message: "invalid tools/call params"}
	}

	result, err := s.callTool(ctx, params.Name, params.Arguments)
	if err != nil {
		return toolResult(fmt.Sprintf("error: %v", err), true), nil
	}
	return toolJSONResult(result), nil
}

func (s *Server) callTool(ctx context.Context, name string, args map[string]any) (any, error) {
	switch name {
	case "janus_get_status":
		return s.client.GetStatus(ctx)
	case "janus_list_tunnels":
		return s.client.ListTunnels(ctx)
	case "janus_restart_tunnel":
		id, err := requiredString(args, "id")
		if err != nil {
			return nil, err
		}
		return s.client.RestartTunnel(ctx, id)
	case "janus_recover_tunnel":
		id, err := requiredString(args, "id")
		if err != nil {
			return nil, err
		}
		return s.client.RecoverTunnel(ctx, id)
	case "janus_list_services":
		return s.client.ListServices(ctx)
	case "janus_get_service":
		id, err := requiredString(args, "id")
		if err != nil {
			return nil, err
		}
		return s.client.GetService(ctx, id)
	case "janus_register_service":
		return s.client.RegisterService(ctx, args)
	case "janus_unregister_service":
		id, err := requiredString(args, "id")
		if err != nil {
			return nil, err
		}
		return s.client.UnregisterService(ctx, id)
	case "janus_get_service_health":
		id, err := requiredString(args, "id")
		if err != nil {
			return nil, err
		}
		return s.client.GetServiceHealth(ctx, id)
	case "janus_list_service_tunnels":
		id, err := requiredString(args, "id")
		if err != nil {
			return nil, err
		}
		return s.client.ListServiceTunnels(ctx, id)
	case "janus_refresh_service":
		id, err := requiredString(args, "id")
		if err != nil {
			return nil, err
		}
		return s.client.RefreshService(ctx, id)
	case "janus_get_events":
		return s.client.GetEvents(ctx)
	case "janus_get_metrics":
		return s.client.GetMetrics(ctx)
	default:
		return nil, fmt.Errorf("unknown tool %q", name)
	}
}

func (s *Server) handleResourceRead(raw json.RawMessage) (any, *rpcError) {
	var params struct {
		URI string `json:"uri"`
	}
	if err := json.Unmarshal(raw, &params); err != nil {
		return nil, &rpcError{Code: -32602, Message: "invalid resources/read params"}
	}
	if params.URI != "janus://agent-guide" {
		return nil, &rpcError{Code: -32602, Message: "unknown resource"}
	}
	return map[string]any{
		"contents": []map[string]string{{
			"uri":      "janus://agent-guide",
			"mimeType": "text/markdown",
			"text":     agentGuide(),
		}},
	}, nil
}

type tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

type resource struct {
	URI         string `json:"uri"`
	Name        string `json:"name"`
	Description string `json:"description"`
	MimeType    string `json:"mimeType"`
}

func tools() []tool {
	all := []tool{
		noArgTool("janus_get_status", "Get aggregate Janus daemon, tunnel, and service health."),
		noArgTool("janus_list_tunnels", "List supervised Cloudflared tunnel statuses."),
		idTool("janus_restart_tunnel", "Restart a supervised Cloudflared tunnel by ID."),
		idTool("janus_recover_tunnel", "Run the configured recovery chain for a tunnel by ID."),
		noArgTool("janus_list_services", "List registered services and active tunnel mappings."),
		idTool("janus_get_service", "Get one registered service by ID."),
		registerServiceTool(),
		idTool("janus_unregister_service", "Remove a service registration by ID."),
		idTool("janus_get_service_health", "Get health details for one service."),
		idTool("janus_list_service_tunnels", "List known tunnel endpoints for one service."),
		idTool("janus_refresh_service", "Refresh service health and active tunnel selection."),
		noArgTool("janus_get_events", "List recent Janus events."),
		noArgTool("janus_get_metrics", "Get Prometheus metrics text from Janus."),
	}
	sort.Slice(all, func(i, j int) bool { return all[i].Name < all[j].Name })
	return all
}

func noArgTool(name, description string) tool {
	return tool{Name: name, Description: description, InputSchema: objectSchema(nil, nil)}
}

func idTool(name, description string) tool {
	return tool{
		Name:        name,
		Description: description,
		InputSchema: objectSchema(map[string]any{
			"id": map[string]any{"type": "string", "description": "Janus tunnel or service ID."},
		}, []string{"id"}),
	}
}

func registerServiceTool() tool {
	return tool{
		Name:        "janus_register_service",
		Description: "Register a service with stable hostname, local URL, optional health path, tags, labels, and Cloudflared tunnel endpoints.",
		InputSchema: objectSchema(map[string]any{
			"id":         map[string]any{"type": "string"},
			"name":       map[string]any{"type": "string"},
			"hostname":   map[string]any{"type": "string"},
			"localUrl":   map[string]any{"type": "string"},
			"healthPath": map[string]any{"type": "string"},
			"tunnels": map[string]any{
				"type": "array",
				"items": objectSchema(map[string]any{
					"id":  map[string]any{"type": "string"},
					"url": map[string]any{"type": "string"},
				}, []string{"id", "url"}),
			},
			"tags": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"labels": map[string]any{
				"type":                 "object",
				"additionalProperties": map[string]any{"type": "string"},
			},
		}, []string{"name", "hostname", "localUrl"}),
	}
}

func objectSchema(properties map[string]any, required []string) map[string]any {
	if properties == nil {
		properties = map[string]any{}
	}
	schema := map[string]any{
		"type":                 "object",
		"properties":           properties,
		"additionalProperties": false,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func guideResource() resource {
	return resource{
		URI:         "janus://agent-guide",
		Name:        "Janus Agent Guide",
		Description: "How agents should inspect and operate Janus safely.",
		MimeType:    "text/markdown",
	}
}

func agentGuide() string {
	return strings.TrimSpace(`
# Janus Agent Guide

Janus supervises Cloudflared tunnels and maintains a service registry that maps stable public service hostnames to healthy Cloudflared tunnel URLs.

Use read-only tools first:

- ` + "`janus_get_status`" + ` for aggregate health.
- ` + "`janus_list_services`" + ` and ` + "`janus_get_service`" + ` for stable public endpoint mappings.
- ` + "`janus_list_tunnels`" + ` for supervised tunnel process state.
- ` + "`janus_get_events`" + ` for recent state changes and recovery history.

Use action tools only when needed:

- ` + "`janus_refresh_service`" + ` safely reevaluates health and active tunnel selection.
- ` + "`janus_recover_tunnel`" + ` runs configured recovery steps.
- ` + "`janus_restart_tunnel`" + ` restarts a supervised tunnel process and may briefly interrupt traffic.
- ` + "`janus_register_service`" + ` and ` + "`janus_unregister_service`" + ` change the service registry.

Janus does not manage Cloudflare DNS, act as a reverse proxy, authenticate users, or load balance traffic.
`)
}

func toolJSONResult(value any) any {
	if text, ok := value.(string); ok {
		return toolResult(text, false)
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return toolResult(fmt.Sprintf("error: %v", err), true)
	}
	return toolResult(string(data), false)
}

func toolResult(text string, isError bool) any {
	return map[string]any{
		"content": []map[string]string{{
			"type": "text",
			"text": text,
		}},
		"isError": isError,
	}
}

func requiredString(args map[string]any, key string) (string, error) {
	value, ok := args[key].(string)
	if !ok || strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("missing required argument %q", key)
	}
	return strings.TrimSpace(value), nil
}
