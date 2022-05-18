package dialer

import (
	"tcplb/lib/core"
)

// PlaceholderDialPolicy is an example of a simple but not very useful DialPolicy.
// It arbitrarily chooses an upstream to dial in an implementation defined way.
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

// TODO add least-connection dial policy
