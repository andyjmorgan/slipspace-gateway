package main

import (
	"strings"
	"sync"
)

type StreamChunk struct {
	Data string `json:"data" yaml:"data"`

	DelayMs int `json:"delay_ms,omitempty" yaml:"delay_ms,omitempty"`
}

type CannedResponse struct {
	Method string `json:"method,omitempty" yaml:"method,omitempty"`

	Path string `json:"path,omitempty" yaml:"path,omitempty"`

	RequestBodyContains string `json:"request_body_contains,omitempty" yaml:"request_body_contains,omitempty"`

	Status int `json:"status,omitempty" yaml:"status,omitempty"`

	Headers map[string]string `json:"headers,omitempty" yaml:"headers,omitempty"`

	Streaming bool `json:"streaming,omitempty" yaml:"streaming,omitempty"`

	Body string `json:"body,omitempty" yaml:"body,omitempty"`

	StreamChunks []StreamChunk `json:"stream_chunks,omitempty" yaml:"stream_chunks,omitempty"`
}

type registry struct {
	mu        sync.RWMutex
	responses []CannedResponse
}

func newRegistry() *registry {
	return &registry{responses: []CannedResponse{}}
}

func (r *registry) add(c CannedResponse) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.responses = append(r.responses, c)
}

func (r *registry) clear() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.responses = r.responses[:0]
}

func (r *registry) replace(rs []CannedResponse) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.responses = append(r.responses[:0], rs...)
}

func (r *registry) snapshot() []CannedResponse {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]CannedResponse, len(r.responses))
	copy(out, r.responses)
	return out
}

func (r *registry) match(method, path, body string) (CannedResponse, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, c := range r.responses {
		if !matchesField(c.Method, method) {
			continue
		}
		if !matchesField(c.Path, path) {
			continue
		}
		if c.RequestBodyContains != "" && !strings.Contains(body, c.RequestBodyContains) {
			continue
		}
		return c, true
	}
	return CannedResponse{}, false
}

func matchesField(criterion, actual string) bool {
	if criterion == "" {
		return true
	}
	return strings.EqualFold(criterion, actual)
}
