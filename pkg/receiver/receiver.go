package receiver

import (
	"context"
	"io"
	"net/http"

	"github.com/theaiinc/janus/pkg/janus"
)

type Receiver struct {
	client *janus.Client
	mode   janus.TransportMode
}

func New(baseURL string, client *http.Client, mode ...janus.TransportMode) *Receiver {
	selected := janus.TransportDirect
	if len(mode) > 0 {
		selected = mode[0]
	}
	return &Receiver{client: janus.NewClient(baseURL, client), mode: selected}
}

func (r *Receiver) Resolve(ctx context.Context, namespace, alias string) (janus.Alias, error) {
	return r.client.Resolve(ctx, namespace, alias)
}

func (r *Receiver) ResolveEndpoint(ctx context.Context, namespace, alias string) (janus.Endpoint, error) {
	return r.client.ResolveEndpoint(ctx, namespace, alias)
}

func (r *Receiver) Receive(ctx context.Context, namespace, alias, path string) (*http.Response, error) {
	return r.request(ctx, http.MethodGet, namespace, alias, path, nil, "")
}

func (r *Receiver) Request(ctx context.Context, method, namespace, alias, path string, body io.Reader, contentType string) (*http.Response, error) {
	return r.request(ctx, method, namespace, alias, path, body, contentType)
}

func (r *Receiver) request(ctx context.Context, method, namespace, alias, path string, body io.Reader, contentType string) (*http.Response, error) {
	if r.mode == janus.TransportProxy {
		return r.client.Do(ctx, method, dataPath(namespace, alias, path), body, contentType)
	}
	endpoint, err := r.client.ResolveEndpoint(ctx, namespace, alias)
	if err == nil {
		return r.client.DoEndpoint(ctx, endpoint, method, path, body, contentType)
	}
	if r.mode != janus.TransportAuto {
		return nil, err
	}
	return r.client.Do(ctx, method, dataPath(namespace, alias, path), body, contentType)
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
