package observability

import (
	"context"
	"net/http"
	"strings"
)

// User-id resolution and context plumbing. A user id identifies the end user
// on whose behalf a request is made — orthogonal to the session/conversation
// that groups a turn's requests and to the agent that issues them. Resolution
// mirrors session and agent id: SlipSpace-first, then a configured fallback chain
// walked top-down. See the design note "Correlating Requests Across Turns
// (Session Bundling)" → User id.

// SlipSpaceUserHeader is the authoritative user header. When a client or proxy
// deliberately sets it, it wins over any ambient client header, so it is
// always tried before the fallback chain.
const SlipSpaceUserHeader = "X-Slipspace-User-Id"

// DefaultUserIDHeaders is the shipped fallback chain, walked in order after the
// authoritative SlipSpace header. It is empty: unlike session and agent id, no
// client emits a standard end-user header today, so there is nothing to ship.
// Operators promote a custom client's user header via SLIPSPACE_USER_ID_HEADERS.
var DefaultUserIDHeaders = []string{}

// UserResolver resolves a user id from inbound request headers. The SlipSpace
// header is always attempted first; operator-configured fallbacks follow the
// shipped defaults, in the order supplied.
type UserResolver struct {
	// fallbacks is the ordered fallback chain (shipped defaults plus
	// operator extras). The SlipSpace header is implicit and always first.
	fallbacks []string
}

// NewUserResolver builds a resolver whose fallback chain is the shipped
// DefaultUserIDHeaders (empty) followed by extra — operator-supplied custom
// headers, kept in the order given. The SlipSpace header is authoritative and
// need not appear in extra. Blank entries are dropped.
func NewUserResolver(extra []string) *UserResolver {
	fb := make([]string, 0, len(DefaultUserIDHeaders)+len(extra))
	fb = append(fb, DefaultUserIDHeaders...)
	for _, e := range extra {
		if e = strings.TrimSpace(e); e != "" {
			fb = append(fb, e)
		}
	}
	return &UserResolver{fallbacks: fb}
}

// Resolve returns the user id and its provenance (the header name it came
// from, which the console uses to label the value). The SlipSpace header wins;
// otherwise the fallback chain is walked top-down and the first present,
// non-empty header wins.
//
// A candidate for which sensitive reports true is treated as absent and
// resolution falls through to the next — so a promoted user id can never
// resurface a value the operator added to the redaction set. Returns ("", "")
// when nothing matches. Shares pickHeader with the session resolver.
func (u *UserResolver) Resolve(h http.Header, sensitive func(string) bool) (id, source string) {
	if h == nil {
		return "", ""
	}
	if id, source = pickHeader(h, SlipSpaceUserHeader, sensitive); id != "" {
		return id, source
	}
	for _, name := range u.fallbacks {
		if id, source = pickHeader(h, name, sensitive); id != "" {
			return id, source
		}
	}
	return "", ""
}

type userIDKey struct{}

type userSourceKey struct{}

// WithUserID stores the resolved user id and its source header on ctx. An
// empty id leaves ctx unchanged so callers can pipe a resolution result
// through unconditionally.
func WithUserID(ctx context.Context, id, source string) context.Context {
	if id == "" {
		return ctx
	}
	ctx = context.WithValue(ctx, userIDKey{}, id)
	if source != "" {
		ctx = context.WithValue(ctx, userSourceKey{}, source)
	}
	return ctx
}

// UserIDFromContext returns the resolved user id on ctx, or "".
func UserIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if id, ok := ctx.Value(userIDKey{}).(string); ok {
		return id
	}
	return ""
}

// UserIDSourceFromContext returns the header name the user id was resolved
// from, or "".
func UserIDSourceFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if s, ok := ctx.Value(userSourceKey{}).(string); ok {
		return s
	}
	return ""
}
