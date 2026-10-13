// Package syncer - Prometheus metrics
package syncer

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"

	pagesv1 "github.com/kup6s/pages/pkg/apis/v1beta1"
)

// Metrics are the syncer's Prometheus metrics. A nil *Metrics records nothing.
type Metrics struct {
	syncTotal   *prometheus.CounterVec
	gitDuration *prometheus.HistogramVec
	volumeFree  prometheus.Gauge
	volumeSize  prometheus.Gauge
}

// NewMetrics creates the syncer metrics and registers them with reg.
func NewMetrics(reg prometheus.Registerer) *Metrics {
	m := &Metrics{
		syncTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "pages_syncer_sync_total",
			Help: "Site syncs by result and Synced condition reason.",
		}, []string{"result", "reason"}),
		gitDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "pages_syncer_git_duration_seconds",
			Help:    "Duration of git clone and update operations.",
			Buckets: []float64{0.1, 0.5, 1, 5, 15, 60, 180, 600},
		}, []string{"op"}),
		volumeFree: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "pages_syncer_volume_free_bytes",
			Help: "Free bytes on the sites volume.",
		}),
		volumeSize: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "pages_syncer_volume_size_bytes",
			Help: "Size of the sites volume in bytes.",
		}),
	}
	reg.MustRegister(m.syncTotal, m.gitDuration, m.volumeFree, m.volumeSize)
	return m
}

func (m *Metrics) observeSync(err error) {
	if m == nil {
		return
	}
	if err != nil {
		m.syncTotal.WithLabelValues("error", syncReason(err)).Inc()
		return
	}
	m.syncTotal.WithLabelValues("success", pagesv1.SyncReasonSynced).Inc()
}

func (m *Metrics) observeGit(op string, start time.Time) {
	if m == nil {
		return
	}
	m.gitDuration.WithLabelValues(op).Observe(time.Since(start).Seconds())
}

func (m *Metrics) setVolume(free, total uint64) {
	if m == nil {
		return
	}
	m.volumeFree.Set(float64(free))
	m.volumeSize.Set(float64(total))
}
