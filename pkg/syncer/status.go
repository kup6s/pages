// Package syncer - Synced condition and sync events
package syncer

import (
	"context"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/log"

	pagesv1 "github.com/kup6s/pages/pkg/apis/v1beta1"
)

const maxConditionMessage = 1024

// reportStatus records a sync result and logs instead of failing the sync.
func (s *Syncer) reportStatus(ctx context.Context, site *staticSiteData, cond metav1.Condition, commit string) {
	if err := s.setSynced(ctx, site, cond, commit); err != nil {
		log.FromContext(ctx).Error(err, "Failed to update status", "site", site.Name)
	}
}

// setSynced writes the Synced condition, and on success lastSync and
// lastCommit. It reads the current object and writes the whole status back
// with optimistic concurrency, so conditions owned by the controller stay.
// A failing site keeps Synced=False while a retry clones.
func (s *Syncer) setSynced(ctx context.Context, site *staticSiteData, cond metav1.Condition, commit string) error {
	sites := s.DynamicClient.Resource(staticSiteGVR).Namespace(site.Namespace)
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		obj, err := sites.Get(ctx, site.Name, metav1.GetOptions{})
		if err != nil {
			return err
		}
		conditions, err := readConditions(obj)
		if err != nil {
			return err
		}
		previous := meta.FindStatusCondition(conditions, pagesv1.ConditionSynced)
		wasFailing := previous != nil && previous.Status == metav1.ConditionFalse
		if cond.Status == metav1.ConditionUnknown && wasFailing {
			return nil
		}

		cond.Type = pagesv1.ConditionSynced
		cond.ObservedGeneration = obj.GetGeneration()
		cond.Message = truncate(cond.Message, maxConditionMessage)
		meta.SetStatusCondition(&conditions, cond)
		if err := writeConditions(obj, conditions); err != nil {
			return err
		}
		if cond.Status == metav1.ConditionTrue {
			if err := unstructured.SetNestedField(obj.Object, time.Now().UTC().Format(time.RFC3339), "status", "lastSync"); err != nil {
				return err
			}
			if err := unstructured.SetNestedField(obj.Object, commit, "status", "lastCommit"); err != nil {
				return err
			}
		}
		if _, err := sites.UpdateStatus(ctx, obj, metav1.UpdateOptions{}); err != nil {
			return err
		}
		s.recordTransition(obj, wasFailing, cond, commit)
		return nil
	})
}

// recordTransition emits an event when a site starts failing or recovers.
func (s *Syncer) recordTransition(obj runtime.Object, wasFailing bool, cond metav1.Condition, commit string) {
	if s.Recorder == nil {
		return
	}
	switch {
	case cond.Status == metav1.ConditionFalse && !wasFailing:
		s.Recorder.Eventf(obj, nil, corev1.EventTypeWarning, "SyncFailed", "Sync", "%s: %s", cond.Reason, cond.Message)
	case cond.Status == metav1.ConditionTrue && wasFailing:
		s.Recorder.Eventf(obj, nil, corev1.EventTypeNormal, "SyncRecovered", "Sync", "Synced %s", commit)
	}
}

func readConditions(obj *unstructured.Unstructured) ([]metav1.Condition, error) {
	raw, _, err := unstructured.NestedSlice(obj.Object, "status", "conditions")
	if err != nil {
		return nil, err
	}
	conditions := make([]metav1.Condition, 0, len(raw))
	for _, item := range raw {
		m, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		var c metav1.Condition
		if err := runtime.DefaultUnstructuredConverter.FromUnstructured(m, &c); err != nil {
			return nil, err
		}
		conditions = append(conditions, c)
	}
	return conditions, nil
}

func writeConditions(obj *unstructured.Unstructured, conditions []metav1.Condition) error {
	raw := make([]interface{}, 0, len(conditions))
	for i := range conditions {
		m, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&conditions[i])
		if err != nil {
			return err
		}
		raw = append(raw, m)
	}
	return unstructured.SetNestedSlice(obj.Object, raw, "status", "conditions")
}
