package forwarder

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"tcplb/lib/core"
	"tcplb/lib/slog"
	"time"
)

var ConnectionTypeUnsupported = errors.New("connection type unsupported")

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
	Logger                      slog.Logger
	Handler                     Handler
	Listener                    net.Listener
	AcceptErrorCooldownDuration time.Duration
}

func (s *Server) Serve() error {
	for {
		clientConn, err := s.Listener.Accept()
		if err != nil {
			s.Logger.Error(&slog.LogRecord{Msg: "listener.Accept error", Error: err})
			time.Sleep(s.AcceptErrorCooldownDuration)
			continue
		}
		duplexClientConn, err := asDuplexConn(clientConn)
		if err != nil {
			_ = clientConn.Close()
			return err
		}
		ctx := context.Background() // TODO consider adding cancel

		// Handler is responsible for closing the client conn
		go s.Handler.Handle(ctx, duplexClientConn)
	}
}

func asDuplexConn(conn net.Conn) (DuplexConn, error) {
	switch cc := conn.(type) {
	case *tls.Conn:
		return cc, nil
	case *net.TCPConn:
		return cc, nil
	default:
		return nil, ConnectionTypeUnsupported
	}
}
