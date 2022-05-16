package limiter

import (
	"context"
	"github.com/stretchr/testify/require"
	"sync"
	"tcplb/lib/core"
	"testing"
	"time"
)

type DummyClientID string

func requireAllCountsZero(t *testing.T, r *UniformlyBoundedClientReserver) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for c, m := range r.resByClient {
		require.Equal(t, int64(0), m, c)
	}
}

func TestUniformlyBoundedClientReserverSingleSequentialClient(t *testing.T) {
	// simple scenario of sequential reservation attempts by single client

	var maxReservationsPerClient int64 = 3
	rsvr := NewUniformlyBoundedClientReserver(maxReservationsPerClient)

	alice := DummyClientID("alice")
	ctx := context.Background()

	r1, err := rsvr.TryReserve(ctx, alice)
	require.NoError(t, err)

	r2, err := rsvr.TryReserve(ctx, alice)
	require.NoError(t, err)

	r3, err := rsvr.TryReserve(ctx, alice)
	require.NoError(t, err)

	r4, err := rsvr.TryReserve(ctx, alice)
	require.Equal(t, MaxReservationsExceeded, err)
	require.Equal(t, ClientReservation{}, r4)

	err = rsvr.ReleaseReservation(ctx, r1)
	require.NoError(t, err)

	r5, err := rsvr.TryReserve(ctx, alice)
	require.NoError(t, err)

	err = rsvr.ReleaseReservation(ctx, r3)
	require.NoError(t, err)

	err = rsvr.ReleaseReservation(ctx, r2)
	require.NoError(t, err)

	err = rsvr.ReleaseReservation(ctx, r5)
	require.NoError(t, err)

	requireAllCountsZero(t, rsvr)
}

func TestUniformlyBoundedClientReserverMultipleSequentialClients(t *testing.T) {
	// simple scenario of sequential reservation attempts by two clients

	var maxReservationsPerClient int64 = 2
	rsvr := NewUniformlyBoundedClientReserver(maxReservationsPerClient)

	alice := DummyClientID("alice")
	bob := DummyClientID("bob")
	ctx := context.Background()

	rb1, err := rsvr.TryReserve(ctx, bob)
	require.NoError(t, err)

	rb2, err := rsvr.TryReserve(ctx, bob)
	require.NoError(t, err)

	ra1, err := rsvr.TryReserve(ctx, alice)
	require.NoError(t, err)

	err = rsvr.ReleaseReservation(ctx, rb1)
	require.NoError(t, err)

	ra2, err := rsvr.TryReserve(ctx, alice)
	require.NoError(t, err)

	rb3, err := rsvr.TryReserve(ctx, bob)
	require.NoError(t, err)

	ra3, err := rsvr.TryReserve(ctx, alice)
	require.Equal(t, MaxReservationsExceeded, err)
	require.Equal(t, ClientReservation{}, ra3)

	err = rsvr.ReleaseReservation(ctx, ra2)
	require.NoError(t, err)

	rb4, err := rsvr.TryReserve(ctx, bob)
	require.Equal(t, MaxReservationsExceeded, err)
	require.Equal(t, ClientReservation{}, rb4)

	err = rsvr.ReleaseReservation(ctx, ra1)
	require.NoError(t, err)

	err = rsvr.ReleaseReservation(ctx, rb2)
	require.NoError(t, err)

	err = rsvr.ReleaseReservation(ctx, rb3)
	require.NoError(t, err)

	requireAllCountsZero(t, rsvr)
}

func TestUniformlyBoundedClientReserverConcurrent(t *testing.T) {
	// Scenario of concurrent reservation attempts by two clients.
	// The intent of this test is to potentially identify data races.

	// We start a number of worker threads for two clients. Each worker thread
	// iteratively attempts to reserve, pause, then release a reservation.

	// BEWARE: if the reserver implementation is defective, the behaviour
	// of this test may be nondeterministic as it depends upon goroutine
	// scheduling.

	// create a reserver with some per client limit
	var maxReservationsPerClient int64 = 5
	rsvr := NewUniformlyBoundedClientReserver(maxReservationsPerClient)

	// two clients, alice and bob
	alice := DummyClientID("alice")
	bob := DummyClientID("bob")

	clients := []DummyClientID{alice, bob}

	type workerStats struct {
		Client        core.ClientID
		Reserved      int64
		Limited       int64
		Errors        int64
		ReleaseErrors int64
	}

	// Create twice as many workers per client as the limit
	// init each worker with same max iters and a different seed
	var wg sync.WaitGroup

	workersPerClient := 2 * maxReservationsPerClient
	itersPerWorker := int64(1000)

	stats := make(chan workerStats, int64(len(clients))*workersPerClient)

	worker := func(c core.ClientID, iters int64, rsvr *UniformlyBoundedClientReserver, out chan<- workerStats) {
		defer wg.Done()
		var s workerStats
		s.Client = c
		ctx := context.Background()

		for i := int64(0); i < iters; i++ {
			r, err := rsvr.TryReserve(ctx, c)
			switch err {
			case nil:
				s.Reserved += 1
			case MaxReservationsExceeded:
				s.Limited += 1
			default:
				s.Errors += 1
			}

			time.Sleep(time.Microsecond)

			if err != nil {
				continue
			}
			err = rsvr.ReleaseReservation(ctx, r)
			if err != nil {
				s.ReleaseErrors += 1
			}
		}
		out <- s
	}

	for _, c := range clients {
		for i := int64(0); i < workersPerClient; i += 1 {
			wg.Add(1)
			go worker(c, itersPerWorker, rsvr, stats)
		}
	}

	wg.Wait()

	// aggregate per client stats
	close(stats)
	aggStatsByClient := make(map[core.ClientID]*workerStats)
	for _, c := range clients {
		aggStatsByClient[c] = &workerStats{}
	}
	for s := range stats {
		aggStatsByClient[s.Client].Reserved += s.Reserved
		aggStatsByClient[s.Client].Limited += s.Limited
		aggStatsByClient[s.Client].Errors += s.Errors
	}

	for _, c := range clients {
		// require no errors
		require.Equal(t, int64(0), aggStatsByClient[c].Errors)

		// require each client had expected number of attempts
		expectedAttempts := itersPerWorker * workersPerClient
		require.Equal(t, expectedAttempts, aggStatsByClient[c].Reserved+aggStatsByClient[c].Limited)

		// require each client managed to get some minimal number of reservations.
		var successfulAttemptsLowerBound int64
		if expectedAttempts > maxReservationsPerClient {
			successfulAttemptsLowerBound = maxReservationsPerClient
		} else {
			successfulAttemptsLowerBound = expectedAttempts
		}
		require.LessOrEqual(t, successfulAttemptsLowerBound, aggStatsByClient[c].Reserved)
	}
}
