package admin

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	adminc "github.com/andyjmorgan/sluice-gateway/contracts/admin"
	"github.com/andyjmorgan/sluice-gateway/internal/observability/livefeed"
)

// sseRetryMs tells EventSource to back off for one second before
// reconnecting when the stream drops. Short enough that operators don't
// notice a blip, long enough not to hammer the gateway on a flapping
// listener.
const sseRetryMs = 1000

// sseHeartbeatInterval is how often the stream sends a comment frame
// to keep proxies from idle-closing the connection. SSE comments start
// with ':' and are ignored by EventSource.
const sseHeartbeatInterval = 15 * time.Second

// MessageBodyHandler serves the captured request/response bodies for
// a single event_id from the in-process body LRU. Returns 503 when
// the body store is nil (capture disabled) and 404 when the event_id
// has rolled out of the LRU.
//
// The mux registers this at the 1.22+ pattern
// `/api/v1/messages/{event_id}/body`; the event_id is read via
// r.PathValue.
func MessageBodyHandler(store *livefeed.BodyStore) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if store == nil {
			http.Error(w, "body capture disabled", http.StatusServiceUnavailable)
			return
		}
		eventID := r.PathValue("event_id")
		if eventID == "" {
			http.Error(w, "missing event_id", http.StatusBadRequest)
			return
		}
		env, ok := store.Get(eventID)
		if !ok {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		writeJSON(w, toMessageBodyDetail(eventID, env))
	})
}

// toMessageBodyDetail projects a livefeed.BodyEnvelope onto the wire
// DTO. Single mapping point so future field additions touch one site.
func toMessageBodyDetail(eventID string, env livefeed.BodyEnvelope) adminc.MessageBodyDetail {
	return adminc.MessageBodyDetail{
		EventID:            eventID,
		Request:            string(env.Request),
		RequestTotalBytes:  env.RequestTotalBytes,
		RequestTruncated:   env.RequestTruncated,
		Response:           string(env.Response),
		ResponseTotalBytes: env.ResponseTotalBytes,
		ResponseTruncated:  env.ResponseTruncated,
		ResponseAssembled:  env.ResponseAssembled,
		AssemblyPartial:    env.AssemblyPartial,
	}
}

// MessagesRecentHandler serves the current ring as JSON. Returns 503
// when the live feed is disabled (ring is nil).
//
// Query params:
//   - limit: clamped to [1, ring capacity]; default = capacity
func MessagesRecentHandler(ring *livefeed.Ring) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ring == nil {
			http.Error(w, "live feed disabled", http.StatusServiceUnavailable)
			return
		}
		limit := ring.Capacity()
		if raw := r.URL.Query().Get("limit"); raw != "" {
			n, err := strconv.Atoi(raw)
			if err != nil || n < 1 {
				http.Error(w, "limit must be a positive integer", http.StatusBadRequest)
				return
			}
			if n < limit {
				limit = n
			}
		}
		entries := ring.Recent(limit)
		out := adminc.MessagesRecentResponse{
			Capacity: ring.Capacity(),
			Entries:  make([]adminc.MessageEntry, 0, len(entries)),
		}
		for _, e := range entries {
			out.Entries = append(out.Entries, toMessageEntry(e))
		}
		writeJSON(w, out)
	})
}

