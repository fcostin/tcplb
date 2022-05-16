package authz

import (
	"context"
	"tcplb/lib/core"
)

// Group is a value type that represents a logical group of clients.
type Group struct {
	Key string // Key is the name of the logical client group
}

// UpstreamGroup is a value type that represents a logical group of upstreams.
type UpstreamGroup struct {
	Key string // Key is the name of the logical upstream group
}

// Config defines the authorization data required by an Authorizer.
type Config struct {
	GroupsByClientID         map[core.ClientID][]Group
	UpstreamGroupsByGroup    map[Group][]UpstreamGroup
	UpstreamsByUpstreamGroup map[UpstreamGroup]core.USet
}

// Authorizer is a static forwarding authorization policy that
// controls which clients are allowed to forward connections to which upstreams.
//
// Authorization data is static and is stored locally in memory.
//
// Multiple goroutines may invoke methods on an Authorizer simultaneously.
type Authorizer struct {
	config Config
}

// NewStaticAuthorizer creates a new static Authorizer from the given config.
func NewStaticAuthorizer(config Config) *Authorizer {
	return &Authorizer{
		config: config,
	}
}

// AuthorizedUpstreams returns an USet of upstreams that the ClientID c
// is authorized to access. If c is not authorized to access any upstreams,
// implementations should return an empty USet and nil.
func (a *Authorizer) AuthorizedUpstreams(ctx context.Context, c core.ClientID) (core.USet, error) {
	result := core.EmptyUSet()
	groups, exists := a.config.GroupsByClientID[c]
	if !exists {
		return result, nil
	}
	for _, g := range groups {
		upstreamGroups, exists := a.config.UpstreamGroupsByGroup[g]
		if !exists {
			continue
		}
		for _, ug := range upstreamGroups {
			us, exists := a.config.UpstreamsByUpstreamGroup[ug]
			if !exists {
				continue
			}
			result = core.UnionUpdate(result, us)
		}
	}
	return result, nil
}
