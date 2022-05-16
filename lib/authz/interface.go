package authz

import (
	"context"
	"tcplb/lib/core"
)

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
	AuthorizedUpstreams(ctx context.Context, c core.ClientID) (core.USet, error)
}
