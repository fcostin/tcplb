package forwarder

import (
	"context"
	"runtime/debug"
	"tcplb/lib/core"
	"tcplb/lib/limiter"
	"tcplb/lib/slog"
)

const (
	RecovererUnexpectedPanicMessage = "RecovererHandler: Unexpected panic!"
)

type clientIdContextKeyType struct{}
type upstreamsContextKeyType struct{}

var clientIdContextKey = clientIdContextKeyType{}
var upstreamContextKey = upstreamsContextKeyType{}

func NewContextWithClientID(parent context.Context, clientID core.ClientID) context.Context {
	return context.WithValue(parent, clientIdContextKey, clientID)
}

func ClientIDFromContext(ctx context.Context) (core.ClientID, bool) {
	clientID, ok := ctx.Value(clientIdContextKey).(core.ClientID)
	return clientID, ok
}

func NewContextWithUpstreams(parent context.Context, upstreams core.UpstreamSet) context.Context {
	return context.WithValue(parent, upstreamContextKey, upstreams)
}

func UpstreamsFromContext(ctx context.Context) (core.UpstreamSet, bool) {
	upstreams, ok := ctx.Value(upstreamContextKey).(core.UpstreamSet)
	return upstreams, ok
}

type Handler interface {
	// Handle accepts the given AuthenticatedConn from the client.
	Handle(ctx context.Context, conn AuthenticatedConn)
}

// ConnCloserHandler is a handler that closes the client connection
// after the Inner handler has finished handling it. It should be the
// base Handler in the stack.
type ConnCloserHandler struct {
	Inner Handler
}

func (h *ConnCloserHandler) Handle(ctx context.Context, conn AuthenticatedConn) {
	defer func() {
		// If there are errors closing the client connection, it is
		// likely due to client or network. Ignore them.
		_ = conn.Close()
	}()
	h.Inner.Handle(ctx, conn)
}

var _ Handler = (*ConnCloserHandler)(nil) // type check

// RecovererHandler is a handler that recovers from panics raised by the
// Inner handler (if any) and logs an error to the given Logger.
type RecovererHandler struct {
	Logger slog.Logger
	Inner  Handler
}

func (h *RecovererHandler) Handle(ctx context.Context, conn AuthenticatedConn) {
	defer func() {
		panicValue := recover()
		if panicValue == nil {
			// Either not panicking or someone called panic(nil). Assume former.
			return
		}
		h.Logger.Error(&slog.LogRecord{
			Msg:        RecovererUnexpectedPanicMessage,
			Details:    panicValue,
			StackTrace: string(debug.Stack()),
		})
	}()
	h.Inner.Handle(ctx, conn)
}

var _ Handler = (*RecovererHandler)(nil) // type check

// RateLimitingHandler is a handler that only allows the Inner handler to
// Handle the connection if a reservation can be obtained for the ClientID.
// A ClientID is expected to be found in the context.
type RateLimitingHandler struct {
	Logger   slog.Logger
	Reserver ClientReserver
	Inner    Handler
}

func (h *RateLimitingHandler) Handle(ctx context.Context, conn AuthenticatedConn) {
	clientID, ok := ClientIDFromContext(ctx)
	if !ok {
		h.Logger.Error(&slog.LogRecord{Msg: "RateLimitingHandler: Failed to get ClientID from context"})
		return
	}

	// Clients are subject to rate-limiting.
	err := h.Reserver.TryReserve(ctx, clientID)
	if err != nil {
		switch err {
		// TODO: refactor to break dep on package lib/limiter
		case limiter.MaxReservationsExceeded:
			h.Logger.Warn(&slog.LogRecord{Msg: "RateLimitingHandler: Client rate limited", ClientID: &clientID})
		default:
			h.Logger.Error(&slog.LogRecord{Msg: "RateLimitingHandler: TryReserve error", ClientID: &clientID, Error: err})
		}
		return
	}
	defer func() {
		err := h.Reserver.ReleaseReservation(ctx, clientID)
		if err != nil {
			h.Logger.Error(&slog.LogRecord{Msg: "RateLimitingHandler: ReleaseReservation error", ClientID: &clientID, Error: err})
		}
	}()

	h.Inner.Handle(ctx, conn)
}

