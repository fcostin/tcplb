package forwarder

import (
	"context"
	"net"
	"tcplb/lib/core"
	"time"
)

type CloseWriter interface {
	CloseWrite() error // CloseWrite shuts down the writer side of a connection.
}

type UpstreamConn interface {
	net.Conn
	CloseWriter
}

type UpstreamDialer interface {
	DialUpstream(ctx context.Context, u core.Upstream) (UpstreamConn, error)
}

type TimeoutDialer struct {
	Timeout time.Duration
	Inner   UpstreamDialer
}

func (d TimeoutDialer) DialUpstream(ctx context.Context, u core.Upstream) (UpstreamConn, error) {
	childCtx, cancel := context.WithTimeout(ctx, d.Timeout)
	defer cancel()
	return d.Inner.DialUpstream(childCtx, u)
}
