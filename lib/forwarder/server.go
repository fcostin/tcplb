package forwarder

import (
	"context"
	"net"
	"runtime/debug"
	"tcplb/lib/core"
	"tcplb/lib/limiter"
	"tcplb/lib/slog"
)

// CloseWriter represents something that can CloseWrite.
//
// Notable implementations in the standard library include:
// - net.TCPCOnn
// - tls.Conn
type CloseWriter interface {
	CloseWrite() error // CloseWrite shuts down the writer side of a connection.
}

type DuplexConn interface {
	net.Conn
	CloseWriter
}

// AuthenticatedConn represents an established authenticated
// connection to a client.
type AuthenticatedConn interface {
	net.Conn
	CloseWriter

	// GetClientID attempts to extract the canonical ClientID representing
	// the identity of an authenticated peer.
	GetClientID() (core.ClientID, error)
}

// ClientReserver represents an entity that can limit "reservations"
// by clients, as an abstraction of client rate limiting.
//
// Multiple goroutines may invoke methods on a ClientReserver
// simultaneously.
type ClientReserver interface {
	// TryReserve attempts to acquire a reservation for the given client.
	// If the attempt succeeds, nil is returned.
	// If no reservations are available, the attempt returns an error.
	// This call does not block.
	TryReserve(ctx context.Context, c core.ClientID) error

	// ReleaseReservation releases a reservation that was previously acquired
	// for the given ClientID c by TryReserve.
	ReleaseReservation(ctx context.Context, c core.ClientID) error
}

// Authorizer abstracts an authorization policy that
// controls which clients are allowed to forward connections to which upstreams.
//
// Multiple goroutines may invoke methods on an Authorizer simultaneously.
type Authorizer interface {
	// AuthorizedUpstreams returns an UpstreamSet of upstreams that the ClientID c
	// is authorized to access. If c is not authorized to access any upstreams,
	// implementations should return an empty UpstreamSet and nil.
	AuthorizedUpstreams(ctx context.Context, c core.ClientID) (core.UpstreamSet, error)
}

// BestUpstreamDialer dials the best upstream out of a set of candidates.
//
// Multiple goroutines may invoke methods on a BestUpstreamDialer simultaneously.
type BestUpstreamDialer interface {
	// DialBestUpstream considers the given candidate upstreams and attempts to connect to
	// the "best" one (implementation defined). If successful, the winning Upstream is
	// returned alongside a DuplexConn to that upstream, and nil error.
	//
	// If error is nil, the caller is responsible for closing the returned DuplexConn
	// once finished with it to avoid leaking resources.
	DialBestUpstream(ctx context.Context, candidates core.UpstreamSet) (core.Upstream, DuplexConn, error)
}

// Forwarder copies data between a client DuplexConn and an upstream DuplexConn.
//
// Multiple goroutines may invoke methods on a Forwarder simultaneously.
type Forwarder interface {
	// Forward connects the clientConn and upstreamConn together, copying
	// application data between the two.
	//
	// The Forward operation blocks until:
	// - one of the two parties closes their end of the connection
	// - one or both of the given connections encounters a serious error
	// - the Forwarder implementation decides to stop forwarding
	//
	// In the event that the forwarding connection is terminated normally
	// by one or both parties, a nil error is returned.
	//
	// Forward implementations must not Close the clientConn or upstreamConn.
	// It may CloseWrite one or both of them.
	Forward(ctx context.Context, clientConn, upstreamConn DuplexConn) error
}

type Server struct {
	Logger     slog.Logger
	Reserver   ClientReserver
	Authorizer Authorizer
	Dialer     BestUpstreamDialer
	Forwarder  Forwarder
}

// Handle accepts the given AuthenticatedConn from the client and
// attempts to forward it to a configured upstream.
//
// Handle is responsible for closing the given AuthenticatedConn.
func (s *Server) Handle(ctx context.Context, conn AuthenticatedConn) {
	defer func() {
		// If there are errors closing the client connection, it is
		// likely due to client or network. Ignore them.
		_ = conn.Close()
	}()
	defer func() {
		panicValue := recover()
		if panicValue == nil {
			// Either not panicking or someone called panic(nil). Assume former.
			return
		}
		s.Logger.Error(&slog.LogRecord{
			Msg:        "Unexpected panic!",
			Details:    panicValue,
			StackTrace: string(debug.Stack()),
		})
	}()

	clientID, err := conn.GetClientID()
	if err != nil {
		s.Logger.Error(&slog.LogRecord{Msg: "Failed to get ClientID", Error: err})
		return
	}

	// Clients are subject to rate-limiting.
	err = s.Reserver.TryReserve(ctx, clientID)
	if err != nil {
		switch err {
		// TODO: refactor to break dep on package lib/limiter
		case limiter.MaxReservationsExceeded:
			s.Logger.Warn(&slog.LogRecord{Msg: "Client rate limited", ClientID: &clientID})
		default:
			s.Logger.Error(&slog.LogRecord{Msg: "TryReserve error", ClientID: &clientID, Error: err})
		}
		return
	}
	defer func() {
		err := s.Reserver.ReleaseReservation(ctx, clientID)
		if err != nil {
			s.Logger.Error(&slog.LogRecord{Msg: "ReleaseReservation error", ClientID: &clientID, Error: err})
		}
	}()

	// Clients are only authorized to forward to certain upstreams.
	authzUpstreams, err := s.Authorizer.AuthorizedUpstreams(ctx, clientID)
	if err != nil {
		s.Logger.Error(&slog.LogRecord{Msg: "AuthorizedUpstreams error", ClientID: &clientID, Error: err})
		return
	}
	if len(authzUpstreams) == 0 {
		s.Logger.Warn(&slog.LogRecord{Msg: "Client not authorized for forwarding", ClientID: &clientID, Error: err})
		return
	}

	upstream, upstreamConn, err := s.Dialer.DialBestUpstream(ctx, authzUpstreams)
	if err != nil {
		// TODO many failure modes end up here. Improve logging to help the operator triage.
		s.Logger.Error(&slog.LogRecord{Msg: "DialBestUpstream error", ClientID: &clientID, Error: err})
		return
	}
	defer func() {
		// If there are errors closing the upstream connection, it is
		// likely due to upstream or network. Ignore them.
		_ = upstreamConn.Close()
	}()
	s.Logger.Info(&slog.LogRecord{Msg: "Attempting Forward", ClientID: &clientID, Upstream: &upstream})
	err = s.Forwarder.Forward(ctx, conn, upstreamConn)
	if err != nil {
		// TODO if upstreamConn is established successfully but later experiences an error that
		// causes Forward to terminate abnormally, then arguably we could sense that here and
		// lodge a HealthReport about that upstream.
		// An alternative approach could be to handle it internally within the BestUpstreamDialer
		// abstraction, which could wrap & instrument the returned upstreamConn to report health.

		s.Logger.Error(&slog.LogRecord{Msg: "Forward complete with error", ClientID: &clientID, Upstream: &upstream, Error: err})
		return
	}
	s.Logger.Info(&slog.LogRecord{Msg: "Forward complete", ClientID: &clientID, Upstream: &upstream})
}
