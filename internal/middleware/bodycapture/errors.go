package bodycapture

import "errors"

// ErrBodyTooLarge is returned when the request body exceeds MaxBodyBytes.
var ErrBodyTooLarge = errors.New("bodycapture: request body too large")

// ErrParse wraps a JSON decoding failure on a typed RequestKind.
var ErrParse = errors.New("bodycapture: malformed request body")

// ErrUnknownKind is returned when Capture receives a RequestKind it cannot
// dispatch. This is a wiring bug — the route table fed an unknown kind.
var ErrUnknownKind = errors.New("bodycapture: unknown request kind")

// ErrMissingRoute is returned by the HTTP handler when the upstream
// kindFrom function cannot find a RequestKind on the request context.
// Like ErrUnknownKind, this is a wiring bug, not a client problem.
var ErrMissingRoute = errors.New("bodycapture: no request kind on context")
