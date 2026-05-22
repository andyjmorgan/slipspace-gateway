package resilience

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	contractsres "github.com/andyjmorgan/sluice-gateway/contracts/resilience"
	contractsrules "github.com/andyjmorgan/sluice-gateway/contracts/rules"
	"github.com/andyjmorgan/sluice-gateway/internal/middleware/rules"
	"github.com/andyjmorgan/sluice-gateway/internal/proxy"
)

// Internal-package tests for load_balance — needed so the
// randomIntN hook is reachable. Test helpers duplicate the
// (small) set used by the resilience_test external suite.

type lbAttemptOutcome struct {
	status int
	err    error
	body   string
}

type lbMockNext struct {
	outcomes []lbAttemptOutcome
	idx      int
	seen     []string
}

func (m *lbMockNext) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	state := rules.MutableStateFromContext(r.Context())
	if state != nil {
		m.seen = append(m.seen, state.Provider)
	}
	if m.idx >= len(m.outcomes) {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	out := m.outcomes[m.idx]
	m.idx++
	if out.status == 0 {
		if buf, ok := w.(*proxy.BufferingResponseWriter); ok {
			buf.SetTransportError(out.err)
		}
		return
	}
	w.WriteHeader(out.status)
	if out.body != "" {
		_, _ = w.Write([]byte(out.body))
	}
}

func lbStubLookup(policies map[string]*contractsres.ResilienceConfig) PolicyLookup {
	return func(name string) *contractsres.ResilienceConfig {
		return policies[name]
	}
}

func lbChangeTo(provider string) []contractsrules.Action {
	return []contractsrules.Action{&contractsrules.ChangeProviderAction{NewProvider: provider}}
}

// stubRandSeq returns a function that walks a fixed sequence of
// integers — letting tests pin which slot weightedSelect picks
// without seeding the global RNG.
func stubRandSeq(seq ...int) func(int) int {
	idx := 0
	return func(n int) int {
		if idx >= len(seq) {
			return 0
		}
		v := seq[idx] % n
		idx++
		return v
	}
}

func withStubRand(t *testing.T, seq ...int) {
	t.Helper()
	prev := randomIntN
	randomIntN = stubRandSeq(seq...)
	t.Cleanup(func() { randomIntN = prev })
}

func loadBalancePolicy(strict bool, targets ...contractsres.ResilienceTarget) *contractsres.ResilienceConfig {
	return &contractsres.ResilienceConfig{
		Name:               "lb",
		Mode:               contractsres.ModeLoadBalance,
		StrictWeights:      strict,
		FailureStatusCodes: []int{500, 502, 503, 504},
		Targets:            targets,
	}
}

func TestWeightedSelect_DistributesByWeight(t *testing.T) {
	// Reach around the test helper to run a 10k-sample statistical
	// check. Stash + restore the global hook explicitly.
	prev := randomIntN
	t.Cleanup(func() { randomIntN = prev })

	pool := []contractsres.ResilienceTarget{
		{Name: "primary", Weight: 90},
		{Name: "canary", Weight: 10},
	}

	counts := map[string]int{}
	const samples = 10000
	for i := 0; i < samples; i++ {
		idx := weightedSelect(pool)
		if idx < 0 {
			t.Fatalf("weightedSelect returned -1 on iteration %d", i)
		}
		counts[pool[idx].Name]++
	}

	// 90/10 over 10k samples — tolerate ±3% drift either way.
	primary := counts["primary"]
	canary := counts["canary"]
	if primary < 8700 || primary > 9300 {
		t.Errorf("primary count = %d; expected ~9000 (±3%%)", primary)
	}
	if canary < 700 || canary > 1300 {
		t.Errorf("canary count = %d; expected ~1000 (±3%%)", canary)
	}
}

