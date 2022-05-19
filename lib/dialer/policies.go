package dialer

import (
	"math"
	"sync"
	"tcplb/lib/core"
)

// PlaceholderDialPolicy is an example of a simple but not very useful DialPolicy.
// It arbitrarily chooses an upstream to dial in an implementation defined way.
//
// Multiple goroutines may invoke methods on an PlaceholderDialPolicy simultaneously.
type PlaceholderDialPolicy struct{}

func (p PlaceholderDialPolicy) ChooseBestUpstream(candidates core.UpstreamSet) (core.Upstream, error) {
	for upstream := range candidates {
		return upstream, nil
	}
	return core.Upstream{}, NoCandidateUpstreams
}

func (p PlaceholderDialPolicy) DialFailed(upstream core.Upstream, symptom error) {}

func (p PlaceholderDialPolicy) DialSucceeded(upstream core.Upstream) {}

func (p PlaceholderDialPolicy) ConnectionClosed(upstream core.Upstream) {}

// LeastConnectionDialPolicy is a DialPolicy that always chooses an upstream
// that has the minimal number of connections among the candidate upstreams.
//
// Multiple goroutines may invoke methods on a LeastConnectionDialPolicy simultaneously.
type LeastConnectionDialPolicy struct {
	// TODO could use fine-grain locks, one per upstream. That could reduce lock contention
	// in situations where different clients make concurrent connection attempts with
	// disjoint sets of candidates. But in case where concurrent connection attempts have
	// overlapping or identical sets of candidate upstreams, it isn't clear (without
	//running experiments) how much that could help.
	mu              sync.Mutex
	connectionCount map[core.Upstream]int64
}

// NewLeastConnectionDialPolicy returns a new LeastConnectionDialPolicy
func NewLeastConnectionDialPolicy() *LeastConnectionDialPolicy {
	return &LeastConnectionDialPolicy{
		connectionCount: make(map[core.Upstream]int64),
	}
}

func (p *LeastConnectionDialPolicy) ChooseBestUpstream(candidates core.UpstreamSet) (core.Upstream, error) {
	var minCount int64 = math.MaxInt64
	argMin := core.Upstream{}

	p.mu.Lock()
	defer p.mu.Unlock()

	// Doing a linear scan over all candidate upstreams does not seem ideal, but it'd
	// be surprising if we have more than 1000 upstreams. Even if we had 10,000 or more,
	// the time to do the scan is insignificant compared to a roundtrip over network.
	for upstream := range candidates {
		count := p.connectionCount[upstream]
		if count < minCount {
			minCount = count
			argMin = upstream
		}
	}

	var err error
	if minCount == math.MaxInt64 {
		err = NoCandidateUpstreams
	}

	return argMin, err
}

func (p *LeastConnectionDialPolicy) DialFailed(upstream core.Upstream, symptom error) {
	// A failed connection attempt does not change the connection count.
}

func (p *LeastConnectionDialPolicy) DialSucceeded(upstream core.Upstream) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.connectionCount[upstream]++
}

func (p *LeastConnectionDialPolicy) ConnectionClosed(upstream core.Upstream) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.connectionCount[upstream]--
}
