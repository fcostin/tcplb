package main

import (
	"errors"
	"flag"
	"fmt"
	"net"
	"strings"
	"tcplb/lib/authn"
	"tcplb/lib/core"
)

const (
	commandName     = "tcplb"
	upstreamListSep = ","
	clientIDListSep = ","
)

// UpstreamListValue is a flag.Value for lists of Upstream addresses.
type UpstreamListValue struct {
	Upstreams []core.Upstream
}

func (v *UpstreamListValue) String() string {
	n := len(v.Upstreams)
	tokens := make([]string, n)
	for i, u := range v.Upstreams {
		tokens[i] = u.Address
	}
	return strings.Join(tokens, upstreamListSep)
}

func (v *UpstreamListValue) Set(s string) error {
	tokens := strings.Split(s, upstreamListSep)
	for _, token := range tokens {
		host, port, err := net.SplitHostPort(token)
		if err != nil {
			msg := fmt.Sprintf("expected upstream address of form host:port but got %s", token)
			return errors.New(msg)
		}
		upstream := core.Upstream{
			Network: defaultUpstreamNetwork,
			Address: net.JoinHostPort(host, port),
		}
		v.Upstreams = append(v.Upstreams, upstream)
	}
	return nil
}

// ClientIDListValue is a flag.Value for lists of ClientIDs
type ClientIDListValue struct {
	ClientIDs []core.ClientID
}

func (v *ClientIDListValue) String() string {
	n := len(v.ClientIDs)
	tokens := make([]string, n)
	for i, c := range v.ClientIDs {
		tokens[i] = c.Key
	}
	return strings.Join(tokens, clientIDListSep)
}

func (v *ClientIDListValue) Set(s string) error {
	tokens := strings.Split(s, clientIDListSep)
	for _, token := range tokens {
		c := core.ClientID{
			Namespace: authn.DefaultNamespace,
			Key:       token,
		}
		v.ClientIDs = append(v.ClientIDs, c)
	}
	return nil
}

func newConfigFromFlags(argv []string) (*Config, error) {
	flagSet := flag.NewFlagSet(commandName, flag.ExitOnError)

	cfg := &Config{
		ListenNetwork:       defaultListenNetwork,
		TLSHandshakeTimeout: defaultTLSHandshakeTimeout,
		Authorization:       &AuthzConfig{},
	}

	tlsConfig := &TLSConfig{}

	upstreamListVar := &UpstreamListValue{}

	clientIDListVar := &ClientIDListValue{}

	var insecureAcceptTCP bool

	flagSet.StringVar(
		&(cfg.ListenAddress),
		"listen-address",
		defaultListenAddress,
		"listen address as host:port")
	flagSet.Int64Var(
		&(cfg.MaxConnectionsPerClient),
		"max-conns-per-client",
		defaultMaxConnectionsPerClient,
		"connection limit per client. if not positive, no limit.")
	flagSet.Var(
		upstreamListVar,
		"upstreams",
		"comma-separated list of upstream as host:port")
	flagSet.Var(
		clientIDListVar,
		"authzd-clients",
		"comma-separated list of authorized client CommonNames")

	flagSet.StringVar(
		&(tlsConfig.ServerKeyFile),
		"key-file",
		"",
		"filename of PEM-encoded private key, for serving TLS")
	flagSet.StringVar(
		&(tlsConfig.ServerCertFile),
		"cert-file",
		"",
		"filename of PEM-encoded certificate chain, ordered leaf first, for serving TLS")
	flagSet.StringVar(
		&(tlsConfig.RootCAPath),
		"ca-root-file",
		"",
		"filename of PEM-encoded trusted CA root certificates")

	flagSet.BoolVar(
		&insecureAcceptTCP,
		"insecure-accept-tcp",
		false,
		"disable TLS and instead accept anonymous TCP connections? (INSECURE)")

	err := flagSet.Parse(argv[1:])
	cfg.Upstreams = upstreamListVar.Upstreams

	cfg.Authorization.AuthorizedClients = clientIDListVar.ClientIDs

	if insecureAcceptTCP {
		cfg.Authentication = &AuthnConfig{AllowAnonymous: true}
	} else {
		cfg.TLS = tlsConfig
	}

	// TODO FIXME allow authz to be configured.

	return cfg, err
}
