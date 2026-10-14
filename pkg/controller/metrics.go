// Package controller - per-site sync metrics
package controller

import (
	"context"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"k8s.io/apimachinery/pkg/api/meta"
	"sigs.k8s.io/controller-runtime/pkg/client"

	pagesv1 "github.com/kup6s/pages/pkg/apis/v1beta1"
)

var (
	siteSyncedDesc = prometheus.NewDesc("pages_site_synced",
		"1 when the site's Synced condition is True, 0 otherwise.",
		[]string{"namespace", "name"}, nil)
	siteLastSyncDesc = prometheus.NewDesc("pages_site_last_sync_timestamp_seconds",
		"Unix time of the last successful sync of the site.",
		[]string{"namespace", "name"}, nil)
)

// SiteMetricsCollector reports sync state per StaticSite at scrape time. It
// reads from the operator's cache, so the values survive syncer crashes and
// deleted sites leave no series behind.
type SiteMetricsCollector struct {
	Reader client.Reader
}

// Describe implements prometheus.Collector.
func (c *SiteMetricsCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- siteSyncedDesc
	ch <- siteLastSyncDesc
}

// Collect implements prometheus.Collector.
func (c *SiteMetricsCollector) Collect(ch chan<- prometheus.Metric) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var sites pagesv1.StaticSiteList
	if err := c.Reader.List(ctx, &sites); err != nil {
		ch <- prometheus.NewInvalidMetric(siteSyncedDesc, err)
		return
	}
	for _, site := range sites.Items {
		synced := 0.0
		if meta.IsStatusConditionTrue(site.Status.Conditions, pagesv1.ConditionSynced) {
			synced = 1
		}
		ch <- prometheus.MustNewConstMetric(siteSyncedDesc, prometheus.GaugeValue, synced, site.Namespace, site.Name)
		if site.Status.LastSync != nil {
			ch <- prometheus.MustNewConstMetric(siteLastSyncDesc, prometheus.GaugeValue,
				float64(site.Status.LastSync.Unix()), site.Namespace, site.Name)
		}
	}
}
