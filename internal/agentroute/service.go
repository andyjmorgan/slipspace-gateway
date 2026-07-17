package agentroute

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/andyjmorgan/slipspace-gateway/contracts/advise"
	contractsconfig "github.com/andyjmorgan/slipspace-gateway/contracts/config"
	"github.com/andyjmorgan/slipspace-gateway/internal/observability"
	"github.com/andyjmorgan/slipspace-gateway/internal/safego"
	"github.com/andyjmorgan/slipspace-gateway/protocols/anthropic/messages"
)

// maxPrefixBytes bounds the prompt excerpts sent to the advisor — the
// classifier reads a prefix, never the body.
const maxPrefixBytes = 4096

// maxInflight bounds concurrent advisory calls per gateway process. On
// saturation new classifications are abandoned (Fail → a later request
// retries) rather than queued — the channel drops instead of building
// backpressure, mirroring the spool posture.
const maxInflight = 8

// Service evaluates agent-aware routing for one request and owns the async
// advisory lifecycle. Built once at startup from the advisors block; the
// per-configuration agent_routing policy is read from each request's config
// snapshot, so policy edits apply live while advisor endpoints (like the
// admin block) require a restart.
type Service struct {
	register *Register
	clients  map[string]*Client
	logger   *slog.Logger
	sem      chan struct{}
}

// NewService builds the routing service. secrets maps advisor name to the
// HMAC key loaded from its hmac_secret_file; an advisor without a loaded
// secret is skipped (startup logs the misconfiguration).
func NewService(advisors contractsconfig.AdvisorsConfig, secrets map[string][]byte, logger *slog.Logger) *Service {
	clients := make(map[string]*Client, len(advisors))
	for name, a := range advisors {
		secret, ok := secrets[name]
		if !ok {
			logger.Warn("agentroute: advisor has no loaded secret, skipping", "advisor", name)
			continue
		}
		clients[name] = NewClient(a.Endpoint, a.GatewayID, secret, a.Timeout())
	}
	return &Service{
		register: NewRegister(),
		clients:  clients,
		logger:   logger,
		sem:      make(chan struct{}, maxInflight),
	}
}

// Evaluate runs the agent-aware routing decision for one generative request
// and returns the model to pin the request to ("" when none). It never
// blocks: a hit returns the stored pin; a first sighting fires the advisory
// call on a bounded background goroutine and returns "" so the request
// proceeds as configured.
//
// v1 trigger scope: Claude Code subagent conversations on the messages
// protocol whose typed body declares tools (the birth-capture gate — sidecar
// utility calls carry no tools and never open a register entry).
func (s *Service) Evaluate(
	ctx context.Context,
	cfgName string,
	ar *contractsconfig.AgentRouting,
	protocol, provider, model string,
	req *messages.MessagesRequest,
	headers http.Header,
) *Pin {
	if s == nil || ar == nil || req == nil {
		return nil
	}
	client, ok := s.clients[ar.Advisor]
	if !ok {
		return nil
	}
	if protocol != contractsconfig.ProtocolMessages {
		return nil
	}

	id := Identify(headers)
	if !id.IsSubagent {
		return nil
	}
	if len(req.Tools) == 0 {
		return nil // sidecar / utility one-shot: never classify
	}

	key := observability.ConversationIDFromContext(ctx)
	if key == "" {
		return nil
	}

	pin, fire := s.register.Observe(key, ar.PinTTL())
	if pin != nil {
		return pin
	}
	if !fire {
		return nil
	}

	areq := advise.Request{
		Configuration:    cfgName,
		Protocol:         protocol,
		Provider:         provider,
		Model:            model,
		ConversationID:   key,
		SessionID:        observability.SessionIDFromContext(ctx),
		AgentFamily:      id.Family,
		Entrypoint:       id.Entrypoint,
		IsSubagent:       id.IsSubagent,
		SystemPrefix:     systemPrefix(req),
		FirstUserMessage: firstUserMessage(req),
		ToolNames:        toolNames(req),
	}
	s.dispatch(ctx, client, ar, key, areq)
	return nil
}

// dispatch launches the advisory call on a bounded background goroutine,
// detached from the request context (the request finishes long before the
// verdict lands). On saturation the attempt is abandoned so a later request
// retries — never queued, never blocking.
func (s *Service) dispatch(ctx context.Context, client *Client, ar *contractsconfig.AgentRouting, key string, areq advise.Request) {
	window := ar.ApplyWindow()
	allowed := append([]string(nil), ar.AllowModels...)

	select {
	case s.sem <- struct{}{}:
	default:
		s.register.Fail(key)
		return
	}

	bg := context.WithoutCancel(ctx)
	safego.Go(bg, "agentroute.advise", s.logger, nil, func() {
		defer func() { <-s.sem }()

		verdict, err := client.Advise(bg, areq)
		if err != nil {
			s.logger.Warn("agentroute: advise failed (failing open)",
				"conversation_id", key, "error", err.Error())
			s.register.Fail(key)
			return
		}

		var pin *Pin
		switch {
		case !verdict.Switch:
			// Continue as configured — but say so: a silently-declined
			// verdict is indistinguishable from a lost advisory in the
			// gateway logs (the arbiter's advise_audit table records the
			// full judgement either way).
			s.logger.Info("agentroute: verdict declined switch, continuing as configured",
				"conversation_id", key, "reason", verdict.Reason)
		case !modelIn(allowed, verdict.Model):
			s.logger.Warn("agentroute: verdict model not in allow_models, discarding",
				"conversation_id", key, "model", verdict.Model)
		default:
			pin = &Pin{Model: verdict.Model, Reason: verdict.Reason}
		}
		s.register.Complete(key, pin, window)
		if pin != nil {
			s.logger.Info("agentroute: conversation pinned",
				"conversation_id", key, "model", pin.Model, "reason", pin.Reason)
		}
	})
}

// modelIn reports whether model is in the captured allow-list.
func modelIn(allowed []string, model string) bool {
	for _, m := range allowed {
		if m == model {
			return true
		}
	}
	return false
}

// systemPrefix extracts the leading bytes of the system prompt, whichever
// wire shape it was sent in.
func systemPrefix(req *messages.MessagesRequest) string {
	if s, ok := req.SystemAsString(); ok {
		return truncate(s)
	}
	if blocks, ok := req.SystemAsBlocks(); ok {
		var out string
		for _, b := range blocks {
			if len(out) >= maxPrefixBytes {
				break
			}
			if out != "" {
				out += "\n"
			}
			out += b.Text
		}
		return truncate(out)
	}
	return ""
}

// firstUserMessage extracts the leading bytes of the first user turn — for a
// subagent, the task description.
func firstUserMessage(req *messages.MessagesRequest) string {
	for _, m := range req.Messages {
		if m.Role != "user" {
			continue
		}
		if s, ok := m.ContentAsString(); ok {
			return truncate(s)
		}
		if blocks, ok := m.ContentAsBlocks(); ok {
			for _, b := range blocks {
				if tb, isText := b.(*messages.TextBlock); isText {
					return truncate(tb.Text)
				}
			}
		}
		return ""
	}
	return ""
}

// toolNames lists the declared tool names.
func toolNames(req *messages.MessagesRequest) []string {
	if len(req.Tools) == 0 {
		return nil
	}
	names := make([]string, 0, len(req.Tools))
	for _, t := range req.Tools {
		names = append(names, t.Name)
	}
	return names
}

// truncate bounds s to maxPrefixBytes.
func truncate(s string) string {
	if len(s) <= maxPrefixBytes {
		return s
	}
	return s[:maxPrefixBytes]
}
