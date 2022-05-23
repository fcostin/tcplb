package forwarder

import (
	"context"
	"fmt"
	"io"
	liberrors "tcplb/lib/errors"
	"tcplb/lib/slog"
	"time"
)

type CopyFailure struct {
	Msg   string
	Cause error
}

func (f *CopyFailure) Error() string {
	return fmt.Sprintf("CopyFailure: %s; cause %s", f.Msg, f.Cause)
}

type taskResult struct {
	written int64
	err     error
}

// worker is responsible for copying data from Src to Dst.
//
// Multiple goroutines must not invoke methods on a worker simultaneously.
type worker struct {
	Out           chan taskResult
	WorkRemaining bool
	Src           DuplexConn
	Dst           DuplexConn
	SrcLabel      string
	DstLabel      string
}

func newWorker(srcLabel string, src DuplexConn, dstLabel string, dst DuplexConn) *worker {
	return &worker{
		Out:           make(chan taskResult, 1),
		WorkRemaining: true,
		Src:           src,
		Dst:           dst,
		SrcLabel:      srcLabel,
		DstLabel:      dstLabel,
	}
}

func (w *worker) start() {
	go func(dst, src DuplexConn, out chan<- taskResult) {
		// Some dst conn types such as *net.TCPConn have a ReadFrom method,
		// which Copy will use to avoid allocating a work buffer.
		written, err := io.Copy(dst, src)
		out <- taskResult{written: written, err: err}
	}(w.Src, w.Dst, w.Out)
}

func (w *worker) checkTaskResult(result taskResult) (err error) {
	if result.err == nil {
		// EOF. Tell Dst not to expect any more bytes of data.
		w.WorkRemaining = false
		if cwErr := w.Dst.CloseWrite(); cwErr != nil {
			msg := fmt.Sprintf("unable to close-write %s conn", w.DstLabel)
			err = &CopyFailure{Msg: msg, Cause: cwErr}
		}
	} else {
		msg := fmt.Sprintf("%s->%s copy error", w.SrcLabel, w.DstLabel)
		err = &CopyFailure{Msg: msg, Cause: result.err}
	}
	return
}

// ForwardingSupervisor robustly forwards data between client DuplexConn and upstream DuplexConn.
type ForwardingSupervisor struct {
	Logger            slog.Logger
	ForwardingTimeout time.Duration
}

// Forward copies data between client DuplexConn and upstream DuplexConn.
//
// The caller is responsible for closing both connections after Forward returns, in both
// error and non-error cases. If the caller does not close both connections, then resources
// may not be released in some error scenarios.
func (s *ForwardingSupervisor) Forward(ctx context.Context, clientConn, upstreamConn DuplexConn) error {
	var fwdCtx context.Context
	var fwdCtxCancel context.CancelFunc

	if s.ForwardingTimeout > 0 {
		// Set hard timeout on this operation. Forwarded connections will be cancelled
		// when this expires, even if they are still perfoming useful work.  This may
		// be undesirable for some application use cases (e.g. streaming),
		// but we currently don't implement an idle timeout.
		// TODO reimplement idle timeout in TLSConn friendly way -- need some way to
		// count copied bytes without letting a TLSConn WriteDeadline expire.
		fwdCtx, fwdCtxCancel = context.WithTimeout(ctx, s.ForwardingTimeout)
		defer fwdCtxCancel()
	} else {
		fwdCtx = ctx
	}

	// Failures can be caused by a variety of reasons:
	// - the input context tells us to stop doing work
	// - we experience an error when attempting to set a connection deadline
	// - a copy operation returns an error
	// - we fail to close the write-side of a dst connection when src reports EOF
	failures := make([]error, 0)

	fail := func(e error) {
		failures = append(failures, e)
		s.Logger.Error(&slog.LogRecord{Msg: "Forwarding failure", Error: e})
	}

	hasFailed := func() bool {
		return len(failures) > 0
	}

	setConnDeadlines := func(deadline time.Time) {
		// Beware: if one or both of our conns is a *tls.Conn, then it only
		// supports setting the write deadline at most once:
		//
		// > After a Write has timed out, the TLS state is corrupt and all
		// > future writes will return the same error.
		// ref: https://pkg.go.dev/crypto/tls@go1.18.1#Conn.SetDeadline
		//
		// Therefore we can only use this conn SetDeadline to terminate,
		// we cannot use it to get IO to stop periodically. If we could call
		// SetDeadline repeatedly, then we could use it to collect statistics
		// from workers on how many bytes had been copied in some time period -
		// to detect if the connection was idling or not, and then if the
		// connection was deemed to be live, setting new deadlines.
		if cdErr := clientConn.SetDeadline(deadline); cdErr != nil {
			fail(&CopyFailure{Msg: "unable to set client conn deadline", Cause: cdErr})
		}
		if udErr := upstreamConn.SetDeadline(deadline); udErr != nil {
			fail(&CopyFailure{Msg: "unable to set upstream conn deadline", Cause: udErr})
		}
	}

	// "cu" denotes Client->Upstream, "uc" denotes Upstream->Client.
	cuWorker := newWorker("client", clientConn, "upstream", upstreamConn)
	ucWorker := newWorker("upstream", upstreamConn, "client", clientConn)

	cuWorker.start()
	ucWorker.start()

	defer func() {
		// When the below for block exits, either both workers completed forwarding,
		// or there was a failure. In the latter case, workers may still be blocking
		// on IO operations. Set immediate deadlines to force IO operations to stop.
		setConnDeadlines(time.Now())
	}()

	for !hasFailed() && (cuWorker.WorkRemaining || ucWorker.WorkRemaining) {
		select {
		case <-fwdCtx.Done():
			fail(&CopyFailure{Msg: "terminated by context", Cause: fwdCtx.Err()})
		case cuResult := <-cuWorker.Out:
			if err := cuWorker.checkTaskResult(cuResult); err != nil {
				fail(err)
			}
		case ucResult := <-ucWorker.Out:
			if err := ucWorker.checkTaskResult(ucResult); err != nil {
				fail(err)
			}
		}
	}

	if hasFailed() {
		return &liberrors.AggregateError{Errors: failures}
	}

	return nil
}
