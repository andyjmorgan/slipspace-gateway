package connector

import (
	"errors"
	"fmt"
	"testing"
)

func TestRetryable_ErrorAndUnwrap(t *testing.T) {
	inner := errors.New("network unreachable")
	e := &Retryable{Err: inner}
	if got := e.Error(); got != "retryable: network unreachable" {
		t.Errorf("Error: got %q", got)
	}
	if !errors.Is(e, inner) {
		t.Error("errors.Is should reach the wrapped error")
	}
}

func TestRetryable_NilSafe(t *testing.T) {
	var e *Retryable
	if got := e.Error(); got != "retryable: <nil>" {
		t.Errorf("nil Retryable.Error: got %q", got)
	}
	if e.Unwrap() != nil {
		t.Error("nil Retryable.Unwrap should be nil")
	}
	z := &Retryable{}
	if got := z.Error(); got != "retryable: <nil>" {
		t.Errorf("zero Retryable.Error: got %q", got)
	}
}

func TestPermanent_ErrorAndUnwrap(t *testing.T) {
	inner := errors.New("403 forbidden")
	e := &Permanent{Err: inner}
	if got := e.Error(); got != "permanent: 403 forbidden" {
		t.Errorf("Error: got %q", got)
	}
	if !errors.Is(e, inner) {
		t.Error("errors.Is should reach the wrapped error")
	}
}

func TestPermanent_NilSafe(t *testing.T) {
	var e *Permanent
	if got := e.Error(); got != "permanent: <nil>" {
		t.Errorf("nil Permanent.Error: got %q", got)
	}
	if e.Unwrap() != nil {
		t.Error("nil Permanent.Unwrap should be nil")
	}
	z := &Permanent{}
	if got := z.Error(); got != "permanent: <nil>" {
		t.Errorf("zero Permanent.Error: got %q", got)
	}
}

func TestIsRetryable(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"plain error", errors.New("nope"), false},
		{"retryable", &Retryable{Err: errors.New("yes")}, true},
		{"wrapped retryable", fmt.Errorf("ctx: %w", &Retryable{Err: errors.New("deep")}), true},
		{"permanent", &Permanent{Err: errors.New("4xx")}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsRetryable(tc.err); got != tc.want {
				t.Errorf("IsRetryable(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestIsPermanent(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"plain error", errors.New("nope"), false},
		{"permanent", &Permanent{Err: errors.New("yes")}, true},
		{"wrapped permanent", fmt.Errorf("ctx: %w", &Permanent{Err: errors.New("deep")}), true},
		{"retryable", &Retryable{Err: errors.New("5xx")}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsPermanent(tc.err); got != tc.want {
				t.Errorf("IsPermanent(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
