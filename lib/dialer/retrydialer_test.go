package dialer

import (
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"io"
	"net"
	"tcplb/lib/core"
	"tcplb/lib/forwarder"
	"tcplb/lib/slog"
	"testing"
	"time"
)

// various mock & fake objects to test against:

type connErrPair struct {
	Conn forwarder.DuplexConn
	Err  error
}

// fakeDialer resolves dials with a lookup table.
type fakeDialer struct {
	DialDelay        time.Duration
	ResultByUpstream map[core.Upstream]connErrPair
}

func (d *fakeDialer) DialUpstream(ctx context.Context, upstream core.Upstream) (forwarder.DuplexConn, error) {
	result, ok := d.ResultByUpstream[upstream]
	if !ok {
		return nil, errors.New("unknown upstream")
	}
	if d.DialDelay > 0 {
		timer := time.NewTimer(d.DialDelay)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	return result.Conn, result.Err
}

// blackholeConn is a DuplexConn from which bytes cannot escape.
type blackholeConn struct{}

func (c *blackholeConn) Read(b []byte) (n int, err error) {
	return 0, io.EOF
}

func (c *blackholeConn) Write(b []byte) (n int, err error) {
	return len(b), nil
}

func (c *blackholeConn) Close() error {
	return nil
}

func (c *blackholeConn) CloseWrite() error {
	return nil
}

func (c *blackholeConn) LocalAddr() net.Addr {
	return nil // FIXME
}

func (c *blackholeConn) RemoteAddr() net.Addr {
	return nil // FIXME
}

func (c *blackholeConn) SetDeadline(t time.Time) error {
	return nil
}

func (c *blackholeConn) SetReadDeadline(t time.Time) error {
	return nil
}

func (c *blackholeConn) SetWriteDeadline(t time.Time) error {
	return nil
}

var _ forwarder.DuplexConn = (*blackholeConn)(nil)

type UpstreamErrPair struct {
	Upstream core.Upstream
	Error    error
}

// MockDialPolicy returns upstreams prepared earlier.
type MockDialPolicy struct {
	I       int
	Results []UpstreamErrPair
	Events  []string
}

func (p *MockDialPolicy) ChooseBestUpstream(candidates core.UpstreamSet) (core.Upstream, error) {
	p.Events = append(p.Events, "ChooseBestUpstream")
	result := p.Results[p.I%len(p.Results)]
	p.I++
	return result.Upstream, result.Error
}

func (p *MockDialPolicy) DialFailed(upstream core.Upstream, symptom error) {
	p.Events = append(p.Events, "DialFailed")
}

func (p *MockDialPolicy) DialSucceeded(upstream core.Upstream) {
	p.Events = append(p.Events, "DialSucceeded")
}

func (p *MockDialPolicy) ConnectionClosed(upstream core.Upstream) {
	p.Events = append(p.Events, "ConnectionClosed")
}

// RetryDialer test scenarios

func TestRetryDialer_DialBestUpstream_Err_When_NoCandidates(t *testing.T) {
	// When DialBestUpstream is called with empty set of candidates
	// it should immediately return NoCandidateUpstreams error.
	rd := &RetryDialer{}

	ctx := context.Background()
	candidates := core.EmptyUpstreamSet()
	_, conn, err := rd.DialBestUpstream(ctx, candidates)
	require.ErrorIs(t, err, NoCandidateUpstreams)
	require.Nil(t, conn)
}

func TestRetryDialer_DialBestUpstream_Err_When_ChooseErr(t *testing.T) {
	// When DialBestUpstream is called with nonempty set of candidates
	// if the first ChooseBestUpstream call returns an error
	// it should immediately return that error.
	upstream := core.Upstream{Network: "test-retrydialer", Address: "a"}
	candidates := core.NewUpstreamSet(upstream)

	chooseErr := errors.New("indecision")
	policy := &MockDialPolicy{
		Results: []UpstreamErrPair{
			{Upstream: core.Upstream{}, Error: chooseErr},
		},
		Events: make([]string, 0),
	}
	rd := &RetryDialer{
		Policy: policy,
		Logger: slog.VoidLogger{},
	}

	ctx := context.Background()
	_, conn, err := rd.DialBestUpstream(ctx, candidates)
	require.ErrorIs(t, err, chooseErr)
	require.Nil(t, conn)
}

func TestRetryDialer_DialBestUpstream_Success_Close(t *testing.T) {
	// When DialBestUpstream is called with nonempty set of candidates
	// if the first ChooseBestUpstream returns some candidate upstream
	// it should call the inner dialer, and if that succeeds, it should
	// return a conn. Calling Close on the conn should result in a call
	// to ConnectionClosed on the policy.
	upstream := core.Upstream{Network: "test-retrydialer", Address: "a"}
	candidates := core.NewUpstreamSet(upstream)

	innerConn := &blackholeConn{}
	policy := &MockDialPolicy{
		Results: []UpstreamErrPair{
			{Upstream: upstream, Error: nil},
		},
		Events: make([]string, 0),
	}
	rd := &RetryDialer{
		Policy:  policy,
		Timeout: time.Second,
		InnerDialer: &fakeDialer{
			ResultByUpstream: map[core.Upstream]connErrPair{
				upstream: {
					innerConn,
					nil,
				},
			},
		},
		Logger: slog.VoidLogger{},
	}

	ctx := context.Background()

	_, conn, err := rd.DialBestUpstream(ctx, candidates)
	require.NoError(t, err)

	expectedEvents := []string{
		"ChooseBestUpstream",
		"DialSucceeded",
	}
	require.Equal(t, expectedEvents, policy.Events)

	err = conn.Close()
	require.NoError(t, err)

	expectedEvents = []string{
		"ChooseBestUpstream",
		"DialSucceeded",
		"ConnectionClosed",
	}
	require.Equal(t, expectedEvents, policy.Events)
}

func TestRetryDialer_DialBestUpstream_Failure_Retry_Success_Close(t *testing.T) {
	// Scenario where first Dial attempt fails, then retry with a different
	// upstream, which succeeds
	unhealthy := core.Upstream{Network: "test-retrydialer", Address: "unhealthy"}
	healthy := core.Upstream{Network: "test-retrydialer", Address: "heathy"}
	candidates := core.NewUpstreamSet(unhealthy, healthy)

	innerConn := &blackholeConn{}
	policy := &MockDialPolicy{
		Results: []UpstreamErrPair{
			{Upstream: unhealthy, Error: nil},
			{Upstream: healthy, Error: nil},
		},
		Events: make([]string, 0),
	}
	rd := &RetryDialer{
		Policy:  policy,
		Timeout: time.Second,
		InnerDialer: &fakeDialer{
			ResultByUpstream: map[core.Upstream]connErrPair{
				unhealthy: {
					nil,
					errors.New("unhealthy upstream is resting in bed"),
				},
				healthy: {
					innerConn,
					nil,
				},
			},
		},
		Logger: slog.VoidLogger{},
	}

	ctx := context.Background()

	_, conn, err := rd.DialBestUpstream(ctx, candidates)
	require.NoError(t, err)

	expectedEvents := []string{
		"ChooseBestUpstream",
		"DialFailed",
		"ChooseBestUpstream",
		"DialSucceeded",
	}
	require.Equal(t, expectedEvents, policy.Events)

	err = conn.Close()
	require.NoError(t, err)

	expectedEvents = []string{
		"ChooseBestUpstream",
		"DialFailed",
		"ChooseBestUpstream",
		"DialSucceeded",
		"ConnectionClosed",
	}
	require.Equal(t, expectedEvents, policy.Events)
}

func TestRetryDialer_DialBestUpstream_Dial_Timeout(t *testing.T) {
	// Scenario where RetryDialer returns error after first Dial attempt times out.

	uncommunicative := core.Upstream{Network: "test-retrydialer", Address: "uncommunicative"}
	candidates := core.NewUpstreamSet(uncommunicative)

	innerConn := &blackholeConn{}
	policy := &MockDialPolicy{
		Results: []UpstreamErrPair{
			{Upstream: uncommunicative, Error: nil},
		},
		Events: make([]string, 0),
	}
	rd := &RetryDialer{
		Policy:  policy,
		Timeout: time.Nanosecond,
		InnerDialer: &fakeDialer{
			DialDelay: time.Millisecond,
			ResultByUpstream: map[core.Upstream]connErrPair{
				uncommunicative: {
					innerConn,
					nil,
				},
			},
		},
		Logger: slog.VoidLogger{},
	}

	ctx := context.Background()

	_, _, err := rd.DialBestUpstream(ctx, candidates)
	require.ErrorIs(t, err, context.DeadlineExceeded)

	expectedEvents := []string{
		"ChooseBestUpstream",
	}
	require.Equal(t, expectedEvents, policy.Events)
}
