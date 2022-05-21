package forwarder

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	liberrors "tcplb/lib/errors"
	"tcplb/lib/slog"
	"time"
)

var IdleTimeoutErr = errors.New("idle timeout")

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

// worker is responsible for copying data from Src to Dst. The work of
// copying is chunked into tasks. Tasks are defined implicitly through
// deadlines on channels by the supervisor. A worker does not
// proactively process tasks. Processing only occurs in reaction to a
// call to releaseTask().
//
// Multiple goroutines must not invoke methods on a worker simultaneously.
type worker struct {
	Out           chan taskResult
	In            chan struct{}
	WorkRemaining bool
	TaskDone      bool
	Src           DuplexConn
	Dst           DuplexConn
	SrcLabel      string
	DstLabel      string
}

func newWorker(srcLabel string, src DuplexConn, dstLabel string, dst DuplexConn) *worker {
	return &worker{
		Out:           make(chan taskResult, 1),
		In:            make(chan struct{}, 1),
		WorkRemaining: true,
		TaskDone:      false,
		Src:           src,
		Dst:           dst,
		SrcLabel:      srcLabel,
		DstLabel:      dstLabel,
	}
}

func (w *worker) start() {
	go func(dst, src DuplexConn, in <-chan struct{}, out chan<- taskResult) {
		for range in {
			// Some dst conn types such as *net.TCPConn have a ReadFrom method,
			// which Copy will use to avoid allocating a work buffer.
			written, err := io.Copy(dst, src)
			out <- taskResult{written: written, err: err}
		}
	}(w.Src, w.Dst, w.In, w.Out)
}

func (w *worker) stop() {
	close(w.In)
}

func (w *worker) releaseTask() {
	if w.WorkRemaining {
		w.TaskDone = false
		w.In <- struct{}{}
	} else {
		w.TaskDone = true
	}
}

func (w *worker) checkTaskResult(result taskResult) (err error) {
	w.TaskDone = true
	if result.err == nil {
		// EOF. Tell Dst not to expect any more bytes of data.
		w.WorkRemaining = false
		if cwErr := w.Dst.CloseWrite(); cwErr != nil {
			msg := fmt.Sprintf("unable to close-write %s conn", w.DstLabel)
			err = &CopyFailure{Msg: msg, Cause: cwErr}
		}
	} else if !errors.Is(result.err, os.ErrDeadlineExceeded) {
		msg := fmt.Sprintf("%s->%s copy error", w.SrcLabel, w.DstLabel)
		err = &CopyFailure{Msg: msg, Cause: result.err}
	}
	// Neither EOF nor deadline expiry are reported to caller as errors.
	return
}

// ForwardingSupervisor robustly forwards data between client DuplexConn and upstream DuplexConn.
type ForwardingSupervisor struct {
	IdleTimeout time.Duration // IdleTimeout enforces an application data idle timeout.
	Logger      slog.Logger
}

// Forward copies data between client DuplexConn and upstream DuplexConn.
//
// The caller is responsible for closing both connections after calling Forward, in both
// error and non-error cases. If the caller does not close both connections, then resources
// may not be released in some error scenarios.
func (s *ForwardingSupervisor) Forward(ctx context.Context, clientConn, upstreamConn DuplexConn) error {
	// Failures can be caused by a variety of reasons:
	// - the input context tells us to stop doing work
	// - we experience an error when attempting to set a connection deadline
	// - a duration of IdleTimeout passes without any bytes of application data being copied
	// - a copy operation returns an error (not deadline expiry)
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
	defer cuWorker.stop()
	ucWorker.start()
	defer ucWorker.stop()

	// Each outer loop cycle corresponds to one IdleTimeout period. We release one task of
	// work to both workers per cycle. The workers are kept in lockstep with aligned task
	// periods to ease detection of the idle timeout condition.
	for cuWorker.WorkRemaining || ucWorker.WorkRemaining {
		setConnDeadlines(time.Now().Add(s.IdleTimeout))
		if hasFailed() {
			break
		}

		var bytesWrittenThisPeriod int64 = 0
		cuWorker.releaseTask()
		ucWorker.releaseTask()

		for !hasFailed() && (!cuWorker.TaskDone || !ucWorker.TaskDone) {
			select {
			case <-ctx.Done():
				fail(&CopyFailure{Msg: "terminated by context", Cause: ctx.Err()})
			case cuResult := <-cuWorker.Out:
				bytesWrittenThisPeriod += cuResult.written
				if err := cuWorker.checkTaskResult(cuResult); err != nil {
					fail(err)
				}
			case ucResult := <-ucWorker.Out:
				bytesWrittenThisPeriod += ucResult.written
				if err := ucWorker.checkTaskResult(ucResult); err != nil {
					fail(err)
				}
			}
		}
		if hasFailed() || (!cuWorker.WorkRemaining && !ucWorker.WorkRemaining) {
			break
		}
		if bytesWrittenThisPeriod == 0 {
			fail(&CopyFailure{Msg: "no data copied", Cause: IdleTimeoutErr})
			break
		}
		// Both copy workers completed this period's task without failure. Next period.
	}

	// Terminal state: either forwarding completed, or there was a failure.
	if hasFailed() {
		// Depending on the failure mode, one or both workers may still be blocking
		// on IO operations. Set immediate deadlines to force IO operations to stop.
		setConnDeadlines(time.Now())
	}

	if hasFailed() {
		return &liberrors.AggregateError{Errors: failures}
	}

	return nil
}
