package resilience

// Error codes the orchestrator writes through httperr when a request ends
// without any attempt committing a response (docs/pipeline.md, "Error
// responses"). They mirror the gateway.resilience.outcome.total label
// vocabulary so a dashboard can join the error counter and the outcome
// counter on one value.
const (
	// errCodeAllFailed is written when every dispatched attempt ended in a
	// retryable status or transport error and the target list is exhausted.
	errCodeAllFailed = orchestratorOutcomeAllFailed

	// errCodeAllOpen is written when no attempt was dispatched because every
	// target's circuit breaker was open.
	errCodeAllOpen = orchestratorOutcomeAllOpen
)

// exhaustedErrorCode maps the terminal orchestrator outcome to the stable
// httperr code the client sees. Unknown outcomes fall back to all_failed —
// the orchestrator only reaches the exhausted path with one of the two
// terminal outcomes, so the fallback is defensive.
func exhaustedErrorCode(outcome string) string {
	if outcome == orchestratorOutcomeAllOpen {
		return errCodeAllOpen
	}
	return errCodeAllFailed
}

// exhaustedErrorMessage is the human-readable detail paired with
// exhaustedErrorCode.
func exhaustedErrorMessage(outcome string) string {
	if outcome == orchestratorOutcomeAllOpen {
		return "no healthy upstream target: every circuit breaker is open"
	}
	return "all upstream targets failed"
}
