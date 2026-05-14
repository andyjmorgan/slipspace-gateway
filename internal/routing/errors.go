package routing

import "errors"

// ErrNoRoute is returned when an inbound (method, path) does not match any
// route claimed by the resolved provider config.
var ErrNoRoute = errors.New("routing: no matching route")

// ErrMethodNotAllowed is returned when an inbound path matches a known route
// but the request method is not in the endpoint's allowed method list.
var ErrMethodNotAllowed = errors.New("routing: method not allowed")
