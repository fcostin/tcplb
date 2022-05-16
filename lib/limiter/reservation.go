package limiter

import (
	"context"
	"errors"
	"sync"
	"tcplb/lib/core"
)

type ClientReservation struct {
	c core.ClientID
}

// MaxReservationsExceeded is the error returned by UniformlyBoundedClientReserver
// when an attempted reservation fails because the client has too  many reservations.
var MaxReservationsExceeded = errors.New("maximum client reservations exceeded")

// UnboundedClientReserver is a ClientReserver where all clients are free
// to acquire arbitrarily many reservations without constraint.
type UnboundedClientReserver struct{}

func (u UnboundedClientReserver) TryReserve(ctx context.Context, c core.ClientID) (ClientReservation, error) {
	return ClientReservation{c: c}, nil
}

func (u UnboundedClientReserver) ReleaseReservation(ctx context.Context, r ClientReservation) error {
	return nil
}

// UniformlyBoundedClientReserver is a ClientReserver where all clients are
// subject to a uniform maximum limit on the number of reservations they can
// acquire at once.
//
// Multiple goroutines may invoke methods on a UniformlyBoundedClientReserver
// simultaneously.
type UniformlyBoundedClientReserver struct {
	MaxReservationsPerClient int64

	// TODO consider also adding MaxConcurrentClients to bound amount of memory that
	// resByClient map can consume. This could return a "reservations overloaded" error
	// to signal to the caller that reservation system is currently overloaded.

	// mu guards resByClient.
	//
	// TODO investigate fine-grain locking per ClientID to reduce lock contention.
	// One naive idea is to use a two-level hierarchy of reservers where the top-level
	// reserver hashes by ClientID to figure out which sub-reserver to delegate to.
	// If the top-level reserver used a pre-specified pre-allocated fixed number of
	// sub-reservers then that avoids the need to modify (and hence lock) the top
	// level reserver at all, at the cost of creating a new parameter (fixed number
	// of sub reservers) to tune to usage patterns.
	//
	// See also: https://github.com/golang/go/issues/21035
	// See also: https://pkg.go.dev/crypto/sha256
	mu          sync.Mutex
	resByClient map[core.ClientID]int64
}

func NewUniformlyBoundedClientReserver(n int64) *UniformlyBoundedClientReserver {
	return &UniformlyBoundedClientReserver{
		MaxReservationsPerClient: n,
		resByClient:              make(map[core.ClientID]int64),
	}
}

func (b *UniformlyBoundedClientReserver) sanityCheck(n int64) {
	// assert invariant 0 <= n <= MaxReservationsPerClient
	if n < 0 || n > b.MaxReservationsPerClient {
		panic("UniformlyBoundedClientReserver: internal invariant failure")
	}
}

// TryReserve attempts to acquire a reservation for the given client.
// If the attempt succeeds, the reservation is returned with nil error.
// If the attempt fails because the client has exceeded the maximum number
// of reservations, the zero reservation and MaxReservationsExceeded error
// will be returned.
//
// If no reservations are available, this call does not block.
func (b *UniformlyBoundedClientReserver) TryReserve(ctx context.Context, c core.ClientID) (ClientReservation, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := b.resByClient[c]
	b.sanityCheck(n)
	if n >= b.MaxReservationsPerClient {
		return ClientReservation{}, MaxReservationsExceeded
	}
	b.resByClient[c] = n + 1
	b.sanityCheck(n)
	return ClientReservation{c: c}, nil
}

// ReleaseReservation releases a reservation that was previously acquired
// by TryReserve.
func (b *UniformlyBoundedClientReserver) ReleaseReservation(ctx context.Context, r ClientReservation) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := b.resByClient[r.c]
	b.sanityCheck(n)
	n = n - 1
	b.resByClient[r.c] = n
	if n == 0 {
		delete(b.resByClient, r.c)
	}
	b.sanityCheck(n)
	return nil
}
