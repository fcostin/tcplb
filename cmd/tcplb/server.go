package main

import (
	"crypto/ed25519"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
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
	defaultApplicationIdleTimeout      = 15 * time.Second
	defaultDialerTimeout               = 15 * time.Second
	defaultTLSHandshakeTimeout         = 15 * time.Second
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
	ApplicationIdleTimeout  time.Duration
	TLSHandshakeTimeout     time.Duration
	TLS                     *TLSConfig
	Authentication          *AuthnConfig
	Authorization           *AuthzConfig
}

type TLSConfig struct {
	ServerCertFile string
	ServerKeyFile  string
	RootCAPath     string
}

type AuthnConfig struct {
	AllowAnonymous bool
}

type AuthzConfig struct {
	AuthorizedClients []core.ClientID
}

func (c *Config) Validate() error {
	if len(c.Upstreams) == 0 {
		return errors.New("server must be configured with 1 or more upstreams")
	}

	someTLSConfig := len(c.TLS.ServerKeyFile) > 0 || len(c.TLS.ServerCertFile) > 0 || len(c.TLS.RootCAPath) > 0
	allTLSConfig := len(c.TLS.ServerKeyFile) > 0 && len(c.TLS.ServerCertFile) > 0 && len(c.TLS.RootCAPath) > 0

	if someTLSConfig && !allTLSConfig {
		return errors.New("TLS misconfiguration: key-file, cert-file and ca-root-file must all be given")
	}
	if allTLSConfig {
		if c.Authentication != nil && c.Authentication.AllowAnonymous {
			return errors.New("refusing to allow anonymous authentication when TLS is configured")
		}
	}
	if !someTLSConfig {
		if c.Authentication == nil || c.Authentication.AllowAnonymous {
			return errors.New("TLS configuration not found")
		}
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
		GroupsByClientID: make(map[core.ClientID][]authz.Group),
		UpstreamGroupsByGroup: map[authz.Group][]authz.UpstreamGroup{
			urGroup: {urUpstreamGroup},
		},
		UpstreamsByUpstreamGroup: map[authz.UpstreamGroup]core.UpstreamSet{
			urUpstreamGroup: core.NewUpstreamSet(cfg.Upstreams...),
		},
	}

	if cfg.Authorization != nil {
		for _, client := range cfg.Authorization.AuthorizedClients {
			authzCfg.GroupsByClientID[client] = []authz.Group{urGroup}
		}
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

func makeForwarderFromConfig(cfg *Config, logger slog.Logger) (forwarder.Forwarder, error) {
	return &forwarder.ForwardingSupervisor{
		IdleTimeout: cfg.ApplicationIdleTimeout,
		Logger:      logger,
	}, nil
}

func loadServerCertificatesFromTLSConfig(cfg *TLSConfig) ([]tls.Certificate, error) {
	cert, err := tls.LoadX509KeyPair(cfg.ServerCertFile, cfg.ServerKeyFile)
	if err != nil {
		return nil, err
	}
	// We expect ed25519 and accept no substitute.
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return nil, err
	}
	switch pub := leaf.PublicKey.(type) {
	case ed25519.PublicKey:
	default:
		msg := fmt.Sprintf("expected server certificate using key algorithm ed25519 but instead got %T", pub)
		return nil, errors.New(msg)
	}

	chains := []tls.Certificate{
		cert,
	}
	return chains, nil
}

func loadRootCAs(rootCAPath string) (*x509.CertPool, error) {
	// Variant of x509 CertPool AppendCertsFromPEM that fails on errors.
	// The version in the standard library skips over certs that don't parse. (!)
	AppendCertsFromPEM := func(pool *x509.CertPool, pemCerts []byte) error {
		for len(pemCerts) > 0 {
			var block *pem.Block
			block, pemCerts = pem.Decode(pemCerts)
			if block == nil {
				break
			}
			if block.Type != "CERTIFICATE" || len(block.Headers) != 0 {
				continue
			}

			certBytes := block.Bytes
			cert, err := x509.ParseCertificate(certBytes)
			if err != nil {
				return err
			}
			pool.AddCert(cert)
		}
		return nil
	}

	f, err := os.Open(rootCAPath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	data, err := io.ReadAll(f)
	if err != nil {
		return nil, err
	}

	pool := x509.NewCertPool()
	err = AppendCertsFromPEM(pool, data)
	return pool, err
}

func makeListenerFromConfig(cfg *Config, logger slog.Logger) (net.Listener, error) {
	if cfg.TLS == nil {
		logger.Warn(&slog.LogRecord{Msg: "no TLS configuration found"})
		listener, err := net.Listen(cfg.ListenNetwork, cfg.ListenAddress)
		if err == nil {
			logger.Warn(&slog.LogRecord{Msg: "created insecure TCP listener"})
		}
		return listener, err
	}
	logger.Info(&slog.LogRecord{Msg: "TLS - found configuration"})
	certificates, err := loadServerCertificatesFromTLSConfig(cfg.TLS)
	if err != nil {
		return nil, err
	}
	logger.Info(&slog.LogRecord{Msg: "TLS - loaded server certificate and key"})
	rootCAs, err := loadRootCAs(cfg.TLS.RootCAPath)
	if err != nil {
		return nil, err
	}
	logger.Info(&slog.LogRecord{Msg: "TLS - loaded server root CAs"})
	tlsConfig := &tls.Config{
		Certificates: certificates,
		ClientCAs:    rootCAs,
		RootCAs:      x509.NewCertPool(), // we plan no outbound TLS connections; trust no one.
		ClientAuth:   tls.RequireAndVerifyClientCert,
		MinVersion:   tls.VersionTLS13,
		MaxVersion:   tls.VersionTLS13,
	}
	listener, err := tls.Listen(cfg.ListenNetwork, cfg.ListenAddress, tlsConfig)
	if err == nil {
		logger.Info(&slog.LogRecord{Msg: "TLS - created listener"})
	}
	return listener, err
}

func makeAuthenticatorFromConfig(cfg *Config, logger slog.Logger, inner forwarder.Handler) (forwarder.Handler, error) {
	if cfg.Authentication != nil && cfg.Authentication.AllowAnonymous {
		return &forwarder.AnonymousAuthenticationHandler{
			Logger:    logger,
			Inner:     inner,
			Anonymous: anonymousTestClientID,
		}, nil
	}
	return &forwarder.MTLSAuthenticationHandler{
		Logger:           logger,
		Inner:            inner,
		HandshakeTimeout: cfg.TLSHandshakeTimeout,
	}, nil
}

func NewServer(logger slog.Logger, cfg *Config) (*forwarder.Server, error) {
	// Wire together the forwarder.Server

	reserver, err := makeClientReserverFromConfig(cfg)
	if err != nil {
		logger.Error(&slog.LogRecord{Msg: "Client rate-limiter error", Error: err})
		return nil, err
	}

	authorizer, err := makeAuthorizerFromConfig(cfg)
	if err != nil {
		logger.Error(&slog.LogRecord{Msg: "Authorization configuration error", Error: err})
		return nil, err
	}

	dialer, err := makeDialerFromConfig(cfg, logger)
	if err != nil {
		logger.Error(&slog.LogRecord{Msg: "Dialer configuration error", Error: err})
		return nil, err
	}

	fwder, err := makeForwarderFromConfig(cfg, logger)
	if err != nil {
		logger.Error(&slog.LogRecord{Msg: "Forwarder configuration error", Error: err})
		return nil, err
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
	authnHandler, err := makeAuthenticatorFromConfig(cfg, logger, rateLimitingHandler)
	if err != nil {
		logger.Error(&slog.LogRecord{Msg: "Authenticator configuration error", Error: err})
		return nil, err
	}
	baseHandler := &forwarder.ConnCloserHandler{
		Inner: authnHandler,
	}

	listener, err := makeListenerFromConfig(cfg, logger)
	if err != nil {
		msg := fmt.Sprintf("Listen error with network: %s address: %s", cfg.ListenNetwork, cfg.ListenAddress)
		logger.Error(&slog.LogRecord{Msg: msg, Error: err})
		return nil, err
	}

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
	return s, nil
}