// MessagesStreamHandler serves the live ring as a Server-Sent Events
// stream. The connection lives until the client disconnects or the
// server shuts the request context down. Each appended entry is sent
// as a single SSE `event: message` frame with the JSON-encoded
// MessageEntry as its data payload.
//
// When the subscriber's buffer fills (slow client / network), the ring
// increments a per-subscriber drop counter. We surface that to the
// client with a `drop` event before resuming normal delivery so the SPA
// can render a "missed N entries" notice without polling a separate
// endpoint.
//
// Returns 503 when the live feed is disabled.
func MessagesStreamHandler(ring *livefeed.Ring) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ring == nil {
			http.Error(w, "live feed disabled", http.StatusServiceUnavailable)
			return
		}
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}
		h := w.Header()
		h.Set("Content-Type", "text/event-stream")
		h.Set("Cache-Control", "no-cache, no-transform")
		h.Set("Connection", "keep-alive")
		// Disable proxy buffering in environments that honour it
		// (nginx, etc); SSE relies on byte-by-byte forwarding.
		h.Set("X-Accel-Buffering", "no")
		w.WriteHeader(http.StatusOK)
		// Advise the client about the reconnect cadence.
		_, _ = w.Write([]byte("retry: " + strconv.Itoa(sseRetryMs) + "\n\n"))
		flusher.Flush()

		sub := ring.Subscribe(0)
		defer sub.Close()

		heartbeat := time.NewTicker(sseHeartbeatInterval)
		defer heartbeat.Stop()

		var lastDropped uint64
		for {
			select {
			case <-r.Context().Done():
				return
			case <-heartbeat.C:
				if _, err := w.Write([]byte(": heartbeat\n\n")); err != nil {
					return
				}
				flusher.Flush()
			case entry, ok := <-sub.C():
				if !ok {
					return
				}
				if dropped := sub.Dropped(); dropped > lastDropped {
					if err := writeSSEDrop(w, dropped-lastDropped); err != nil {
						return
					}
					lastDropped = dropped
				}
				if err := writeSSEEntry(w, entry); err != nil {
					return
				}
				flusher.Flush()
			}
		}
	})
}

// writeSSEEntry serialises one entry as an SSE `message` event.
func writeSSEEntry(w http.ResponseWriter, e livefeed.Entry) error {
	payload, err := json.Marshal(toMessageEntry(e))
	if err != nil {
		return err
	}
	if _, err := w.Write([]byte("event: message\ndata: ")); err != nil {
		return err
	}
	if _, err := w.Write(payload); err != nil {
		return err
	}
	_, err = w.Write([]byte("\n\n"))
	return err
}

// writeSSEDrop emits a `drop` event carrying the delta count. The SPA
// can surface this as "missed N entries — connection was slow" without
// any additional endpoint.
func writeSSEDrop(w http.ResponseWriter, delta uint64) error {
	frame := "event: drop\ndata: {\"count\":" + strconv.FormatUint(delta, 10) + "}\n\n"
	_, err := w.Write([]byte(frame))
	return err
}

// toMessageEntry projects an internal Entry onto the wire DTO. Kept as
// a single mapping point so future field additions touch one site.
func toMessageEntry(e livefeed.Entry) adminc.MessageEntry {
	out := adminc.MessageEntry{
		EventID:             e.EventID,
		At:                  e.At,
		CorrelationID:       e.CorrelationID,
		Provider:            e.Provider,
		Endpoint:            e.Endpoint,
		Model:               e.Model,
		Configuration:       e.Configuration,
		StatusCode:          e.StatusCode,
		DurationMs:          e.DurationMs,
		Streaming:           e.Streaming,
		UpstreamError:       e.UpstreamError,
		TokensIn:            e.TokensIn,
		TokensOut:           e.TokensOut,
		TokensCached:        e.TokensCached,
		TokensCacheCreation: e.TokensCacheCreation,
	}
	if len(e.Tags) > 0 {
		out.Tags = append(out.Tags, e.Tags...)
	}
	if len(e.RulesMatched) > 0 {
		out.RulesMatched = make([]adminc.RuleHit, 0, len(e.RulesMatched))
		for _, h := range e.RulesMatched {
			out.RulesMatched = append(out.RulesMatched, adminc.RuleHit{
				RuleName:       h.RuleName,
				ActionsApplied: append([]string(nil), h.ActionsApplied...),
				Terminated:     h.Terminated,
				ErrorMessage:   h.ErrorMessage,
			})
		}
	}
	return out
}
