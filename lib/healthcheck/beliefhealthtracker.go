package healthcheck

import (
	"sync"
	"tcplb/lib/core"
)

type HealthBeliefState uint8

const (
	HEALTHY HealthBeliefState = iota
	UNHEALTHY
)

// Config holds configuration for a BeliefHealthTracker
type Config struct {
	// HealthBeliefState is the initial HealthBeliefState value to use
	// for the health of an upstream, before any observations are known.
	Prior HealthBeliefState

	// MinFailuresToInferUnhealthy is the minimum number of consecutive
	// CheckResult observations with the value CheckFail for the belief
	// state to transition to UNHEALTHY.
	MinFailuresToInferUnhealthy uint8

	// MinSuccessesToInferHealthy is the minimum number of consecutive
	// CheckResult observations with the value CheckSuccess for the belief
	// state to transition to UNHEALTHY.
	MinSuccessesToInferHealthy uint8
}

// BeliefHealthTracker maintains a belief state about the health of each
// upstream. All upstreams in scope for health tracking must be registered
// when the BeliefHealthTracker is created by NewBeliefHealthTracker.
type BeliefHealthTracker struct {
	beliefStateByUpstream map[core.Upstream]*upstreamBeliefState
}

func NewBeliefHealthTracker(upstreams core.UpstreamSet, cfg Config) *BeliefHealthTracker {
	beliefStateByUpstream := make(map[core.Upstream]*upstreamBeliefState)
	for u := range upstreams {
		beliefStateByUpstream[u] = &upstreamBeliefState{
			cfg:       cfg,
			state:     cfg.Prior,
			failures:  0,
			successes: 0,
		}
	}
	return &BeliefHealthTracker{
		beliefStateByUpstream: beliefStateByUpstream,
	}
}

// HealthyUpstreams returns a new UpstreamSet containing the subset of input
// candidate upstreams that are currently believed to be healthy.
//
// Any unknown Upstreams in the candidate set are ignored.
func (hc *BeliefHealthTracker) HealthyUpstreams(candidates core.UpstreamSet) core.UpstreamSet {
	var result = core.EmptyUpstreamSet()

	// TODO sweep requires acquiring many locks. Can we relax it?
	for u := range candidates {
		_, exists := hc.beliefStateByUpstream[u]
		if !exists {
			continue // Upstream was not previously registered, ignore.
		}
		beliefState := hc.beliefStateByUpstream[u]
		if beliefState.CurrentBelief() == HEALTHY {
			result[u] = struct{}{}
		}
	}
	return result
}

// ReportUpstreamHealth accepts a HealthReport.
//
// If the report is for unknown Upstream, it is ignored.
func (hc *BeliefHealthTracker) ReportUpstreamHealth(report *HealthReport) {
	if report == nil {
		return
	}
	beliefState, exists := hc.beliefStateByUpstream[report.Upstream]
	if !exists {
		return // Upstream was not previously registered, ignore.
	}
	beliefState.UpdateBelief(report)
}

// upstreamBeliefState encodes the current belief about the health
// of a single upstream. It must not be copied.
type upstreamBeliefState struct {
	// cfg is never modified after initialisation
	cfg Config

	// mu guards the below state variables
	mu        sync.Mutex // TODO consider replacing with sync RWmutex
	state     HealthBeliefState
	failures  uint8
	successes uint8
}

func min(a, b uint8) uint8 {
	if a < b {
		return a
	} else {
		return b
	}
}

func (s *upstreamBeliefState) UpdateBelief(report *HealthReport) {
	if report == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	s.updateBeliefLocked(report)
}

func (s *upstreamBeliefState) updateBeliefLocked(report *HealthReport) {
	switch report.CheckResult {
	case CheckSuccess:
		s.failures = 0
		s.successes = min(s.successes+1, s.cfg.MinSuccessesToInferHealthy)
		if s.successes >= s.cfg.MinSuccessesToInferHealthy {
			s.state = HEALTHY
		}
	case CheckFail:
		s.failures = min(s.failures+1, s.cfg.MinFailuresToInferUnhealthy)
		s.successes = 0
		if s.failures >= s.cfg.MinFailuresToInferUnhealthy {
			s.state = UNHEALTHY
		}
	}
}

func (s *upstreamBeliefState) CurrentBelief() HealthBeliefState {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.state
}

// type check *BeliefHealthTracker satisfies HealthReportSink interface
var _ HealthReportSink = (*BeliefHealthTracker)(nil)
