package emitter

import (
	"bytes"
	"context"
	"net/http"

	"github.com/theaiinc/janus/pkg/janus"
)

type Emitter struct {
	client *janus.Client
	mode   janus.TransportMode
}

func New(baseURL string, client *http.Client, mode ...janus.TransportMode) *Emitter {
	selected := janus.TransportDirect
	if len(mode) > 0 {
		selected = mode[0]
	}
	return &Emitter{client: janus.NewClient(baseURL, client), mode: selected}
}

func (e *Emitter) Register(ctx context.Context, namespace, alias string, registration janus.Registration) (janus.Alias, error) {
	return e.client.Register(ctx, namespace, alias, registration)
}

func (e *Emitter) Send(ctx context.Context, namespace, alias, path string, payload []byte, contentType string) (*http.Response, error) {
	body := bytes.NewReader(payload)
	if e.mode == janus.TransportProxy {
		return e.client.Do(ctx, http.MethodPost, dataPath(namespace, alias, path), body, contentType)
	}
	endpoint, err := e.client.ResolveEndpoint(ctx, namespace, alias)
	if err == nil {
		return e.client.DoEndpoint(ctx, endpoint, http.MethodPost, path, bytes.NewReader(payload), contentType)
	}
	if e.mode != janus.TransportAuto {
		return nil, err
	}
	return e.client.Do(ctx, http.MethodPost, dataPath(namespace, alias, path), bytes.NewReader(payload), contentType)
}

func dataPath(namespace, alias, path string) string {
	return "/api/namespaces/" + escape(namespace) + "/aliases/" + escape(alias) + "/data/" + trim(path)
}

func escape(value string) string {
	result := ""
	for _, char := range value {
		if char == '/' {
			result += "%2F"
		} else if char == ' ' {
			result += "%20"
		} else {
			result += string(char)
		}
	}
	return result
}

func trim(value string) string {
	for len(value) > 0 && value[0] == '/' {
		value = value[1:]
	}
	return value
}
