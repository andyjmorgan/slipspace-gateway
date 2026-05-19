package observability

import "context"

// RequestLabels carries the (provider, endpoint, model, configuration)
// values resolved at destination-finalisation time. These are the canonical
// labels the per-request reporter stamps on metrics and on the
// gateway.request event so dashboards and audit feeds see a uniform
// stream regardless of whether the request was forwarded or short-
// circuited by a terminating rule.
//
// Writers: the rules middleware sets these on the synthetic path
// (before driving the reporter lifecycle); the cmd/gateway final
// handler sets them on the forwarded path (just before forwarder
// invocation, after rules have resolved their post-rule state).
//
// Readers: the per-request observer factory pulls them from context
// at factory-invocation time; no other code should depend on these.
type RequestLabels struct {
	Provider string

	Endpoint string

	Model string

	// Configuration is the name of the resolved configuration the
	// request resolved against. Bounded cardinality — operators
	// define a handful of named configurations (production,
	// internal-dev, etc.); never derived from client input.
	Configuration string
}

type requestLabelsKey struct{}

// WithRequestLabels stamps the canonical per-request labels onto
// ctx. The cmd/gateway final handler and the rules.HTTPHandler
// synthetic path both call this; the reporter reads via
// RequestLabelsFromContext.
func WithRequestLabels(ctx context.Context, l RequestLabels) context.Context {
	return context.WithValue(ctx, requestLabelsKey{}, l)
}

// RequestLabelsFromContext returns the labels written by
// WithRequestLabels, or the zero value when none were set.
func RequestLabelsFromContext(ctx context.Context) RequestLabels {
	if ctx == nil {
		return RequestLabels{}
	}
	l, _ := ctx.Value(requestLabelsKey{}).(RequestLabels)
	return l
}
