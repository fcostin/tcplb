package static

import (
	"context"
	"tcplb/lib/authz"
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

	alpha := NewGroup("alpha")
	beta := NewGroup("beta")
	admin := NewGroup("admin")

	web := NewUGroup("web")
	worker := NewUGroup("worker")

	web1 := DummyUpstream("web1")
	web2 := DummyUpstream("web2")
	worker1 := DummyUpstream("worker1")
	worker2 := DummyUpstream("worker2")

	cfgZero := Config{}

	cfgSmall := Config{
		GroupsByClientID: map[authz.ClientID][]Group{
			alice:  []Group{admin},
			bob:    []Group{beta, alpha},
			cindy:  []Group{beta},
			dinesh: []Group{alpha},
		},
		UGroupsByGroup: map[Group][]UGroup{
			alpha: []UGroup{web},
			beta:  []UGroup{worker},
			admin: []UGroup{web, worker},
		},
		UpstreamsByUGroup: map[UGroup]authz.USet{
			web:    authz.NewUSet(web1, web2),
			worker: authz.NewUSet(worker1, worker2),
		},
	}

	scenarios := []struct {
		name              string
		c                 authz.ClientID
		cfg               Config
		expectedUpstreams authz.USet
		expectedErr       error
	}{
		{
			name:              "zero alice query",
			c:                 alice,
			cfg:               cfgZero,
			expectedUpstreams: authz.EmptyUSet(),
			expectedErr:       nil,
		},
		{
			name:              "small alice query",
			c:                 alice,
			cfg:               cfgSmall,
			expectedUpstreams: authz.NewUSet(web1, web2, worker1, worker2),
			expectedErr:       nil,
		},
		{
			name:              "small bob query",
			c:                 bob,
			cfg:               cfgSmall,
			expectedUpstreams: authz.NewUSet(web1, web2, worker1, worker2),
			expectedErr:       nil,
		},
		{
			name:              "small cindy query",
			c:                 cindy,
			cfg:               cfgSmall,
			expectedUpstreams: authz.NewUSet(worker1, worker2),
			expectedErr:       nil,
		},
		{
			name:              "small dinesh query",
			c:                 dinesh,
			cfg:               cfgSmall,
			expectedUpstreams: authz.NewUSet(web1, web2),
			expectedErr:       nil,
		},
		{
			name:              "small eve query",
			c:                 eve,
			cfg:               cfgSmall,
			expectedUpstreams: authz.EmptyUSet(),
			expectedErr:       nil,
		},
	}

	for _, s := range scenarios {
		t.Run(s.name, func(t *testing.T) {
			a := NewStaticAuthorizer(s.cfg)

			ctx := context.Background()
			upstreams, err := a.AuthorizedUpstreams(ctx, s.c)

			require.Equal(t, s.expectedErr, err)
			require.Equal(t, s.expectedUpstreams, upstreams)
		})
	}
}