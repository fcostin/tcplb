package static

import (
	"context"
	"fmt"
	"tcplb/lib/core"
)

// Group is a value type that represents a logical group of clients.
type Group struct {
	key string
}

// String returns a string representation of the Group
func (g Group) String() string {
	return fmt.Sprintf("<Group %s>", g.key)
}

// NewGroup returns a logical client group with the given name.
func NewGroup(groupName string) Group {
	return Group{key: groupName}
}

// UGroup is a value type that represents a logical group of upstreams.
type UGroup struct {
	key string
}

// String returns a string representation of the UGroup
func (g UGroup) String() string {
	return fmt.Sprintf("<UGroup %s>", g.key)
}

// NewUGroup returns a logical upstream group with the given name.
func NewUGroup(uGroupName string) UGroup {
	return UGroup{key: uGroupName}
}

// Config defines the authorization data
// required by an Authorizer.
type Config struct {
	GroupsByClientID  map[core.ClientID][]Group
	UGroupsByGroup    map[Group][]UGroup
	UpstreamsByUGroup map[UGroup]core.USet
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

func (a *Authorizer) AuthorizedUpstreams(ctx context.Context, c core.ClientID) (core.USet, error) {
	result := core.EmptyUSet()
	_, exists := a.config.GroupsByClientID[c]
	if !exists {
		return result, nil
	}
	for _, g := range a.config.GroupsByClientID[c] {
		_, exists := a.config.UGroupsByGroup[g]
		if !exists {
			continue
		}
		for _, ug := range a.config.UGroupsByGroup[g] {
			us, exists := a.config.UpstreamsByUGroup[ug]
			if !exists {
				continue
			}
			result = core.UnionUpdate(result, us)
		}
	}
	return result, nil
}
