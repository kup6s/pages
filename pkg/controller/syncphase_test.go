package controller

import (
	"context"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	pagesv1 "github.com/kup6s/pages/pkg/apis/v1beta1"
)

func TestSyncPhase(t *testing.T) {
	since := metav1.NewTime(time.Date(2026, 10, 13, 14, 2, 0, 0, time.UTC))
	tests := []struct {
		name      string
		condition *metav1.Condition
		commit    string
		wantPhase pagesv1.Phase
		wantMsg   string
	}{
		{
			name:      "no sync yet",
			wantPhase: pagesv1.PhasePending,
			wantMsg:   "Waiting for first sync",
		},
		{
			name: "clone in progress",
			condition: &metav1.Condition{
				Type: pagesv1.ConditionSynced, Status: metav1.ConditionUnknown,
				Reason: pagesv1.SyncReasonSyncing, LastTransitionTime: since,
			},
			wantPhase: pagesv1.PhaseSyncing,
			wantMsg:   "Syncing since 2026-10-13T14:02:00Z",
		},
		{
			name: "synced",
			condition: &metav1.Condition{
				Type: pagesv1.ConditionSynced, Status: metav1.ConditionTrue,
				Reason: pagesv1.SyncReasonSynced, LastTransitionTime: since,
			},
			commit:    "abc12345",
			wantPhase: pagesv1.PhaseReady,
			wantMsg:   "Synced abc12345",
		},
		{
			name: "sync failed",
			condition: &metav1.Condition{
				Type: pagesv1.ConditionSynced, Status: metav1.ConditionFalse,
				Reason: pagesv1.SyncReasonCloneFailed, Message: "git clone: exit status 128",
				LastTransitionTime: since,
			},
			wantPhase: pagesv1.PhaseError,
			wantMsg:   "Sync failed: CloneFailed: git clone: exit status 128",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			site := &pagesv1.StaticSite{Status: pagesv1.StaticSiteStatus{LastCommit: tt.commit}}
			if tt.condition != nil {
				site.Status.Conditions = []metav1.Condition{*tt.condition}
			}
			phase, msg := syncPhase(site)
			if phase != tt.wantPhase || msg != tt.wantMsg {
				t.Errorf("syncPhase() = (%q, %q), want (%q, %q)", phase, msg, tt.wantPhase, tt.wantMsg)
			}
		})
	}
}

func TestReconcile_KeepsSyncedConditionAndDerivesError(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = pagesv1.AddToScheme(scheme)

	site := &pagesv1.StaticSite{
		ObjectMeta: metav1.ObjectMeta{
			Name: "failing", Namespace: "default", UID: "uid-failing",
			Finalizers: []string{finalizerName},
		},
		Spec: pagesv1.StaticSiteSpec{Repo: "https://github.com/example/repo.git"},
		Status: pagesv1.StaticSiteStatus{
			SyncToken: "token",
			Conditions: []metav1.Condition{{
				Type: pagesv1.ConditionSynced, Status: metav1.ConditionFalse,
				Reason: pagesv1.SyncReasonFetchFailed, Message: "connection refused",
				LastTransitionTime: metav1.Now(),
			}},
		},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(site).WithStatusSubresource(site).Build()
	r := &StaticSiteReconciler{
		Client: c, DynamicClient: &fakeDynamicClient{}, Recorder: events.NewFakeRecorder(10),
		PagesDomain: "pages.kup6s.com", ClusterIssuer: "letsencrypt-prod",
		NginxNamespace: "kup6s-pages", NginxServiceName: "kup6s-pages-nginx",
	}

	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "failing", Namespace: "default"}}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}

	got := &pagesv1.StaticSite{}
	if err := c.Get(context.Background(), req.NamespacedName, got); err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status.Phase != pagesv1.PhaseError {
		t.Errorf("Phase = %q, want %q", got.Status.Phase, pagesv1.PhaseError)
	}
	if want := "Sync failed: FetchFailed: connection refused"; got.Status.Message != want {
		t.Errorf("Message = %q, want %q", got.Status.Message, want)
	}
	if meta.FindStatusCondition(got.Status.Conditions, pagesv1.ConditionSynced) == nil {
		t.Error("Synced condition was dropped by the controller")
	}
}
