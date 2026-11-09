package observability

import (
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"hello/internal/fcm"
)

type provider struct {
	logical  *prometheus.CounterVec
	physical *prometheus.CounterVec
	duration *prometheus.HistogramVec
	inflight prometheus.Gauge
	deferred *prometheus.CounterVec
}

// Provider registers one delivery worker's transport metrics at composition time.
func (s *State) Provider() fcm.Observer {
	p := &provider{
		logical:  prometheus.NewCounterVec(prometheus.CounterOpts{Name: "hello_provider_results_total", Help: "Completed one-use permits by normalized outcome, including preflight failures."}, []string{"disposition", "reason"}),
		physical: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "hello_provider_requests_total", Help: "Completed base HTTP transport calls by response status class, or none when unavailable."}, []string{"status_class"}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "hello_provider_request_duration_seconds", Help: "Time to final logical outcome or to physical response headers.", Buckets: []float64{.01, .05, .1, .5, 1, 5, 10, 30}}, []string{"operation"}),
		inflight: prometheus.NewGauge(prometheus.GaugeOpts{Name: "hello_provider_inflight", Help: "Physical provider HTTP requests awaiting response headers."}),
		deferred: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "hello_provider_permit_deferrals_total", Help: "Pre-claim provider permit deferrals by local gate."}, []string{"reason"}),
	}
	// A first isolated configuration failure must have a zero baseline for increase().
	for _, pair := range []struct {
		d fcm.Disposition
		r fcm.Reason
	}{
		{fcm.NotSubmitted, fcm.CredentialFailure}, {fcm.Rejected, fcm.CredentialFailure},
		{fcm.Rejected, fcm.APNsCredentials}, {fcm.Rejected, fcm.SenderMismatch},
	} {
		p.logical.WithLabelValues(string(pair.d), string(pair.r))
	}
	for _, reason := range []fcm.DeferralReason{fcm.DeferralQuota, fcm.DeferralBreaker, fcm.DeferralConcurrency} {
		p.deferred.WithLabelValues(string(reason))
	}
	s.registry.MustRegister(p.logical, p.physical, p.duration, p.inflight, p.deferred)
	return p
}

// ObserveProviderAdmission exposes local transport state for the scoped worker.
func (s *State) ObserveProviderAdmission(client *fcm.Client) {
	s.registry.MustRegister(
		prometheus.NewGaugeFunc(prometheus.GaugeOpts{Name: "hello_provider_breaker_open", Help: "One when the local provider circuit awaits a successful probe."}, func() float64 { return gauge(client.BreakerOpen()) }),
		prometheus.NewGaugeFunc(prometheus.GaugeOpts{Name: "hello_provider_quota_cooldown", Help: "One when the local client is honoring a provider quota hold."}, func() float64 { return gauge(client.QuotaCooldown()) }),
	)
}

func (p *provider) PermitDeferred(reason fcm.DeferralReason) {
	p.deferred.WithLabelValues(string(reason)).Inc()
}
func (p *provider) LogicalResult(disposition fcm.Disposition, reason fcm.Reason, duration time.Duration) {
	p.logical.WithLabelValues(string(disposition), string(reason)).Inc()
	p.duration.WithLabelValues("logical").Observe(duration.Seconds())
}
func (p *provider) PhysicalStart() { p.inflight.Inc() }
func (p *provider) PhysicalFinish(status int, duration time.Duration) {
	p.inflight.Dec()
	class := "none"
	if status >= 100 && status <= 599 {
		class = strconv.Itoa(status/100) + "xx"
	}
	p.physical.WithLabelValues(class).Inc()
	p.duration.WithLabelValues("physical").Observe(duration.Seconds())
}
