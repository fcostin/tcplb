package dialer

import (
	"github.com/stretchr/testify/require"
	"tcplb/lib/core"
	"testing"
)

func TestLeastConnectionDialPolicy_Err_When_NoCandidates(t *testing.T) {
	policy := NewLeastConnectionDialPolicy()
	_, err := policy.ChooseBestUpstream(core.EmptyUpstreamSet())
	require.ErrorIs(t, err, NoCandidateUpstreams)
}

func TestLeastConnectionDialPolicy_ChoosesDifferentUpstreamAfterFirstChoiceSucceeds(t *testing.T) {
	// Basic scenario that policy might be able to balance load.
	a := core.Upstream{Network: "test-policies", Address: "a"}
	b := core.Upstream{Network: "test-policies", Address: "b"}
	candidates := core.NewUpstreamSet(a, b)
	policy := NewLeastConnectionDialPolicy()

	choice1, err := policy.ChooseBestUpstream(candidates)
	require.NoError(t, err)
	policy.DialSucceeded(choice1)
	choice2, err := policy.ChooseBestUpstream(candidates)
	require.NoError(t, err)
	require.NotEqual(t, choice1, choice2)
}

func TestLeastConnectionDialPolicy_Catchup(t *testing.T) {
	// Scenario where we open multiple connections to the first
	// upstream chosen by the policy, to check that it focuses on
	// choosing the other upstream.
	a := core.Upstream{Network: "test-policies", Address: "a"}
	b := core.Upstream{Network: "test-policies", Address: "b"}
	candidates := core.NewUpstreamSet(a, b)
	policy := NewLeastConnectionDialPolicy()

	choice1, err := policy.ChooseBestUpstream(candidates)
	require.NoError(t, err)

	n := 5
	for i := 0; i < n; i++ {
		policy.DialSucceeded(choice1)
	}

	for i := 0; i < n; i++ {
		choice2, err := policy.ChooseBestUpstream(candidates)
		require.NoError(t, err)
		require.NotEqual(t, choice1, choice2)
		policy.DialSucceeded(choice2)
	}

	for i := 0; i < n; i++ {
		policy.ConnectionClosed(choice1)
	}

	for i := 0; i < n; i++ {
		choice3, err := policy.ChooseBestUpstream(candidates)
		require.NoError(t, err)
		require.Equal(t, choice1, choice3)
	}
}
