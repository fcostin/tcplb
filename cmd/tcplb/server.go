package main

import (
	"errors"
	"fmt"
	"net"
	"tcplb/lib/authz"
	"tcplb/lib/core"
	"tcplb/lib/dialer"
	"tcplb/lib/forwarder"
	"tcplb/lib/limiter"
	"tcplb/lib/slog"
	"time"
)

const (
	defaultAcceptErrorCooldownDuration = time.Second
	defaultDialerTimeout               = 15 * time.Second
	defaultUpstreamNetwork             = "tcp"
	defaultListenNetwork               = "tcp"
	defaultListenAddress               = "0.0.0.0:4321"
	defaultMaxConnectionsPerClient     = 10
)

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

func makeDialerFromConfig(cfg *Config, logger slog.Logger) (forwarder.BestUpstreamDialer, error) {
	dialer := &dialer.RetryDialer{
		Logger:      logger,
		Timeout:     defaultDialerTimeout,
		Policy:      dialer.NewLeastConnectionDialPolicy(),
		InnerDialer: dialer.SimpleUpstreamDialer{},
	}
	return dialer, nil
}

func makeForwarderFromConfig(cfg *Config) (forwarder.Forwarder, error) {
	// TODO implement something more robust with timeouts
	return forwarder.MediocreForwarder{}, nil
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

	dialer, err := makeDialerFromConfig(cfg, logger)
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
	// TODO replace placeholder implementation: use mTLS for authn
	authnHandler := &forwarder.AnonymousAuthenticationHandler{
		Logger:    logger,
		Inner:     rateLimitingHandler,
		Anonymous: anonymousTestClientID,
	}
	baseHandler := &forwarder.ConnCloserHandler{
		Inner: authnHandler,
	}

	// TODO replace placeholder implementation: accept TLS instead of TCP.
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

	s := &forwarder.Server{
		Logger:                      logger,
		Handler:                     baseHandler,
		Listener:                    listener,
		AcceptErrorCooldownDuration: defaultAcceptErrorCooldownDuration,
	}
	return s.Serve()
}
