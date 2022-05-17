package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"tcplb/lib/authn"
	"tcplb/lib/authz"
	"tcplb/lib/core"
	"tcplb/lib/forwarder"
	"tcplb/lib/limiter"
	"tcplb/lib/slog"
	"time"
)

const (
	defaultAcceptErrorCooldownDuration = time.Second
	defaultUpstreamNetwork             = "tcp"
	defaultListenNetwork               = "tcp"
	defaultListenAddress               = "0.0.0.0:4321"
	defaultMaxConnectionsPerClient     = 10
)

var NotImplementedError = errors.New("not implemented")

// TODO FIXME insecure
var anonymousTestClientID = core.ClientID{Namespace: "test", Key: "anonymous"}

type Config struct {
	ListenNetwork           string
	ListenAddress           string
	Upstreams               []core.Upstream
	MaxConnectionsPerClient int64
}

func (c *Config) Validate() error {
	if len(c.Upstreams) == 0 {
		return errors.New("server must be configured with 1 or more upstreams")
	}
	return nil
}

func makeClientReserverFromConfig(cfg *Config) (forwarder.ClientReserver, error) {
	var reserver forwarder.ClientReserver
	if cfg.MaxConnectionsPerClient > 0 {
		reserver = limiter.NewUniformlyBoundedClientReserver(cfg.MaxConnectionsPerClient)
	} else {
		reserver = limiter.UnboundedClientReserver{}
	}
	return reserver, nil
}

func makeAuthorizerFromConfig(cfg *Config) (forwarder.Authorizer, error) {
	// TODO FIXME begin placeholder demo authorization config
	urGroup := authz.Group{Key: "ur"}
	urUpstreamGroup := authz.UpstreamGroup{Key: "ur"}
	authzCfg := authz.Config{
		GroupsByClientID: map[core.ClientID][]authz.Group{
			anonymousTestClientID: {urGroup},
		},
		UpstreamGroupsByGroup: map[authz.Group][]authz.UpstreamGroup{
			urGroup: {urUpstreamGroup},
		},
		UpstreamsByUpstreamGroup: map[authz.UpstreamGroup]core.UpstreamSet{
			urUpstreamGroup: core.NewUpstreamSet(cfg.Upstreams...),
		},
	}
	// TODO FIXME end placeholder demo authorization config
	return authz.NewStaticAuthorizer(authzCfg), nil
}

// IdiotDialer attempts to dial an arbitrary candidate and gives up if that fails.
// This is a placeholder implementation with various issues:
// - no timeout
// - it doesn't attempt to balance load
// - it doesn't try alternative upstreams if one attempt fails
// - it doesn't learn anything
type IdiotDialer struct{}

func (d IdiotDialer) DialBestUpstream(ctx context.Context, candidates core.UpstreamSet) (core.Upstream, forwarder.DuplexConn, error) {
	for c := range candidates {
		conn, err := net.Dial(c.Network, c.Address)
		if err != nil {
			return core.Upstream{}, nil, err
		}
		switch upstreamConn := conn.(type) {
		case *net.TCPConn:
			return c, upstreamConn, nil
		default:
			_ = conn.Close()
			break
		}
	}
	return core.Upstream{}, nil, errors.New("idiot dialer failed to dial")
}

func makeDialerFromConfig(cfg *Config) (forwarder.BestUpstreamDialer, error) {
	// TODO FIXME replace with something better
	return IdiotDialer{}, nil
}

func makeForwarderFromConfig(cfg *Config) (forwarder.Forwarder, error) {
	// TODO implement something more robust with timeouts
	return forwarder.MediocreForwarder{}, nil
}

