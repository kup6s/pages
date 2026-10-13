package syncer

import (
	"context"
	"errors"
	"strings"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/events"

	pagesv1 "github.com/kup6s/pages/pkg/apis/v1beta1"
)

func newStatusClient(t *testing.T, conditions ...metav1.Condition) *dynamicfake.FakeDynamicClient {
	t.Helper()
	obj := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "pages.kup6s.com/v1beta1",
		"kind":       "StaticSite",
		"metadata":   map[string]interface{}{"name": "site", "namespace": "default", "generation": int64(3)},
		"spec":       map[string]interface{}{"repo": "https://example.org/r.git"},
	}}
	if len(conditions) > 0 {
		if err := writeConditions(obj, conditions); err != nil {
			t.Fatal(err)
		}
	}
	return dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(),
		map[schema.GroupVersionResource]string{staticSiteGVR: "StaticSiteList"}, obj)
}

func storedConditions(t *testing.T, c *dynamicfake.FakeDynamicClient) ([]metav1.Condition, *unstructured.Unstructured) {
	t.Helper()
	obj, err := c.Resource(staticSiteGVR).Namespace("default").Get(context.Background(), "site", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	conds, err := readConditions(obj)
	if err != nil {
		t.Fatal(err)
	}
	return conds, obj
}

var statusSite = &staticSiteData{Name: "site", Namespace: "default"}

func synced(status metav1.ConditionStatus, reason, msg string) metav1.Condition {
	return metav1.Condition{Status: status, Reason: reason, Message: msg}
}

func TestSetSynced_KeepsControllerConditions(t *testing.T) {
	c := newStatusClient(t, metav1.Condition{
		Type: pagesv1.ConditionCertificateReady, Status: metav1.ConditionTrue,
		Reason: "Ready", LastTransitionTime: metav1.Now(),
	})
	s := &Syncer{DynamicClient: c}

	err := s.setSynced(context.Background(), statusSite, synced(metav1.ConditionTrue, pagesv1.SyncReasonSynced, "Synced abc12345"), "abc12345")
	if err != nil {
		t.Fatalf("setSynced: %v", err)
	}
	conds, obj := storedConditions(t, c)
	if meta.FindStatusCondition(conds, pagesv1.ConditionCertificateReady) == nil {
		t.Error("CertificateReady condition was dropped")
	}
	got := meta.FindStatusCondition(conds, pagesv1.ConditionSynced)
	if got == nil || got.Status != metav1.ConditionTrue || got.ObservedGeneration != 3 {
		t.Errorf("Synced = %+v", got)
	}
	if commit, _, _ := unstructured.NestedString(obj.Object, "status", "lastCommit"); commit != "abc12345" {
		t.Errorf("lastCommit = %q", commit)
	}
	if lastSync, _, _ := unstructured.NestedString(obj.Object, "status", "lastSync"); lastSync == "" {
		t.Error("lastSync not set")
	}
}

func TestSetSynced_SyncingDoesNotHideFailure(t *testing.T) {
	c := newStatusClient(t, metav1.Condition{
		Type: pagesv1.ConditionSynced, Status: metav1.ConditionFalse,
		Reason: pagesv1.SyncReasonFetchFailed, Message: "refused", LastTransitionTime: metav1.Now(),
	})
	s := &Syncer{DynamicClient: c}

	if err := s.setSynced(context.Background(), statusSite, synced(metav1.ConditionUnknown, pagesv1.SyncReasonSyncing, "Cloning"), ""); err != nil {
		t.Fatal(err)
	}
	conds, _ := storedConditions(t, c)
	if got := meta.FindStatusCondition(conds, pagesv1.ConditionSynced); got.Status != metav1.ConditionFalse {
		t.Errorf("Synced = %s, want False kept while retrying", got.Status)
	}
}

func TestSetSynced_RetriesOnConflict(t *testing.T) {
	c := newStatusClient(t)
	conflicts := 0
	c.PrependReactor("update", "staticsites", func(k8stesting.Action) (bool, runtime.Object, error) {
		if conflicts == 0 {
			conflicts++
			return true, nil, apierrors.NewConflict(staticSiteGVR.GroupResource(), "site", errors.New("modified"))
		}
		return false, nil, nil
	})
	s := &Syncer{DynamicClient: c}

	if err := s.setSynced(context.Background(), statusSite, synced(metav1.ConditionTrue, pagesv1.SyncReasonSynced, "ok"), "abc"); err != nil {
		t.Fatalf("setSynced: %v", err)
	}
	if conflicts != 1 {
		t.Errorf("conflicts = %d, want 1", conflicts)
	}
}

func TestSetSynced_EventsOnTransitionsOnly(t *testing.T) {
	c := newStatusClient(t)
	rec := events.NewFakeRecorder(10)
	s := &Syncer{DynamicClient: c, Recorder: rec}
	ctx := context.Background()
	fail := synced(metav1.ConditionFalse, pagesv1.SyncReasonCloneFailed, "boom")
	ok := synced(metav1.ConditionTrue, pagesv1.SyncReasonSynced, "Synced abc")

	for _, cond := range []metav1.Condition{fail, fail, ok, ok} {
		if err := s.setSynced(ctx, statusSite, cond, "abc"); err != nil {
			t.Fatal(err)
		}
	}
	close(rec.Events)
	var got []string
	for e := range rec.Events {
		got = append(got, e)
	}
	if len(got) != 2 || !strings.Contains(got[0], "SyncFailed") || !strings.Contains(got[1], "SyncRecovered") {
		t.Errorf("events = %q, want one SyncFailed then one SyncRecovered", got)
	}
}

func TestSyncSite_ReportsSyncingThenResult(t *testing.T) {
	r := newTestRemote(t, archiveFiles, remoteOptions{})
	s := newTestSyncer(t, r)
	c := newStatusClient(t)
	s.DynamicClient = c

	mustSync(t, s, testSite(r, "/2018"))

	var statuses []metav1.ConditionStatus
	for _, action := range c.Actions() {
		update, ok := action.(k8stesting.UpdateAction)
		if !ok || action.GetSubresource() != "status" {
			continue
		}
		conds, err := readConditions(update.GetObject().(*unstructured.Unstructured))
		if err != nil {
			t.Fatal(err)
		}
		statuses = append(statuses, meta.FindStatusCondition(conds, pagesv1.ConditionSynced).Status)
	}
	want := []metav1.ConditionStatus{metav1.ConditionUnknown, metav1.ConditionTrue}
	if len(statuses) != 2 || statuses[0] != want[0] || statuses[1] != want[1] {
		t.Errorf("status writes = %v, want %v", statuses, want)
	}
}

func TestSyncSite_ReportsFailureReason(t *testing.T) {
	r := newTestRemote(t, archiveFiles, remoteOptions{})
	s := newTestSyncer(t, r)
	c := newStatusClient(t)
	s.DynamicClient = c
	site := testSite(r, "/2018")
	site.Branch = "gone"

	_ = s.syncSite(context.Background(), site)

	conds, _ := storedConditions(t, c)
	got := meta.FindStatusCondition(conds, pagesv1.ConditionSynced)
	if got == nil || got.Status != metav1.ConditionFalse || got.Reason != pagesv1.SyncReasonCloneFailed {
		t.Errorf("Synced = %+v, want False/CloneFailed", got)
	}
}
