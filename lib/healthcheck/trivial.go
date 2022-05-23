package healthcheck

import (
	"tcplb/lib/core"
)

// AlwaysHealthyChecker is a trivial health tracker that reports all upstreams are healthy.
type AlwaysHealthyChecker struct{}

func (hc AlwaysHealthyChecker) HealthyUpstreams(candidates core.UpstreamSet) core.UpstreamSet {
	return candidates
}

func (hc AlwaysHealthyChecker) ReportUpstreamHealth(report *HealthReport) {
}

// type check *AlwaysHealthyChecker satisfies HealthReportSink interface
var _ HealthReportSink = (*BeliefHealthTracker)(nil)
