package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
)

const maxRequestBody = 8 << 20

type server struct {
	mux *http.ServeMux

	reg *registry

	requestCount atomic.Uint64
}

func newServer(reg *registry) *server {
	s := &server{
		mux: http.NewServeMux(),
		reg: reg,
	}
	s.routes()
	return s
}

func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

func (s *server) routes() {
	s.mux.HandleFunc("/healthz", s.healthz)

	s.mux.HandleFunc("/control/responses", s.controlResponses)
	s.mux.HandleFunc("/control/state", s.controlState)

	provider := s.providerHandler()

	for _, path := range providerPaths {
		s.mux.HandleFunc(path, provider)
	}

	s.mux.HandleFunc("/v1beta/models/", provider)
}

var providerPaths = []string{
	"/v1/chat/completions",
	"/v1/responses",
	"/v1/models",
	"/v1/messages",
	"/v1beta/models",
}

func (s *server) healthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok\n"))
}

func (s *server) providerHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.requestCount.Add(1)

		body, err := io.ReadAll(io.LimitReader(r.Body, maxRequestBody))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "read body: " + err.Error()})
			return
		}
		_ = r.Body.Close()

		canned, ok := s.reg.match(r.Method, r.URL.Path, string(body))
		if !ok {
			slog.Debug("no canned response matched", "method", r.Method, "path", r.URL.Path)
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "no canned response"})
			return
		}

		if canned.Streaming {
			s.writeStream(r.Context(), w, canned)
			return
		}

		s.writeNonStream(w, canned)
	}
}

func (s *server) writeNonStream(w http.ResponseWriter, c CannedResponse) {
	for k, v := range c.Headers {
		w.Header().Set(k, v)
	}
	if w.Header().Get("Content-Type") == "" {
		w.Header().Set("Content-Type", "application/json")
	}
	status := c.Status
	if status == 0 {
		status = http.StatusOK
	}
	w.WriteHeader(status)
	if c.Body != "" {
		_, _ = io.Copy(w, strings.NewReader(c.Body))
	}
}

// writeStream replays canned SSE chunks. Chunks are written verbatim wrapped
// in "data: ...\n\n" framing, flushing after every chunk. Inter-chunk delays
// honor request-context cancellation so client disconnect terminates promptly.
func (s *server) writeStream(ctx context.Context, w http.ResponseWriter, c CannedResponse) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "streaming unsupported"})
		return
	}

	for k, v := range c.Headers {
		w.Header().Set(k, v)
	}
	if w.Header().Get("Content-Type") == "" {
		w.Header().Set("Content-Type", "text/event-stream")
	}
	if w.Header().Get("Cache-Control") == "" {
		w.Header().Set("Cache-Control", "no-cache")
	}

	status := c.Status
	if status == 0 {
		status = http.StatusOK
	}
	w.WriteHeader(status)

	for _, chunk := range c.StreamChunks {
		if chunk.DelayMs > 0 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Duration(chunk.DelayMs) * time.Millisecond):
			}
		}
		if _, err := fmt.Fprintf(w, "data: %s\n\n", chunk.Data); err != nil {
			return
		}
		flusher.Flush()
	}
}

func (s *server) controlResponses(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		s.controlAdd(w, r)
	case http.MethodDelete:
		s.reg.clear()
		w.WriteHeader(http.StatusNoContent)
	default:
		w.Header().Set("Allow", "POST, DELETE")
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

func (s *server) controlAdd(w http.ResponseWriter, r *http.Request) {
	defer func() { _ = r.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(r.Body, maxRequestBody))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Errorf("read body: %w", err).Error()})
		return
	}

	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	var c CannedResponse
	if err := dec.Decode(&c); err != nil {
		if errors.Is(err, io.EOF) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "empty body"})
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "decode: " + err.Error()})
		return
	}

	s.reg.add(c)
	w.WriteHeader(http.StatusCreated)
}

func (s *server) controlState(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	state := struct {
		Responses    []CannedResponse `json:"responses"`
		RequestCount uint64           `json:"request_count"`
	}{
		Responses:    s.reg.snapshot(),
		RequestCount: s.requestCount.Load(),
	}
	writeJSON(w, http.StatusOK, state)
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
