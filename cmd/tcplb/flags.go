package main

import (
	"errors"
	"flag"
	"net"
	"strings"
	"tcplb/lib/core"
)

const (
	commandName     = "tcplb"
	upstreamListSep = ","
)

var InvalidUpstreamListFlag = errors.New("invalid upstream list flag value")

// UpstreamListValue is a flag.Value for lists of Upstream addresses.
type UpstreamListValue struct {
	Upstreams []core.Upstream
}

func (v *UpstreamListValue) String() string {
	n := len(v.Upstreams)
	tokens := make([]string, n, n)
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
			return InvalidUpstreamListFlag
		}
		upstream := core.Upstream{
			Network: defaultUpstreamNetwork,
			Address: net.JoinHostPort(host, port),
		}
		v.Upstreams = append(v.Upstreams, upstream)
	}
	return nil
}

func newConfigFromFlags(argv []string) (*Config, error) {
	flagSet := flag.NewFlagSet(commandName, flag.ExitOnError)

	cfg := &Config{
		ListenNetwork: defaultListenNetwork,
	}

	upstreamListVar := &UpstreamListValue{}

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

	err := flagSet.Parse(argv[1:])
	cfg.Upstreams = upstreamListVar.Upstreams
	return cfg, err
}
