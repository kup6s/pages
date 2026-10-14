package syncer

import (
	"context"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	pagesv1 "github.com/kup6s/pages/pkg/apis/v1beta1"
)

func TestMetrics_CountSyncResults(t *testing.T) {
	r := newTestRemote(t, archiveFiles, remoteOptions{})
	s := newTestSyncer(t, r)
	s.Metrics = NewMetrics(prometheus.NewRegistry())

	mustSync(t, s, testSite(r, "/2018"))
	broken := testSite(r, "/2018")
	broken.Branch = "gone"
	broken.Name = "broken"
	_ = s.syncSite(context.Background(), broken)

	if got := testutil.ToFloat64(s.Metrics.syncTotal.WithLabelValues("success", pagesv1.SyncReasonSynced)); got != 1 {
		t.Errorf("success count = %v, want 1", got)
	}
	if got := testutil.ToFloat64(s.Metrics.syncTotal.WithLabelValues("error", pagesv1.SyncReasonCloneFailed)); got != 1 {
		t.Errorf("CloneFailed count = %v, want 1", got)
	}
	if got := testutil.CollectAndCount(s.Metrics.gitDuration); got != 1 {
		t.Errorf("git duration series = %d, want 1 (clone)", got)
	}
}

func TestMetrics_VolumeGauges(t *testing.T) {
	stubFreeSpace(t, 40)
	s := &Syncer{SitesRoot: "/sites", Metrics: NewMetrics(prometheus.NewRegistry())}

	_ = s.checkFreeSpace()

	if got := testutil.ToFloat64(s.Metrics.volumeFree); got != float64(uint64(100<<30)/100*40) {
		t.Errorf("free bytes = %v", got)
	}
	if got := testutil.ToFloat64(s.Metrics.volumeSize); got != float64(100<<30) {
		t.Errorf("size bytes = %v", got)
	}
}

func TestMetrics_NilIsSafe(t *testing.T) {
	var m *Metrics
	m.observeSync(nil)
	m.setVolume(1, 2)
}
