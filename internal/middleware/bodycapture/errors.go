package bodycapture

import "errors"

// ErrBodyTooLarge is returned when the request body exceeds MaxBodyBytes.
var ErrBodyTooLarge = errors.New("bodycapture: request body too large")

// ErrParse wraps a JSON decoding failure on a typed RequestKind.
var ErrParse = errors.New("bodycapture: malformed request body")

// ErrUnknownKind is returned when Capture receives a RequestKind it cannot
// dispatch. This is a wiring bug — the injected KindFromContextFunc fed an
// unknown kind.
var ErrUnknownKind = errors.New("bodycapture: unknown request kind")

// ErrMissingRoute is a retained v1 sentinel and is no longer returned. The
// HTTP handler's missing-kind path logs result=no_kind and writes a bare 500
// rather than surfacing this error; it is kept only for API compatibility
// with external errors.Is checks.
var ErrMissingRoute = errors.New("bodycapture: no request kind on context")
