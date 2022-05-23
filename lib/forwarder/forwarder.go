package forwarder

import (
	"context"
	"io"
	"sync"
	liberrors "tcplb/lib/errors"
)

// MediocreForwarder is a implementation of the Forward operation.
// This is a placeholder implementation that lacks robustness.
type MediocreForwarder struct{}

func (f MediocreForwarder) Forward(ctx context.Context, clientConn, upstreamConn DuplexConn) error {
	// Caller is responsible for closing both DuplexConns, not us.
	out := make(chan error, 4)
	wg := sync.WaitGroup{}

	copy := func(dst, src DuplexConn, out chan<- error) {
		defer wg.Done()
		// TODO FIXME add an idle timeout here that detects if neither
		// of the two directions of copying have made any progress in
		// some time window.
		// TODO FIXME also honour cancellation by ctx
		_, err := io.Copy(dst, src)
		cwErr := dst.CloseWrite() // Inform peer at dst end that we're done writing.
		out <- err
		out <- cwErr
	}

	wg.Add(1)
	go copy(upstreamConn, clientConn, out)
	wg.Add(1)
	go copy(clientConn, upstreamConn, out)

	// Note that if upstream and client keep talking to each other without ever
	// closing their connection, we may block here forever, while one or both
	// goroutines copy application data. This is a feature, as this server is
	// doing useful work.
	wg.Wait()
	close(out)

	return liberrors.AggregateErrorFromChannel(out)
}
