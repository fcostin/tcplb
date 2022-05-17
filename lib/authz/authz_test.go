package authz

import (
	"context"
	"tcplb/lib/core"
	"testing"

	"github.com/stretchr/testify/require"
)

type DummyClientID string

type DummyUpstream string

func (u DummyUpstream) Name() string {
	return string(u)
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
		UpstreamsByUpstreamGroup: map[UpstreamGroup]core.USet{
			web:    core.NewUSet(web1, web2),
			worker: core.NewUSet(worker1, worker2),
		},
	}

	scenarios := []struct {
		name              string
		c                 core.ClientID
		cfg               Config
		expectedUpstreams core.USet
	}{
		{
			name:              "zero alice query",
			c:                 alice,
			cfg:               cfgZero,
			expectedUpstreams: core.EmptyUSet(),
		},
		{
			name:              "small alice query",
			c:                 alice,
			cfg:               cfgSmall,
			expectedUpstreams: core.NewUSet(web1, web2, worker1, worker2),
		},
		{
			name:              "small bob query",
			c:                 bob,
			cfg:               cfgSmall,
			expectedUpstreams: core.NewUSet(web1, web2, worker1, worker2),
		},
		{
			name:              "small cindy query",
			c:                 cindy,
			cfg:               cfgSmall,
			expectedUpstreams: core.NewUSet(worker1, worker2),
		},
		{
			name:              "small dinesh query",
			c:                 dinesh,
			cfg:               cfgSmall,
			expectedUpstreams: core.NewUSet(web1, web2),
		},
		{
			name:              "small eve query",
			c:                 eve,
			cfg:               cfgSmall,
			expectedUpstreams: core.EmptyUSet(),
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
