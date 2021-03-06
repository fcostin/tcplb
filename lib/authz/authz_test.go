package authz

import (
	"context"
	"tcplb/lib/core"
	"testing"

	"github.com/stretchr/testify/require"
)

func DummyClientID(key string) core.ClientID {
	return core.ClientID{Namespace: "authz_test", Key: key}
}

func DummyUpstream(key string) core.Upstream {
	return core.Upstream{Network: "authz_test_network", Address: key}
}

func TestAuthorizer(t *testing.T) {
	alice := DummyClientID("alice")
	bob := DummyClientID("bob")
	cindy := DummyClientID("cindy")
	dinesh := DummyClientID("dinesh")
	eve := DummyClientID("eve")

	alpha := Group{Key: "alpha"}
	beta := Group{Key: "beta"}
	admin := Group{Key: "admin"}

	web := UpstreamGroup{Key: "web"}
	worker := UpstreamGroup{Key: "worker"}

	web1 := DummyUpstream("web1")
	web2 := DummyUpstream("web2")
	worker1 := DummyUpstream("worker1")
	worker2 := DummyUpstream("worker2")

	cfgZero := Config{}

	cfgSmall := Config{
		GroupsByClientID: map[core.ClientID][]Group{
			alice:  {admin},
			bob:    {beta, alpha},
			cindy:  {beta},
			dinesh: {alpha},
		},
		UpstreamGroupsByGroup: map[Group][]UpstreamGroup{
			alpha: {web},
			beta:  {worker},
			admin: {web, worker},
		},
		UpstreamsByUpstreamGroup: map[UpstreamGroup]core.UpstreamSet{
			web:    core.NewUpstreamSet(web1, web2),
			worker: core.NewUpstreamSet(worker1, worker2),
		},
	}

	scenarios := []struct {
		name              string
		c                 core.ClientID
		cfg               Config
		expectedUpstreams core.UpstreamSet
	}{
		{
			name:              "zero alice query",
			c:                 alice,
			cfg:               cfgZero,
			expectedUpstreams: core.EmptyUpstreamSet(),
		},
		{
			name:              "small alice query",
			c:                 alice,
			cfg:               cfgSmall,
			expectedUpstreams: core.NewUpstreamSet(web1, web2, worker1, worker2),
		},
		{
			name:              "small bob query",
			c:                 bob,
			cfg:               cfgSmall,
			expectedUpstreams: core.NewUpstreamSet(web1, web2, worker1, worker2),
		},
		{
			name:              "small cindy query",
			c:                 cindy,
			cfg:               cfgSmall,
			expectedUpstreams: core.NewUpstreamSet(worker1, worker2),
		},
		{
			name:              "small dinesh query",
			c:                 dinesh,
			cfg:               cfgSmall,
			expectedUpstreams: core.NewUpstreamSet(web1, web2),
		},
		{
			name:              "small eve query",
			c:                 eve,
			cfg:               cfgSmall,
			expectedUpstreams: core.EmptyUpstreamSet(),
		},
	}

	for _, s := range scenarios {
		t.Run(s.name, func(t *testing.T) {
			a := NewStaticAuthorizer(s.cfg)

			ctx := context.Background()
			upstreams, err := a.AuthorizedUpstreams(ctx, s.c)

			require.NoError(t, err)
			require.Equal(t, s.expectedUpstreams, upstreams)
		})
	}
}