func TestWeightedSelect_NoPositiveWeights_ReturnsMinusOne(t *testing.T) {
	t.Parallel()
	pool := []contractsres.ResilienceTarget{
		{Name: "a", Weight: 0},
		{Name: "b", Weight: 0},
	}
	if got := weightedSelect(pool); got != -1 {
		t.Errorf("weightedSelect = %d; want -1 for all-zero pool", got)
	}
}

func TestWeightedSelect_SingleTarget(t *testing.T) {
	t.Parallel()
	pool := []contractsres.ResilienceTarget{{Name: "only", Weight: 7}}
	if got := weightedSelect(pool); got != 0 {
		t.Errorf("weightedSelect = %d; want 0 (single target)", got)
	}
}

func TestWeightedSelect_SkipsZeroWeightEntries(t *testing.T) {
	prev := randomIntN
	t.Cleanup(func() { randomIntN = prev })
	randomIntN = func(int) int { return 0 } // always roll 0

	pool := []contractsres.ResilienceTarget{
		{Name: "muted-1", Weight: 0},
		{Name: "live", Weight: 5},
		{Name: "muted-2", Weight: 0},
	}
	if got := weightedSelect(pool); got != 1 {
		t.Errorf("weightedSelect = %d; want 1 (zero-weight slots are skipped)", got)
	}
}

func TestLoadBalance_StrictWeights_NoReRoll(t *testing.T) {
	withStubRand(t, 95) // roll 95 out of 100 → lands in canary slot (cumulative 90→primary, 100→canary)

	pol := loadBalancePolicy(true,
		contractsres.ResilienceTarget{Name: "primary", Provider: "openai", Weight: 90, Actions: lbChangeTo("openai")},
		contractsres.ResilienceTarget{Name: "canary", Provider: "anthropic", Weight: 10, Actions: lbChangeTo("anthropic")},
	)
	lookup := lbStubLookup(map[string]*contractsres.ResilienceConfig{"lb": pol})
	next := &lbMockNext{outcomes: []lbAttemptOutcome{{status: 503}}}
	h := HTTPHandler(lookup, next)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	ctx := rules.WithMutableState(req.Context(), &rules.MutableState{PolicyRef: "lb"})
	h.ServeHTTP(rec, req.WithContext(ctx))

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("client status = %d; want 503 (strict_weights blocks re-roll)", rec.Code)
	}
	if len(next.seen) != 1 {
		t.Errorf("attempts = %d; want 1 (no re-roll under strict_weights)", len(next.seen))
	}
}

func TestLoadBalance_LBWF_RerollsOnRetryableFailure(t *testing.T) {
	// First roll picks canary (95 out of 100). Canary returns 503.
	// Pool shrinks to [primary]. Second roll picks primary (only
	// option) which returns 200 → committed.
	withStubRand(t, 95, 0)

	pol := loadBalancePolicy(false,
		contractsres.ResilienceTarget{Name: "primary", Provider: "openai", Weight: 90, Actions: lbChangeTo("openai")},
		contractsres.ResilienceTarget{Name: "canary", Provider: "anthropic", Weight: 10, Actions: lbChangeTo("anthropic")},
	)
	lookup := lbStubLookup(map[string]*contractsres.ResilienceConfig{"lb": pol})
	next := &lbMockNext{outcomes: []lbAttemptOutcome{
		{status: 503, body: "down"},
		{status: 200, body: "ok"},
	}}
	h := HTTPHandler(lookup, next)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	ctx := rules.WithMutableState(req.Context(), &rules.MutableState{PolicyRef: "lb"})
	h.ServeHTTP(rec, req.WithContext(ctx))

	if rec.Code != http.StatusOK {
		t.Errorf("client status = %d; want 200 (re-roll absorbed canary failure)", rec.Code)
	}
	if len(next.seen) != 2 {
		t.Errorf("attempts = %d; want 2", len(next.seen))
	}
	if next.seen[0] != "anthropic" || next.seen[1] != "openai" {
		t.Errorf("attempt order = %v; want [anthropic openai]", next.seen)
	}
}

