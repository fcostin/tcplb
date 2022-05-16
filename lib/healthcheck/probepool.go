package healthcheck

import (
	"context"
	"sync"
	"tcplb/lib/core"
	"tcplb/lib/forwarder"
	"time"
)

type HealthCheckResult int8

const (
	CheckFail HealthCheckResult = iota
	CheckSuccess
)

type UpstreamDialer interface {
	DialUpstream(ctx context.Context, u core.Upstream) (forwarder.DuplexConn, error)
}

type TimeoutDialer struct {
	Timeout time.Duration
	Inner   UpstreamDialer
}

func (d TimeoutDialer) DialUpstream(ctx context.Context, u core.Upstream) (forwarder.DuplexConn, error) {
	childCtx, cancel := context.WithTimeout(ctx, d.Timeout)
	defer cancel()
	return d.Inner.DialUpstream(childCtx, u)
}

// HealthReport contains information from a single observation
// of upstream health - perhaps from a successful or failed
// connection attempt, or the result of an active probe.
type HealthReport struct {
	Upstream    core.Upstream
	CheckResult HealthCheckResult
	Symptom     error // Symptom may optionally contain information relating to a failed check
}

// HealthReportSink represents an entity that can be used by the
// ProbePool to receive upstream health reports.
//
// Multiple goroutines may invoke methods on a HealthReportSink
// simultaneously.
type HealthReportSink interface {

	// ReportUpstreamHealth receives a HealthReport.
	ReportUpstreamHealth(report *HealthReport)
}

type ProbePoolConfig struct {
	HealthReportSink HealthReportSink
	ProbePeriod      time.Duration
	Upstreams        core.UpstreamSet
	Dialer           UpstreamDialer
}

// ProbePool probes a set of upstreams on a periodic schedule,
// reporting probe outcomes to a HealthReportSink. To initialise
// a ProbePool, call NewProbePool. To start an initialised
// ProbePool, call Start.
//
// Multiple goroutines may invoke methods on a ProbePool.
type ProbePool struct {
	cfg ProbePoolConfig

	// mu guards state variables below
	mu      sync.Mutex
	started bool
	stopped bool
	done    context.CancelFunc
	wg      sync.WaitGroup
}

// NewProbePool creates a new ProbePool from the given ProbePoolConfig.
func NewProbePool(cfg ProbePoolConfig) *ProbePool {
	return &ProbePool{
		cfg: cfg,
	}
}

// Start starts a ProbePool that has been initialised but not yet
// started. After launching probes, Start will return without
// blocking. Health observations will be reported to the configured
// HealthReportSink asynchronously.
func (ap *ProbePool) Start(ctx context.Context) {
	ap.mu.Lock()
	defer ap.mu.Unlock()

	probeCtx, probeCancel := context.WithCancel(ctx)
	ap.done = probeCancel

	if ap.started {
		return
	}
	ap.started = true
	ap.stopped = false
	for u := range ap.cfg.Upstreams {
		ap.wg.Add(1)
		w := newWorker(workerConfig{
			Upstream:         u,
			Period:           ap.cfg.ProbePeriod,
			HealthReportSink: ap.cfg.HealthReportSink,
			Dialer:           ap.cfg.Dialer,
			WaitGroup:        &ap.wg,
		})
		go w.probeForever(probeCtx)
	}
}

// Stop stops a ProbePool that was previously started.
// Stop cancels probing, and blocks until all probes are stopped.
func (ap *ProbePool) Stop() {
	ap.mu.Lock()
	defer ap.mu.Unlock()

	if !ap.started || ap.stopped {
		return
	}

	ap.started = false
	ap.stopped = true
	ap.done() // cancel parent context for all probe workers
	// wait for all probe workers to stop
	ap.wg.Wait()
}

type workerConfig struct {
	Upstream         core.Upstream
	Period           time.Duration
	HealthReportSink HealthReportSink
	Dialer           UpstreamDialer
	WaitGroup        *sync.WaitGroup
	// TODO add logger to observe what probe Workers do
}

// worker is responsible for actively probing the health of a single
// configured upstream according to a periodic schedule.
type worker struct {
	cfg workerConfig
}

func newWorker(cfg workerConfig) *worker {
	return &worker{
		cfg: cfg,
	}
}

func (w *worker) probeForever(ctx context.Context) {
	defer w.cfg.WaitGroup.Done()

	// TODO could add initial delay to smooth out probe schedule network impact.
	// TODO could add jitter to smooth out probe schedule network impact.

	ticker := time.NewTicker(w.cfg.Period)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// TODO could guard against panic in dialer by trapping panics
			// and logging them or reporting them to the HealthReportSink.

			// The dialer is responsible for setting connect timeout.
			conn, err := w.cfg.Dialer.DialUpstream(ctx, w.cfg.Upstream)
			var report HealthReport
			report.Upstream = w.cfg.Upstream
			if err != nil {
				report.Symptom = err
				report.CheckResult = CheckFail
			} else {
				report.CheckResult = CheckSuccess
				_ = conn.Close()
			}
			w.cfg.HealthReportSink.ReportUpstreamHealth(&report)
		}
	}
}