func coerceIntoAuthenticatedConn(logger slog.Logger, conn net.Conn) (forwarder.AuthenticatedConn, error) {
	var authenticatedClientConn forwarder.AuthenticatedConn = nil
	switch cc := conn.(type) {
	case *tls.Conn:
		authenticatedClientConn = &authn.AuthenticatedTLSConn{Conn: cc}
	case *net.TCPConn:
		logger.Warn(&slog.LogRecord{Msg: "using TCP connection to client, this is insecure"})
		authenticatedClientConn = &authn.InsecureTCPConn{
			TCPConn:  cc,
			ClientID: anonymousTestClientID,
		}
	default:
		return nil, NotImplementedError
	}
	return authenticatedClientConn, nil
}

func serve(logger slog.Logger, cfg *Config) error {
	// Wire together the forwarder.Server

	reserver, err := makeClientReserverFromConfig(cfg)
	if err != nil {
		logger.Error(&slog.LogRecord{Msg: "Client rate-limiter error", Error: err})
		return err
	}

	authorizer, err := makeAuthorizerFromConfig(cfg)
	if err != nil {
		logger.Error(&slog.LogRecord{Msg: "Authorization configuration error", Error: err})
		return err
	}

	dialer, err := makeDialerFromConfig(cfg)
	if err != nil {
		logger.Error(&slog.LogRecord{Msg: "Dialer configuration error", Error: err})
		return err
	}

	fwder, err := makeForwarderFromConfig(cfg)
	if err != nil {
		logger.Error(&slog.LogRecord{Msg: "Forwarder configuration error", Error: err})
		return err
	}

	// Compose stack of connection handlers. They are defined
	// in order from innermost to outermost.
	forwardingHandler := &forwarder.ForwardingHandler{
		Logger:    logger,
		Dialer:    dialer,
		Forwarder: fwder,
	}
	authzHandler := &forwarder.AuthorizedUpstreamsHandler{
		Logger:     logger,
		Authorizer: authorizer,
		Inner:      forwardingHandler,
	}
	rateLimitingHandler := &forwarder.RateLimitingHandler{
		Logger:   logger,
		Reserver: reserver,
		Inner:    authzHandler,
	}
	recovererHandler := &forwarder.RecovererHandler{
		Logger: logger,
		Inner:  rateLimitingHandler,
	}
	baseHandler := &forwarder.ConnCloserHandler{
		Inner: recovererHandler,
	}

	listener, err := net.Listen(cfg.ListenNetwork, cfg.ListenAddress)
	if err != nil {
		msg := fmt.Sprintf("Listen error with network: %s address: %s", cfg.ListenNetwork, cfg.ListenAddress)
		logger.Error(&slog.LogRecord{Msg: msg, Error: err})
		return err
	}
	defer func() {
		_ = listener.Close()
	}()

	// TODO graceful shutdown upon receiving interrupt
	// - stop accepting new connections
	// - wait for currently forwarded connections to terminate (hard cut off after timeout?)
	// - stop healthcheck probes of upstreams (if applicable)

	logger.Info(&slog.LogRecord{Msg: fmt.Sprintf("listening on network: %s address: %s", cfg.ListenNetwork, cfg.ListenAddress)})
	for {
		// TODO replace placeholder implementation: accept mTLS instead of TCP.
		clientConn, err := listener.Accept()
		if err != nil {
			logger.Error(&slog.LogRecord{Msg: "listener.Accept error", Error: err})
			time.Sleep(defaultAcceptErrorCooldownDuration)
			continue
		}
		// TODO refactor to remove this AuthenticatedConn abstraction,
		// instead pass the clientID in the context.
		authenticatedClientConn, err := coerceIntoAuthenticatedConn(logger, clientConn)
		if err != nil {
			_ = clientConn.Close()
			return err
		}
		clientId, err := authenticatedClientConn.GetClientID()
		if err != nil {
			_ = clientConn.Close()
			return err
		}
		parentCtx := context.Background() // TODO consider adding cancel
		ctx := forwarder.NewContextWithClientID(parentCtx, clientId)
		// baseHandler.Handle is responsible for closing authenticatedClientConn
		go baseHandler.Handle(ctx, authenticatedClientConn)
	}
	// Unreachable.
}