func TestLoadBalance_AllFail_SurfacesLastStatus(t *testing.T) {
	withStubRand(t, 0, 0)

	pol := loadBalancePolicy(false,
		contractsres.ResilienceTarget{Name: "a", Provider: "openai", Weight: 50, Actions: lbChangeTo("openai")},
		contractsres.ResilienceTarget{Name: "b", Provider: "anthropic", Weight: 50, Actions: lbChangeTo("anthropic")},
	)
	lookup := lbStubLookup(map[string]*contractsres.ResilienceConfig{"lb": pol})
	next := &lbMockNext{outcomes: []lbAttemptOutcome{
		{status: 503},
		{status: 502},
	}}
	h := HTTPHandler(lookup, next)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	ctx := rules.WithMutableState(req.Context(), &rules.MutableState{PolicyRef: "lb"})
	h.ServeHTTP(rec, req.WithContext(ctx))

	if rec.Code != http.StatusBadGateway {
		t.Errorf("client status = %d; want 502 (last attempt's status surfaces)", rec.Code)
	}
	if len(next.seen) != 2 {
		t.Errorf("attempts = %d; want 2", len(next.seen))
	}
}

func TestLoadBalance_TransportError_AllAttemptsFail_502(t *testing.T) {
	withStubRand(t, 0, 0)

	pol := loadBalancePolicy(false,
		contractsres.ResilienceTarget{Name: "a", Provider: "openai", Weight: 50, Actions: lbChangeTo("openai")},
		contractsres.ResilienceTarget{Name: "b", Provider: "anthropic", Weight: 50, Actions: lbChangeTo("anthropic")},
	)
	lookup := lbStubLookup(map[string]*contractsres.ResilienceConfig{"lb": pol})
	next := &lbMockNext{outcomes: []lbAttemptOutcome{
		{status: 0, err: errors.New("connection refused")},
		{status: 0, err: errors.New("connection refused")},
	}}
	h := HTTPHandler(lookup, next)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	ctx := rules.WithMutableState(req.Context(), &rules.MutableState{PolicyRef: "lb"})
	h.ServeHTTP(rec, req.WithContext(ctx))

	if rec.Code != http.StatusBadGateway {
		t.Errorf("client status = %d; want 502 (all transport errors)", rec.Code)
	}
}

func TestLoadBalance_ModeLoadBalanceWithFailover_DispatchesSame(t *testing.T) {
	withStubRand(t, 0, 0)
	pol := &contractsres.ResilienceConfig{
		Name:               "lbwf-legacy",
		Mode:               contractsres.ModeLoadBalanceWithFailover,
		FailureStatusCodes: []int{503},
		Targets: []contractsres.ResilienceTarget{
			{Name: "a", Provider: "openai", Weight: 50, Actions: lbChangeTo("openai")},
			{Name: "b", Provider: "anthropic", Weight: 50, Actions: lbChangeTo("anthropic")},
		},
	}
	lookup := lbStubLookup(map[string]*contractsres.ResilienceConfig{"lbwf-legacy": pol})
	next := &lbMockNext{outcomes: []lbAttemptOutcome{
		{status: 503},
		{status: 200, body: "ok"},
	}}
	h := HTTPHandler(lookup, next)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	ctx := rules.WithMutableState(req.Context(), &rules.MutableState{PolicyRef: "lbwf-legacy"})
	h.ServeHTTP(rec, req.WithContext(ctx))

	if rec.Code != http.StatusOK {
		t.Errorf("legacy ModeLoadBalanceWithFailover did not re-roll: status = %d", rec.Code)
	}
	if len(next.seen) != 2 {
		t.Errorf("attempts = %d; want 2", len(next.seen))
	}
}

