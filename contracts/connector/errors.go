package connector

import "errors"

// Retryable wraps an error indicating Upload should be retried after
// backoff. Use for transient I/O failures, 5xx responses, 429s with or
// without Retry-After, and unexpected network errors.
type Retryable struct {
	Err error
}

func (e *Retryable) Error() string {
	if e == nil || e.Err == nil {
		return "retryable: <nil>"
	}
	return "retryable: " + e.Err.Error()
}

func (e *Retryable) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// Permanent wraps an error indicating Upload should NOT be retried — the
// segment moves straight to the deadletter directory. Use for 4xx other
// than 429, auth/signature failures, and malformed-payload-from-our-side
// bugs.
type Permanent struct {
	Err error
}

func (e *Permanent) Error() string {
	if e == nil || e.Err == nil {
		return "permanent: <nil>"
	}
	return "permanent: " + e.Err.Error()
}

func (e *Permanent) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// IsRetryable reports whether err (or anything in its Unwrap chain) is a
// *Retryable. Returns false for nil.
func IsRetryable(err error) bool {
	if err == nil {
		return false
	}
	var r *Retryable
	return errors.As(err, &r)
}

// IsPermanent reports whether err (or anything in its Unwrap chain) is a
// *Permanent. Returns false for nil.
func IsPermanent(err error) bool {
	if err == nil {
		return false
	}
	var p *Permanent
	return errors.As(err, &p)
}
