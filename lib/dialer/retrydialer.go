package dialer

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"tcplb/lib/core"
	"tcplb/lib/forwarder"
	"tcplb/lib/slog"
	"time"
)

// NoCandidateUpstreams is an error returned by RetryDialer if there are no
// upstreams given as candidates to dial.
var NoCandidateUpstreams = errors.New("no candidate upstreams")
var ConnectionTypeUnsupported = errors.New("connection type is not supported")

// DialPolicy controls which upstream to dial, out of a set of candidates.
//
// Multiple goroutines may invoke methods on a UpstreamDialer simultaneously.
type DialPolicy interface {

	// ChooseBestUpstream asks the policy to choose an upstream from the
	// given set of candidates. In the case that the policy decides
	// none of the candidates are feasible, an error is returned.
	ChooseBestUpstream(candidates core.UpstreamSet) (core.Upstream, error)

	// DialFailed informs the policy that a dial attempt failed
	DialFailed(upstream core.Upstream, symptom error)

	// DialSucceeded informs the policy that a dial attempt succeeded
	DialSucceeded(upstream core.Upstream)

	// ConnectionClosed informs the policy that a connection created by a prior
	// successful dial attempt has been closed.
	ConnectionClosed(upstream core.Upstream)
}

// UpstreamDialer dials upstreams.
//
// Multiple goroutines may invoke methods on a UpstreamDialer simultaneously.
type UpstreamDialer interface {
	// DialUpstream dials upstream, returning a DuplexConn if a connection is established.
	// Implementations should honour context deadlines, timeouts, and cancellations (if any).
	DialUpstream(ctx context.Context, upstream core.Upstream) (forwarder.DuplexConn, error)
}

type SimpleUpstreamDialer struct{}

func (d SimpleUpstreamDialer) DialUpstream(ctx context.Context, upstream core.Upstream) (forwarder.DuplexConn, error) {
	dd := net.Dialer{}
	conn, err := dd.DialContext(ctx, upstream.Network, upstream.Address)
	if err != nil {
		return nil, err
	}
	switch c := conn.(type) {
	case *net.TCPConn:
		return c, nil
	case *tls.Conn:
		return c, nil
	default:
		_ = conn.Close()
		return nil, ConnectionTypeUnsupported
	}
}

// RetryDialer attempts to dial a candidate Upstream as selected by a
// configurable DialPolicy. If the dial attempt fails, it informs the policy
// of the failure and asks the policy for the next candidate upstream.
// RetryDialer requires a Timeout to be supplied, which is shared across
// all dial attempts.
//
// Multiple goroutines may invoke methods on a RetryDialer simultaneously.
type RetryDialer struct {
	Logger      slog.Logger
	Timeout     time.Duration // Timeout to apply for each DialBestUpstream operation.
	Policy      DialPolicy
	InnerDialer UpstreamDialer
}

func (d *RetryDialer) DialBestUpstream(ctx context.Context, candidates core.UpstreamSet) (core.Upstream, forwarder.DuplexConn, error) {
	if len(candidates) == 0 {
		return core.Upstream{}, nil, NoCandidateUpstreams
	}
	// TODO use shorter timeout for each of n > 1 dial attempts?
	dialCtx, cancel := context.WithTimeout(ctx, d.Timeout)
	defer cancel()

	for {
		upstream, err := d.Policy.ChooseBestUpstream(candidates)
		if err != nil {
			// TODO could sleep here (honouring dialCtx timeout) to give policy chance to change its mind
			return core.Upstream{}, nil, err
		}
		conn, err := d.InnerDialer.DialUpstream(dialCtx, upstream)
		if err != nil {
			// If we exceeded the dial timeout, then dialCtx.Err() is non-nil
			if dialCtxErr := dialCtx.Err(); dialCtxErr != nil {
				d.Logger.Warn(&slog.LogRecord{Msg: "dial timed out", Upstream: &upstream})
				// We cannot infer much about upstream health in this scenario.
				// Halt and indicate to caller that we timed out.
				return core.Upstream{}, nil, dialCtxErr
			}
			d.Logger.Warn(&slog.LogRecord{Msg: "dial failed", Upstream: &upstream})
			d.Policy.DialFailed(upstream, err)
			continue
		}
		d.Logger.Info(&slog.LogRecord{Msg: "dial succeeded", Upstream: &upstream})
		d.Policy.DialSucceeded(upstream)

		// Wrap & instrument the returned conn to inform the DialPolicy on conn Close.
		wrappedConn := &CloseNotifyingDuplexConn{
			DuplexConn: conn,
			OnClose: func() {
				d.Policy.ConnectionClosed(upstream)
			},
		}
		return upstream, wrappedConn, nil
	}
}

type CloseNotifyingDuplexConn struct {
	forwarder.DuplexConn
	OnClose func()
}

func (c *CloseNotifyingDuplexConn) Close() error {
	defer c.OnClose()
	return c.DuplexConn.Close()
}