func TestLoadBalance_NoPositiveWeights_Writes502(t *testing.T) {
	t.Parallel()
	pol := &contractsres.ResilienceConfig{
		Name: "broken",
		Mode: contractsres.ModeLoadBalance,
		Targets: []contractsres.ResilienceTarget{
			{Name: "a", Provider: "openai", Weight: 0, Actions: lbChangeTo("openai")},
			{Name: "b", Provider: "anthropic", Weight: 0, Actions: lbChangeTo("anthropic")},
		},
	}
	lookup := lbStubLookup(map[string]*contractsres.ResilienceConfig{"broken": pol})
	next := &lbMockNext{}
	h := HTTPHandler(lookup, next)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	ctx := rules.WithMutableState(req.Context(), &rules.MutableState{PolicyRef: "broken"})
	h.ServeHTTP(rec, req.WithContext(ctx))

	if rec.Code != http.StatusBadGateway {
		t.Errorf("client status = %d; want 502", rec.Code)
	}
	if len(next.seen) != 0 {
		t.Errorf("downstream invoked despite zero-weight pool: %v", next.seen)
	}
}

func TestLoadBalance_NonRetryable4xx_CommitsImmediately(t *testing.T) {
	withStubRand(t, 0)

	pol := loadBalancePolicy(false,
		contractsres.ResilienceTarget{Name: "a", Provider: "openai", Weight: 50, Actions: lbChangeTo("openai")},
		contractsres.ResilienceTarget{Name: "b", Provider: "anthropic", Weight: 50, Actions: lbChangeTo("anthropic")},
	)
	lookup := lbStubLookup(map[string]*contractsres.ResilienceConfig{"lb": pol})
	next := &lbMockNext{outcomes: []lbAttemptOutcome{{status: 403, body: "denied"}}}
	h := HTTPHandler(lookup, next)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	ctx := rules.WithMutableState(req.Context(), &rules.MutableState{PolicyRef: "lb"})
	h.ServeHTTP(rec, req.WithContext(ctx))

	if rec.Code != http.StatusForbidden {
		t.Errorf("client status = %d; want 403 (not retried)", rec.Code)
	}
	if len(next.seen) != 1 {
		t.Errorf("attempts = %d; want 1", len(next.seen))
	}
}

func TestLoadBalance_ApplyActionError_Returns500(t *testing.T) {
	withStubRand(t, 0)

	pol := loadBalancePolicy(false,
		contractsres.ResilienceTarget{
			Name:     "broken",
			Provider: "openai",
			Weight:   100,
			Actions:  []contractsrules.Action{&contractsrules.ChangeProviderAction{NewProvider: ""}},
		},
	)
	lookup := lbStubLookup(map[string]*contractsres.ResilienceConfig{"lb": pol})
	next := &lbMockNext{}
	h := HTTPHandler(lookup, next)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	ctx := rules.WithMutableState(req.Context(), &rules.MutableState{PolicyRef: "lb"})
	h.ServeHTTP(rec, req.WithContext(ctx))

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("client status = %d; want 500", rec.Code)
	}
}

func TestLoadBalance_RerollExhaustsPool(t *testing.T) {
	withStubRand(t, 0, 0, 0)

	pol := loadBalancePolicy(false,
		contractsres.ResilienceTarget{Name: "a", Provider: "a", Weight: 1, Actions: lbChangeTo("a")},
		contractsres.ResilienceTarget{Name: "b", Provider: "b", Weight: 1, Actions: lbChangeTo("b")},
		contractsres.ResilienceTarget{Name: "c", Provider: "c", Weight: 1, Actions: lbChangeTo("c")},
	)
	lookup := lbStubLookup(map[string]*contractsres.ResilienceConfig{"lb": pol})
	next := &lbMockNext{outcomes: []lbAttemptOutcome{
		{status: 503},
		{status: 503},
		{status: 503},
	}}
	h := HTTPHandler(lookup, next)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	ctx := rules.WithMutableState(req.Context(), &rules.MutableState{PolicyRef: "lb"})
	h.ServeHTTP(rec, req.WithContext(ctx))

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("client status = %d; want 503", rec.Code)
	}
	if len(next.seen) != 3 {
		t.Errorf("attempts = %d; want 3 (exhausted pool of 3)", len(next.seen))
	}
}