var _ Handler = (*RateLimitingHandler)(nil) // type check

// AuthorizedUpstreamsHandler is a handler that determines which upstreams
// the client connection is authorized to forward to. If the client is
// authorized to connect to one or more upstreams, an UpstreamSet is stored
// in the child context passed to the Inner Handler, and can be extracted
// with UpstreamsFromContext.
type AuthorizedUpstreamsHandler struct {
	Logger     slog.Logger
	Authorizer Authorizer
	Inner      Handler
}

func (h *AuthorizedUpstreamsHandler) Handle(ctx context.Context, conn AuthenticatedConn) {
	clientID, ok := ClientIDFromContext(ctx)
	if !ok {
		h.Logger.Error(&slog.LogRecord{Msg: "AuthorizedUpstreamsHandler: Failed to get ClientID from context"})
		return
	}

	// Clients are only authorized to forward to certain upstreams.
	authzUpstreams, err := h.Authorizer.AuthorizedUpstreams(ctx, clientID)
	if err != nil {
		h.Logger.Error(&slog.LogRecord{Msg: "AuthorizedUpstreamsHandler: AuthorizedUpstreams error", ClientID: &clientID, Error: err})
		return
	}
	if len(authzUpstreams) == 0 {
		h.Logger.Warn(&slog.LogRecord{Msg: "Client not authorized for forwarding", ClientID: &clientID, Error: err})
		return
	}

	childCtx := NewContextWithUpstreams(ctx, authzUpstreams)

	h.Inner.Handle(childCtx, conn)
}

var _ Handler = (*AuthorizedUpstreamsHandler)(nil) // type check

// ForwardingHandler is the terminal handler that dials the best upstream to
// serve the client connection, then forwards the client connection to that upstream.
// It expects to find clientID and upstreams (the set of candidate upstreams to
// consider forwarding to) in the given context.
type ForwardingHandler struct {
	Logger    slog.Logger
	Dialer    BestUpstreamDialer
	Forwarder Forwarder
}

func (h *ForwardingHandler) Handle(ctx context.Context, conn AuthenticatedConn) {
	clientID, ok := ClientIDFromContext(ctx)
	if !ok {
		h.Logger.Error(&slog.LogRecord{Msg: "ForwardingHandler: Failed to get ClientID from context"})
		return
	}
	candidateUpstreams, ok := UpstreamsFromContext(ctx)
	if !ok {
		h.Logger.Error(&slog.LogRecord{Msg: "ForwardingHandler: Failed to get candidate Upstreams from context"})
		return
	}
	upstream, upstreamConn, err := h.Dialer.DialBestUpstream(ctx, candidateUpstreams)
	if err != nil {
		// TODO many failure modes end up here. Improve logging to help the operator triage.
		h.Logger.Error(&slog.LogRecord{Msg: "ForwardingHandler: DialBestUpstream error", ClientID: &clientID, Error: err})
		return
	}
	defer func() {
		// If there are errors closing the upstream connection, it is
		// likely due to upstream or network. Ignore them.
		_ = upstreamConn.Close()
	}()
	h.Logger.Info(&slog.LogRecord{Msg: "ForwardingHandler: Attempting Forward", ClientID: &clientID, Upstream: &upstream})
	err = h.Forwarder.Forward(ctx, conn, upstreamConn)
	if err != nil {
		// TODO if upstreamConn is established successfully but later experiences an error that
		// causes Forward to terminate abnormally, then arguably we could sense that here and
		// lodge a HealthReport about that upstream.
		// An alternative approach could be to handle it internally within the BestUpstreamDialer
		// abstraction, which could wrap & instrument the returned upstreamConn to report health.

		h.Logger.Error(&slog.LogRecord{Msg: "ForwardingHandler: Forward complete with error", ClientID: &clientID, Upstream: &upstream, Error: err})
		return
	}
	h.Logger.Info(&slog.LogRecord{Msg: "ForwardingHandler: Forward complete", ClientID: &clientID, Upstream: &upstream})
}

var _ Handler = (*ForwardingHandler)(nil) // type check
