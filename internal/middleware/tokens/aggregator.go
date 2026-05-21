package tokens

// Aggregator collects per-emission Usage observations under a chosen
// Strategy and exposes the running Snapshot. Not safe for concurrent
// use — extractors walk a response serially and the reporter is the
// only writer.
type Aggregator struct {
	snap Snapshot
}

// Handle folds u into the aggregator under strategy s. A zero-valued
// Usage is a no-op so extractors can call Handle defensively without
// pre-checking each field.
func (a *Aggregator) Handle(s Strategy, u Usage) {
	if u.IsZero() {
		return
	}
	switch s {
	case StrategyLastWins:
		a.snap.Input = u.Input
		a.snap.Output = u.Output
		a.snap.Cached = u.Cached
		a.snap.CacheCreation = u.CacheCreation
	case StrategyAddition:
		a.snap.Input += u.Input
		a.snap.Output += u.Output
		a.snap.Cached += u.Cached
		a.snap.CacheCreation += u.CacheCreation
	case StrategyIncremental:
		// First emission sets everything; subsequent emissions only
		// move Output. Mirrors airia-platform's historical contract:
		// vendors that quote input once at the start of a stream and
		// emit growing output thereafter must not have a second input
		// emission overwrite the first.
		if a.snap.Input == 0 && a.snap.Cached == 0 && a.snap.CacheCreation == 0 {
			a.snap.Input = u.Input
			a.snap.Cached = u.Cached
			a.snap.CacheCreation = u.CacheCreation
		}
		a.snap.Output = u.Output
	}
	a.snap.Recognised = true
}

// Snapshot returns the running aggregated view. Cheap — no copy beyond
// the four-int struct.
func (a *Aggregator) Snapshot() Snapshot { return a.snap }
