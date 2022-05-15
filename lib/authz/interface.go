package authz

import (
	"context"
)

// ClientID represents the identity of an authenticated client.
// Implementations must have value semantics and support the
// comparison operators (==, !=).
type ClientID interface {
	// TODO add required methods, or IsClientID, or switch to concrete type

	// TODO this is such a core type, move it into its own package.
}

// Upstream represents an upstream that clients can be forwarded to.
// Implementations must support comparison
// operators (==, !=) and have value semantics.
type Upstream interface {
	Name() string // name of the upstream (unique amongst all upstreams)
}

// USet represents a set of Upstreams
type USet map[Upstream]struct{}

// EmptyUSet returns a new Upstream set containing no Upstreams.
func EmptyUSet() USet {
	return make(map[Upstream]struct{})
}

// NewUSet returns a new Upstream set containing the given Upstreams.
func NewUSet(upstreams ...Upstream) USet {
	result := EmptyUSet()
	for _, u := range upstreams {
		result[u] = struct{}{}
	}
	return result
}

// Union returns a new Upstream set that is the union of the input Sets
func Union(lhs, rhs USet) USet {
	result := EmptyUSet()
	for k, _ := range lhs {
		result[k] = struct{}{}
	}
	for k, _ := range rhs {
		result[k] = struct{}{}
	}
	return result
}

// UnionUpdate updates the input acc USet in-place by taking the union
// with the given rhs USet. The modified input acc is returned.
func UnionUpdate(acc, rhs USet) USet {
	for k, _ := range rhs {
		acc[k] = struct{}{}
	}
	return acc
}

// TODO add USet Intersection and IntersectionUpdate

// TODO add USet Difference and DifferenceUpdate

// ForwardingAuthorizer is a generic forwarding authorization policy that
// controls which clients are allowed to forward connections to which upstreams.
//
// Multiple goroutines may invoke methods on a ForwardingAuthorizer simultaneously.
type ForwardingAuthorizer interface {

	// AuthorizedUpstreams returns an USet of upstreams that the ClientID c
	// is authorized to access. If c is not authorized to access any upstreams,
	// implementations should return an empty USet and nil. If an implementation
	// depends on external or long-running operations to evaluate the authorized upstreams,
	// it should honour cancellations and deadlines through the given ctx.
	AuthorizedUpstreams(ctx context.Context, c ClientID) (USet, error)
}
