package controller

import (
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	pagesv1 "github.com/kup6s/pages/pkg/apis/v1beta1"
)

func TestSiteMetricsCollector(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = pagesv1.AddToScheme(scheme)
	lastSync := metav1.NewTime(time.Unix(1760361600, 0))
	site := func(name string, status metav1.ConditionStatus, synced *metav1.Time) *pagesv1.StaticSite {
		s := &pagesv1.StaticSite{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "team"}}
		if status != "" {
			s.Status.Conditions = []metav1.Condition{{Type: pagesv1.ConditionSynced, Status: status, Reason: "x", LastTransitionTime: lastSync}}
		}
		s.Status.LastSync = synced
		return s
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
		site("ok", metav1.ConditionTrue, &lastSync),
		site("failing", metav1.ConditionFalse, &lastSync),
		site("syncing", metav1.ConditionUnknown, nil),
		site("new", "", nil),
	).Build()

	expected := `
# HELP pages_site_last_sync_timestamp_seconds Unix time of the last successful sync of the site.
# TYPE pages_site_last_sync_timestamp_seconds gauge
pages_site_last_sync_timestamp_seconds{name="failing",namespace="team"} 1.7603616e+09
pages_site_last_sync_timestamp_seconds{name="ok",namespace="team"} 1.7603616e+09
# HELP pages_site_synced 1 when the site's Synced condition is True, 0 otherwise.
# TYPE pages_site_synced gauge
pages_site_synced{name="failing",namespace="team"} 0
pages_site_synced{name="new",namespace="team"} 0
pages_site_synced{name="ok",namespace="team"} 1
pages_site_synced{name="syncing",namespace="team"} 0
`
	if err := testutil.CollectAndCompare(&SiteMetricsCollector{Reader: c}, strings.NewReader(expected)); err != nil {
		t.Error(err)
	}
}
