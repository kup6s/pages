# Syncer Sparse Partial Clones Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** The syncer serves path-scoped StaticSites from large repositories with per-site sparse partial clones through the git CLI, recovers from broken clones, protects the shared volume, and makes sync failures visible in status, events and Prometheus.

**Architecture:** A thin `gitCLI` wrapper replaces go-git. Each site gets its own `--filter=blob:none` clone in `/sites/.repos/<name>`, sparse when `path` is set, cloned into a temporary directory and swapped in; `/sites/<name>` is always a symlink. The syncer owns the `Synced` condition; the controller derives `phase` from it. The operator exports per-site metrics from its cache, the syncer exports its own metrics on a separate port, and the chart can install PodMonitors and a PrometheusRule.

**Tech Stack:** Go 1.26, git CLI (≥ 2.38 in tests, 2.54 in the image), client-go dynamic client and events, controller-runtime, prometheus/client_golang, Helm with helm-unittest, Hugo docs.

**Spec:** `docs/superpowers/specs/2026-10-13-syncer-sparse-clones-design.md`

## Global Constraints

- Worktree `/home/jensens/ws/pro/kup6s/pages/.worktrees/153`, branch `fix/153-syncer-sparse-clones`. All commands run from the worktree root.
- Never mention AI, Claude or assistants in code, commits, PR title or body. No `Co-Authored-By` trailers.
- TDD: write the failing test first, see it fail, then implement.
- Before every commit: `GOTOOLCHAIN=go1.26.0 go test -race ./...` passes and `make lint` reports `0 issues` (golangci-lint v2.14.0; a binary is at `/tmp/claude-1000/-home-jensens-ws-pro-kup6s/425a568e-355a-432c-81ee-d21825abf590/scratchpad/bin`, put it on `PATH` or install with `go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0`).
- Commit messages: conventional prefix (`feat:`, `fix:`, `refactor:`, `test:`, `docs:`, `build:`), English, one logical unit per commit. Structural and behavioural changes in separate commits.
- go-git must be gone from `go.mod` after Task 6.
- git environment for every invocation: `GIT_TERMINAL_PROMPT=0`, `GIT_CONFIG_NOSYSTEM=1`, `GIT_CONFIG_GLOBAL=/dev/null`, `GIT_ALLOW_PROTOCOL=http:https`; credentials only via `GIT_CONFIG_COUNT=1`, `GIT_CONFIG_KEY_0=http.<scheme>://<host>/.extraHeader`, `GIT_CONFIG_VALUE_0=Authorization: Basic <base64(user:password)>`.
- Defaults: `--git-timeout=10m`, `--min-free-percent=10`, `--metrics-addr=:9090`; chart `syncer.metricsPort: 9090`; re-clone when `.git` > 2 × checkout + 50 MiB.
- Directory layout: `/sites/.repos/<name>` (clone), `/sites/<name>` → `/sites/.repos/<name>/<path>` (symlink), `/sites/.repos/.tmp-<name>-<8 hex>`, `/sites/.repos/.old-<name>-<8 hex>`, `/sites/.tmp-link-<name>-<8 hex>`.
- Condition type `Synced`; reasons `Synced`, `Syncing`, `CloneFailed`, `FetchFailed`, `AuthFailed`, `SubpathMissing`, `DiskFull`, `Timeout`, `InvalidSpec`, fallback `SyncFailed`.

## Review Focus

1. **Branch names with a slash** (`release/2026`): the second sync must update in place, not re-clone. Pinned in Task 6 (`TestSyncSite_BranchWithSlashUpdatesInPlace`).
2. **`path` spelled `/public/`, `./public` or nested `docs/public`**: the stored sparse path must compare equal, otherwise every sync re-clones. Pinned in Task 6 (`TestSyncSite_PathSpellingsDoNotReclone`).
3. **Git server without partial-clone support** (`uploadpack.allowFilter` unset): clone still works and later syncs update in place. Pinned in Task 6 (`TestSyncSite_ServerWithoutFilterUpdatesInPlace`).
4. **Branch that does not exist**: `CloneFailed` with git's message, no `.tmp-*` left, previously served content untouched. Pinned in Task 6 (`TestSyncSite_MissingBranchKeepsServedContent`).
5. **Site names with dashes** (`archive` and `archive-2018`): leftover cleanup only touches the matching site and never a running sync. Pinned in Task 4 (`TestSiteFromTempName`) and Task 7 (`TestRemoveLeftovers_DistinguishesSimilarNames`).

## File Map

| File | Responsibility |
|---|---|
| `pkg/apis/v1beta1/types.go` | `ConditionSynced`, `SyncReason*` constants, printer column marker |
| `charts/kup6s-pages/crds/staticsites.pages.kup6s.com.yaml` | `Synced` printer column |
| `pkg/controller/staticsite.go` | `syncPhase`, `updateFinalStatus` derives phase |
| `pkg/controller/syncphase_test.go` | phase derivation tests |
| `pkg/controller/metrics.go`, `metrics_test.go` | `SiteMetricsCollector` |
| `cmd/operator/main.go` | registers the collector |
| `pkg/syncer/gitserver_test.go` | test-only smart-HTTP git server and remote helpers |
| `pkg/syncer/gitcli.go`, `gitcli_test.go` | git CLI wrapper |
| `pkg/syncer/layout.go`, `layout_test.go` | path normalisation, temp names, atomic symlink |
| `pkg/syncer/errors.go` | `syncError`, reason classification |
| `pkg/syncer/sync.go`, `sync_test.go` | per-site sync flow |
| `pkg/syncer/leftovers.go`, `leftovers_test.go` | per-site locks, leftover cleanup |
| `pkg/syncer/status.go`, `status_test.go` | `Synced` condition writer, events |
| `pkg/syncer/diskspace.go`, `diskspace_test.go` | free-space check and clone watchdog |
| `pkg/syncer/metrics.go`, `metrics_test.go` | syncer Prometheus metrics |
| `pkg/syncer/git.go`, `git_test.go` | remaining: URL validation, SyncAll/SyncOne, secrets, RunLoop, Cleanup, DeleteSite |
| `cmd/syncer/main.go` | flags, recorder, metrics server |
| `charts/kup6s-pages/...` | args, ports, RBAC, nginx, PodMonitors, PrometheusRule, values, schema, unit tests |
| `docs/content/...`, `CLAUDE.md` | documentation |

---

### Task 1: Synced condition contract and controller phase derivation

**Files:**
- Modify: `pkg/apis/v1beta1/types.go:12-16` (printcolumn markers), `:147-151` (condition constants)
- Modify: `charts/kup6s-pages/crds/staticsites.pages.kup6s.com.yaml:145-150` (printer columns)
- Modify: `pkg/controller/staticsite.go:268-272` (`updateFinalStatus`)
- Create: `pkg/controller/syncphase_test.go`
- Modify: `pkg/controller/staticsite_test.go:93`, `:662`, `:2298`, `:2866`

**Interfaces:**
- Produces: `pagesv1.ConditionSynced`, `pagesv1.SyncReasonSynced`, `SyncReasonSyncing`, `SyncReasonCloneFailed`, `SyncReasonFetchFailed`, `SyncReasonAuthFailed`, `SyncReasonSubpathMissing`, `SyncReasonDiskFull`, `SyncReasonTimeout`, `SyncReasonInvalidSpec`, `SyncReasonSyncFailed` (all `string`); `func syncPhase(site *pagesv1.StaticSite) (pagesv1.Phase, string)` in package `controller`.

- [ ] **Step 1: Write the failing test**

Create `pkg/controller/syncphase_test.go`:

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `GOTOOLCHAIN=go1.26.0 go test ./pkg/controller/ -run 'TestSyncPhase|TestReconcile_KeepsSyncedConditionAndDerivesError'`
Expected: FAIL to compile with `undefined: syncPhase` and `undefined: pagesv1.ConditionSynced`.

- [ ] **Step 3: Add the API constants**

In `pkg/apis/v1beta1/types.go`, replace the condition const block:

```go
// Condition types for StaticSite
const (
	// ConditionCertificateReady indicates whether the TLS certificate is ready
	ConditionCertificateReady = "CertificateReady"

	// ConditionSynced reports whether the syncer has checked out the configured
	// revision. Only the syncer writes it; the controller derives phase from it.
	ConditionSynced = "Synced"
)

// Reasons of the Synced condition
const (
	SyncReasonSynced         = "Synced"
	SyncReasonSyncing        = "Syncing"
	SyncReasonCloneFailed    = "CloneFailed"
	SyncReasonFetchFailed    = "FetchFailed"
	SyncReasonAuthFailed     = "AuthFailed"
	SyncReasonSubpathMissing = "SubpathMissing"
	SyncReasonDiskFull       = "DiskFull"
	SyncReasonTimeout        = "Timeout"
	SyncReasonInvalidSpec    = "InvalidSpec"
	SyncReasonSyncFailed     = "SyncFailed"
)
```

In the printcolumn markers at the top of the file, add after the `Status` line:

```go
// +kubebuilder:printcolumn:name="Synced",type=string,JSONPath=`.status.conditions[?(@.type=="Synced")].status`
```

In `charts/kup6s-pages/crds/staticsites.pages.kup6s.com.yaml`, add after the `Status` printer column:

```yaml
        - name: Synced
          type: string
          jsonPath: .status.conditions[?(@.type=="Synced")].status
```

- [ ] **Step 4: Implement the derivation**

In `pkg/controller/staticsite.go` add (imports `time` and `k8s.io/apimachinery/pkg/api/meta` are already used in the file; add `time` if missing):

```go
// syncPhase derives phase and message from the syncer-owned Synced condition.
func syncPhase(site *pagesv1.StaticSite) (pagesv1.Phase, string) {
	synced := meta.FindStatusCondition(site.Status.Conditions, pagesv1.ConditionSynced)
	switch {
	case synced == nil:
		return pagesv1.PhasePending, "Waiting for first sync"
	case synced.Status == metav1.ConditionTrue:
		return pagesv1.PhaseReady, "Synced " + site.Status.LastCommit
	case synced.Status == metav1.ConditionFalse:
		return pagesv1.PhaseError, fmt.Sprintf("Sync failed: %s: %s", synced.Reason, synced.Message)
	default:
		return pagesv1.PhaseSyncing, "Syncing since " + synced.LastTransitionTime.UTC().Format(time.RFC3339)
	}
}
```

In `updateFinalStatus`, replace

```go
	site.Status.Phase = pagesv1.PhaseReady
	site.Status.Message = "Site configured, waiting for sync"
```

with

```go
	site.Status.Phase, site.Status.Message = syncPhase(site)
```

- [ ] **Step 5: Update the existing expectations**

A freshly reconciled site has no `Synced` condition yet, so it is `Pending`. In `pkg/controller/staticsite_test.go`:
- lines 93-94, 662-663, 2298-2299: change `pagesv1.PhaseReady` to `pagesv1.PhasePending` in both the comparison and the message.
- line 2866: `failOnPhase: pagesv1.PhaseReady` becomes `failOnPhase: pagesv1.PhasePending`.

- [ ] **Step 6: Run the controller tests**

Run: `GOTOOLCHAIN=go1.26.0 go test -race ./pkg/controller/ ./pkg/apis/...`
Expected: PASS.

- [ ] **Step 7: Lint and commit**

```bash
make lint
git add pkg/apis/v1beta1/types.go charts/kup6s-pages/crds/staticsites.pages.kup6s.com.yaml pkg/controller/
git commit -m "fix: derive StaticSite phase from the syncer's Synced condition

The controller set phase Ready on every reconcile and overwrote sync
errors written by the syncer. The syncer now owns the Synced condition;
the controller derives phase and message from it."
```

---

### Task 2: Test git server

**Files:**
- Create: `pkg/syncer/gitserver_test.go`

**Interfaces:**
- Produces (test-only, package `syncer`): `type remoteOptions struct{ User, Password string; NoFilter bool }`; `type testRemote struct{ URL, Host string; ... }`; `func newTestRemote(t *testing.T, files map[string]string, opts remoteOptions) *testRemote`; methods `commit(files map[string]string, msg string) string` (returns short hash, 8 chars), `amend(files map[string]string, msg string) string`, `pushBranch(branch string, files map[string]string) string`, `close()`; `func runGit(t *testing.T, dir string, args ...string) string`; `func tryGit(dir string, args ...string) (string, error)`.

- [ ] **Step 1: Write the helper and its self-test**

Create `pkg/syncer/gitserver_test.go`:

```go
package syncer

import (
	"net/http"
	"net/http/cgi"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// remoteOptions configures a test remote.
type remoteOptions struct {
	User, Password string // basic auth required when User is set
	NoFilter       bool   // serve without partial clone support
}

// testRemote is a bare repository served over smart HTTP by git http-backend.
type testRemote struct {
	t       *testing.T
	URL     string // http://127.0.0.1:<port>/site.git
	Host    string // 127.0.0.1
	workDir string
	server  *httptest.Server
}

func testGitEnv() []string {
	return append(os.Environ(),
		"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_TERMINAL_PROMPT=0",
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com",
	)
}

// tryGit runs git in dir and returns trimmed combined output.
func tryGit(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = testGitEnv()
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// runGit runs git in dir and fails the test on error.
func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := tryGit(dir, args...)
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return out
}

// newTestRemote creates a repository with one commit on main containing files
// and serves it over HTTP.
func newTestRemote(t *testing.T, files map[string]string, opts remoteOptions) *testRemote {
	t.Helper()
	root := t.TempDir()
	bare := filepath.Join(root, "site.git")
	runGit(t, root, "init", "--quiet", "--bare", "--initial-branch=main", bare)
	if !opts.NoFilter {
		runGit(t, bare, "config", "uploadpack.allowFilter", "true")
		runGit(t, bare, "config", "uploadpack.allowAnySHA1InWant", "true")
	}
	work := filepath.Join(root, "work")
	runGit(t, root, "init", "--quiet", "--initial-branch=main", work)
	runGit(t, work, "remote", "add", "origin", bare)

	r := &testRemote{t: t, workDir: work}
	r.commit(files, "initial")

	r.server = httptest.NewServer(gitHTTPHandler(root, opts))
	t.Cleanup(r.server.Close)
	u, err := url.Parse(r.server.URL)
	if err != nil {
		t.Fatal(err)
	}
	r.URL = r.server.URL + "/site.git"
	r.Host = u.Hostname()
	return r
}

func gitHTTPHandler(projectRoot string, opts remoteOptions) http.Handler {
	gitPath, err := exec.LookPath("git")
	if err != nil {
		panic(err)
	}
	backend := &cgi.Handler{
		Path:       gitPath,
		Args:       []string{"http-backend"},
		InheritEnv: []string{"PATH"},
		Env: []string{
			"GIT_PROJECT_ROOT=" + projectRoot,
			"GIT_HTTP_EXPORT_ALL=1",
			"GIT_CONFIG_NOSYSTEM=1",
			"GIT_CONFIG_GLOBAL=/dev/null",
		},
	}
	if opts.User == "" {
		return backend
	}
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		user, password, ok := req.BasicAuth()
		if !ok || user != opts.User || password != opts.Password {
			w.Header().Set("WWW-Authenticate", `Basic realm="git"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		backend.ServeHTTP(w, req)
	})
}

func (r *testRemote) writeFiles(files map[string]string) {
	for p, content := range files {
		full := filepath.Join(r.workDir, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			r.t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			r.t.Fatal(err)
		}
	}
	runGit(r.t, r.workDir, "add", "--all")
}

// commit adds files to main, pushes, and returns the 8-char commit hash.
func (r *testRemote) commit(files map[string]string, msg string) string {
	r.writeFiles(files)
	runGit(r.t, r.workDir, "commit", "--quiet", "--allow-empty", "-m", msg)
	runGit(r.t, r.workDir, "push", "--quiet", "origin", "HEAD:refs/heads/main")
	return runGit(r.t, r.workDir, "rev-parse", "--short=8", "HEAD")
}

// amend rewrites the last commit on main and force-pushes it.
func (r *testRemote) amend(files map[string]string, msg string) string {
	r.writeFiles(files)
	runGit(r.t, r.workDir, "commit", "--quiet", "--amend", "-m", msg)
	runGit(r.t, r.workDir, "push", "--quiet", "--force", "origin", "HEAD:refs/heads/main")
	return runGit(r.t, r.workDir, "rev-parse", "--short=8", "HEAD")
}

// pushBranch commits files on top of main to a new branch and pushes it.
func (r *testRemote) pushBranch(branch string, files map[string]string) string {
	runGit(r.t, r.workDir, "checkout", "--quiet", "-b", branch)
	r.writeFiles(files)
	runGit(r.t, r.workDir, "commit", "--quiet", "--allow-empty", "-m", "branch "+branch)
	runGit(r.t, r.workDir, "push", "--quiet", "origin", "HEAD:refs/heads/"+branch)
	hash := runGit(r.t, r.workDir, "rev-parse", "--short=8", "HEAD")
	runGit(r.t, r.workDir, "checkout", "--quiet", "main")
	return hash
}

// close stops serving the repository, so fetches fail.
func (r *testRemote) close() { r.server.Close() }

func TestTestRemote_ServesRepositoryOverHTTP(t *testing.T) {
	r := newTestRemote(t, map[string]string{"index.html": "hi"}, remoteOptions{})
	head := r.commit(map[string]string{"index.html": "v2"}, "second")

	out := runGit(t, t.TempDir(), "ls-remote", r.URL, "refs/heads/main")
	if !strings.HasPrefix(out, head) {
		t.Errorf("ls-remote = %q, want prefix %q", out, head)
	}
}

func TestTestRemote_RequiresAuthWhenConfigured(t *testing.T) {
	r := newTestRemote(t, map[string]string{"index.html": "hi"}, remoteOptions{User: "u", Password: "p"})

	if _, err := tryGit(t.TempDir(), "ls-remote", r.URL); err == nil {
		t.Error("ls-remote without credentials succeeded, want failure")
	}
}
```

- [ ] **Step 2: Run the self-tests**

Run: `GOTOOLCHAIN=go1.26.0 go test ./pkg/syncer/ -run TestTestRemote -v`
Expected: both PASS. If `TestTestRemote_ServesRepositoryOverHTTP` fails with `git-http-backend` not found, the git installation lacks `http-backend`; on Ubuntu it ships with the `git` package.

- [ ] **Step 3: Lint and commit**

```bash
make lint
git add pkg/syncer/gitserver_test.go
git commit -m "test: serve test repositories over smart HTTP"
```

---

### Task 3: git CLI wrapper

**Files:**
- Create: `pkg/syncer/gitcli.go`
- Create: `pkg/syncer/gitcli_test.go`

**Interfaces:**
- Consumes: Task 2 helpers.
- Produces:
  - `type gitAuth struct{ Username, Password string }`
  - `type gitCLI struct{ Binary string; Timeout time.Duration }`
  - `type gitError struct{ Command string; ExitCode int; Stderr string; Err error }` with `Error()` and `Unwrap()`
  - `type cloneInfo struct{ OriginURL, Branch, SparsePath string }`
  - `func (g *gitCLI) clone(ctx context.Context, repoURL, branch, subpath, dest string, auth *gitAuth) error`
  - `func (g *gitCLI) fetch(ctx context.Context, dir, repoURL, branch string, auth *gitAuth) error`
  - `func (g *gitCLI) resetHard(ctx context.Context, dir, repoURL, branch string, auth *gitAuth) error`
  - `func (g *gitCLI) head(ctx context.Context, dir string) (string, error)` (8-char hash)
  - `func (g *gitCLI) inspect(ctx context.Context, dir string) (cloneInfo, error)`
  - `func gitEnv(dir, repoURL string, auth *gitAuth) []string`
  - `func isAuthError(err error) bool`
  - `func truncate(s string, max int) string`

- [ ] **Step 1: Write the failing tests**

Create `pkg/syncer/gitcli_test.go`:

```go
package syncer

import (
	"context"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

var archiveFiles = map[string]string{
	"index.html":            "root",
	"2018/index.html":       "2018 v1",
	"2005/index.html":       "2005 v1",
	"docs/public/index.html": "docs v1",
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

func TestGitCLI_CloneWholeRepository(t *testing.T) {
	r := newTestRemote(t, archiveFiles, remoteOptions{})
	g := &gitCLI{}
	dest := filepath.Join(t.TempDir(), "clone")

	if err := g.clone(context.Background(), r.URL, "main", "", dest, nil); err != nil {
		t.Fatalf("clone: %v", err)
	}
	if got := readFile(t, filepath.Join(dest, "2005", "index.html")); got != "2005 v1" {
		t.Errorf("2005/index.html = %q", got)
	}
	info, err := g.inspect(context.Background(), dest)
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}
	want := cloneInfo{OriginURL: r.URL, Branch: "main", SparsePath: ""}
	if info != want {
		t.Errorf("inspect() = %+v, want %+v", info, want)
	}
}

func TestGitCLI_CloneSparseChecksOutOnlySubpath(t *testing.T) {
	r := newTestRemote(t, archiveFiles, remoteOptions{})
	g := &gitCLI{}
	dest := filepath.Join(t.TempDir(), "clone")

	if err := g.clone(context.Background(), r.URL, "main", "2018", dest, nil); err != nil {
		t.Fatalf("clone: %v", err)
	}
	if got := readFile(t, filepath.Join(dest, "2018", "index.html")); got != "2018 v1" {
		t.Errorf("2018/index.html = %q", got)
	}
	if _, err := os.Stat(filepath.Join(dest, "2005")); !os.IsNotExist(err) {
		t.Error("2005 is checked out, want only 2018")
	}
	info, err := g.inspect(context.Background(), dest)
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}
	if info.SparsePath != "2018" {
		t.Errorf("SparsePath = %q, want 2018", info.SparsePath)
	}
}

func TestGitCLI_FetchAndResetFollowCommitsAndForcePushes(t *testing.T) {
	r := newTestRemote(t, archiveFiles, remoteOptions{})
	g := &gitCLI{}
	ctx := context.Background()
	dest := filepath.Join(t.TempDir(), "clone")
	if err := g.clone(ctx, r.URL, "main", "2018", dest, nil); err != nil {
		t.Fatalf("clone: %v", err)
	}

	for _, step := range []struct {
		name   string
		push   func() string
		expect string
	}{
		{"commit", func() string { return r.commit(map[string]string{"2018/index.html": "2018 v2"}, "v2") }, "2018 v2"},
		{"force-push", func() string { return r.amend(map[string]string{"2018/index.html": "2018 v3"}, "v3") }, "2018 v3"},
	} {
		want := step.push()
		if err := g.fetch(ctx, dest, r.URL, "main", nil); err != nil {
			t.Fatalf("%s: fetch: %v", step.name, err)
		}
		if err := g.resetHard(ctx, dest, r.URL, "main", nil); err != nil {
			t.Fatalf("%s: reset: %v", step.name, err)
		}
		if got, _ := g.head(ctx, dest); got != want {
			t.Errorf("%s: head = %q, want %q", step.name, got, want)
		}
		if got := readFile(t, filepath.Join(dest, "2018", "index.html")); got != step.expect {
			t.Errorf("%s: content = %q, want %q", step.name, got, step.expect)
		}
	}
}

func TestGitCLI_CredentialsStayOutOfConfig(t *testing.T) {
	r := newTestRemote(t, archiveFiles, remoteOptions{User: "deploy", Password: "s3cret-token"})
	g := &gitCLI{}
	ctx := context.Background()
	dest := filepath.Join(t.TempDir(), "clone")

	err := g.clone(ctx, r.URL, "main", "2018", dest, &gitAuth{Username: "deploy", Password: "s3cret-token"})
	if err != nil {
		t.Fatalf("clone with credentials: %v", err)
	}
	config := readFile(t, filepath.Join(dest, ".git", "config"))
	if strings.Contains(config, "s3cret-token") || strings.Contains(config, "Authorization") {
		t.Errorf(".git/config contains credentials:\n%s", config)
	}

	err = g.clone(ctx, r.URL, "main", "2018", filepath.Join(t.TempDir(), "anon"), nil)
	if err == nil || !isAuthError(err) {
		t.Errorf("clone without credentials: err = %v, want an auth error", err)
	}
}

func TestGitEnv_ScopesCredentialsToRepositoryHost(t *testing.T) {
	env := gitEnv("/sites/.repos/site", "https://git.example.org/org/repo.git", &gitAuth{Username: "u", Password: "p"})
	token := base64.StdEncoding.EncodeToString([]byte("u:p"))
	for _, want := range []string{
		"GIT_TERMINAL_PROMPT=0",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_ALLOW_PROTOCOL=http:https",
		"GIT_CEILING_DIRECTORIES=/sites/.repos",
		"GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_KEY_0=http.https://git.example.org/.extraHeader",
		"GIT_CONFIG_VALUE_0=Authorization: Basic " + token,
	} {
		if !slices.Contains(env, want) {
			t.Errorf("gitEnv() lacks %q", want)
		}
	}
	if slices.ContainsFunc(gitEnv("", "https://git.example.org/r.git", nil), func(e string) bool {
		return strings.HasPrefix(e, "GIT_CONFIG_COUNT=")
	}) {
		t.Error("gitEnv() without auth sets GIT_CONFIG_COUNT")
	}
}

func TestGitCLI_TimeoutKillsGit(t *testing.T) {
	g := &gitCLI{Binary: sleepingGit(t), Timeout: 100 * time.Millisecond}
	start := time.Now()
	err := g.clone(context.Background(), "https://example.org/r.git", "main", "", filepath.Join(t.TempDir(), "c"), nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want context.DeadlineExceeded", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Error("git was not killed at the timeout")
	}
}

func TestGitCLI_InspectRejectsBrokenClone(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "broken")
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := (&gitCLI{}).inspect(context.Background(), dir); err == nil {
		t.Error("inspect() of a clone without objects succeeded, want error")
	}
}

// sleepingGit returns a fake git binary that hangs, for timeout tests.
func sleepingGit(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "git")
	if err := os.WriteFile(p, []byte("#!/bin/sh\nexec sleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `GOTOOLCHAIN=go1.26.0 go test ./pkg/syncer/ -run 'TestGitCLI|TestGitEnv'`
Expected: FAIL to compile, `undefined: gitCLI`.

- [ ] **Step 3: Implement the wrapper**

Create `pkg/syncer/gitcli.go`:

```go
// Package syncer - git command line wrapper
package syncer

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const maxGitStderr = 2048

// gitAuth holds HTTP basic credentials for one repository.
type gitAuth struct {
	Username string
	Password string
}

// gitCLI runs the git binary with an environment that ignores system and
// user configuration and never prompts.
type gitCLI struct {
	// Binary is the git executable; empty means "git" from PATH.
	Binary string
	// Timeout bounds every invocation; zero means no limit.
	Timeout time.Duration
}

// gitError describes a failed git invocation. Err is the context error when
// the invocation was cancelled or timed out, so errors.Is works on it.
type gitError struct {
	Command  string
	ExitCode int
	Stderr   string
	Err      error
}

func (e *gitError) Error() string {
	if e.Stderr == "" {
		return fmt.Sprintf("git %s: %v", e.Command, e.Err)
	}
	return fmt.Sprintf("git %s: %v: %s", e.Command, e.Err, e.Stderr)
}

func (e *gitError) Unwrap() error { return e.Err }

// cloneInfo is what the syncer compares against the StaticSite spec.
type cloneInfo struct {
	OriginURL  string
	Branch     string
	SparsePath string
}

// clone makes a shallow partial clone of branch into dest. A non-empty
// subpath restricts the checkout to that directory (sparse cone mode).
func (g *gitCLI) clone(ctx context.Context, repoURL, branch, subpath, dest string, auth *gitAuth) error {
	args := []string{"clone", "--quiet", "--depth", "1", "--single-branch", "--no-tags",
		"--filter=blob:none", "--branch", branch}
	if subpath != "" {
		args = append(args, "--sparse")
	}
	args = append(args, "--", repoURL, dest)
	if _, err := g.run(ctx, "", repoURL, auth, args...); err != nil {
		return err
	}
	if subpath == "" {
		return nil
	}
	_, err := g.run(ctx, dest, repoURL, auth, "sparse-checkout", "set", subpath)
	return err
}

// fetch updates refs/remotes/origin/<branch> to the remote head.
func (g *gitCLI) fetch(ctx context.Context, dir, repoURL, branch string, auth *gitAuth) error {
	refspec := "+refs/heads/" + branch + ":refs/remotes/origin/" + branch
	_, err := g.run(ctx, dir, repoURL, auth, "fetch", "--quiet", "--depth", "1", "--no-tags", "origin", refspec)
	return err
}

// resetHard checks out the fetched head. It needs credentials because
// missing blobs are fetched on demand.
func (g *gitCLI) resetHard(ctx context.Context, dir, repoURL, branch string, auth *gitAuth) error {
	_, err := g.run(ctx, dir, repoURL, auth, "reset", "--quiet", "--hard", "refs/remotes/origin/"+branch)
	return err
}

// head returns the abbreviated commit hash checked out in dir.
func (g *gitCLI) head(ctx context.Context, dir string) (string, error) {
	return g.run(ctx, dir, "", nil, "rev-parse", "--short=8", "HEAD")
}

// inspect reads origin, branch and sparse path of the clone in dir. It fails
// when dir is not a usable clone.
func (g *gitCLI) inspect(ctx context.Context, dir string) (cloneInfo, error) {
	if _, err := g.run(ctx, dir, "", nil, "rev-parse", "--verify", "--quiet", "HEAD^{commit}"); err != nil {
		return cloneInfo{}, err
	}
	var info cloneInfo
	var err error
	if info.OriginURL, err = g.configGet(ctx, dir, "remote.origin.url"); err != nil {
		return cloneInfo{}, err
	}
	refspec, err := g.configGet(ctx, dir, "remote.origin.fetch")
	if err != nil {
		return cloneInfo{}, err
	}
	info.Branch = branchFromRefspec(refspec)
	sparse, err := g.configGet(ctx, dir, "core.sparseCheckout")
	if err != nil {
		return cloneInfo{}, err
	}
	if sparse == "true" {
		if info.SparsePath, err = g.run(ctx, dir, "", nil, "sparse-checkout", "list"); err != nil {
			return cloneInfo{}, err
		}
	}
	return info, nil
}

// configGet returns a config value, or "" when the key is not set.
func (g *gitCLI) configGet(ctx context.Context, dir, key string) (string, error) {
	out, err := g.run(ctx, dir, "", nil, "config", "--get", key)
	var gerr *gitError
	if errors.As(err, &gerr) && gerr.ExitCode == 1 {
		return "", nil
	}
	return out, err
}

func branchFromRefspec(refspec string) string {
	src, _, _ := strings.Cut(strings.TrimPrefix(refspec, "+"), ":")
	return strings.TrimPrefix(src, "refs/heads/")
}

func (g *gitCLI) run(ctx context.Context, dir, repoURL string, auth *gitAuth, args ...string) (string, error) {
	if g.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, g.Timeout)
		defer cancel()
	}
	bin := g.Binary
	if bin == "" {
		bin = "git"
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir
	cmd.Env = gitEnv(dir, repoURL, auth)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err == nil {
		return strings.TrimSpace(stdout.String()), nil
	}
	gerr := &gitError{
		Command:  args[0],
		ExitCode: -1,
		Stderr:   truncate(strings.TrimSpace(stderr.String()), maxGitStderr),
		Err:      err,
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		gerr.ExitCode = exitErr.ExitCode()
	}
	if ctx.Err() != nil {
		gerr.Err = ctx.Err()
	}
	return "", gerr
}

// gitEnv builds the complete environment of a git invocation. Credentials
// travel as a URL-scoped config entry in the environment, never in argv or
// .git/config.
func gitEnv(dir, repoURL string, auth *gitAuth) []string {
	env := []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + os.TempDir(),
		"LC_ALL=C",
		"GIT_TERMINAL_PROMPT=0",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_ALLOW_PROTOCOL=http:https",
	}
	if dir != "" {
		// Never fall back to a repository above the clone.
		env = append(env, "GIT_CEILING_DIRECTORIES="+filepath.Dir(dir))
	}
	if auth != nil {
		token := base64.StdEncoding.EncodeToString([]byte(auth.Username + ":" + auth.Password))
		env = append(env,
			"GIT_CONFIG_COUNT=1",
			"GIT_CONFIG_KEY_0=http."+credentialScope(repoURL)+".extraHeader",
			"GIT_CONFIG_VALUE_0=Authorization: Basic "+token,
		)
	}
	return env
}

// credentialScope limits the auth header to the repository's origin.
func credentialScope(repoURL string) string {
	u, err := url.Parse(repoURL)
	if err != nil {
		return repoURL
	}
	return u.Scheme + "://" + u.Host + "/"
}

// isAuthError reports whether git failed because the server rejected or
// required credentials.
func isAuthError(err error) bool {
	var gerr *gitError
	if !errors.As(err, &gerr) {
		return false
	}
	for _, marker := range []string{
		"Authentication failed",
		"could not read Username",
		"terminal prompts disabled",
		"returned error: 401",
		"returned error: 403",
	} {
		if strings.Contains(gerr.Stderr, marker) {
			return true
		}
	}
	return false
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `GOTOOLCHAIN=go1.26.0 go test -race ./pkg/syncer/ -run 'TestGitCLI|TestGitEnv' -v`
Expected: PASS.

- [ ] **Step 5: Lint and commit**

```bash
make lint
git add pkg/syncer/gitcli.go pkg/syncer/gitcli_test.go
git commit -m "feat: add git CLI wrapper for partial and sparse clones"
```

---

### Task 4: Layout helpers and sync errors

**Files:**
- Create: `pkg/syncer/layout.go`, `pkg/syncer/layout_test.go`
- Create: `pkg/syncer/errors.go`

**Interfaces:**
- Consumes: `isAuthError` (Task 3), `pagesv1.SyncReason*` (Task 1).
- Produces:
  - constants `reposDirName = ".repos"`, `tmpRepoPrefix = ".tmp-"`, `oldRepoPrefix = ".old-"`, `tmpLinkPrefix = ".tmp-link-"`
  - `func normalizeSubpath(p string) (string, error)` ("" = repository root)
  - `func tempName(prefix, siteName string) string`
  - `func siteFromTempName(prefix, entry string) (string, bool)`
  - `func replaceSymlink(target, link string) error`
  - `type syncError struct{ Reason string; Err error }`, `func newSyncError(reason string, err error) error`, `func syncReason(err error) string`, `func classifyGitError(fallback string, err error) error`

- [ ] **Step 1: Write the failing tests**

Create `pkg/syncer/layout_test.go`:

```go
package syncer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pagesv1 "github.com/kup6s/pages/pkg/apis/v1beta1"
)

func TestNormalizeSubpath(t *testing.T) {
	tests := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{in: "", want: ""},
		{in: "/", want: ""},
		{in: "/2018", want: "2018"},
		{in: "2018", want: "2018"},
		{in: "/public/", want: "public"},
		{in: "./public", want: "public"},
		{in: "/docs/public", want: "docs/public"},
		{in: "/docs//public/./", want: "docs/public"},
		{in: "../etc", wantErr: true},
		{in: "/docs/../../etc", wantErr: true},
		{in: "/-config", wantErr: true},
	}
	for _, tt := range tests {
		got, err := normalizeSubpath(tt.in)
		if (err != nil) != tt.wantErr || got != tt.want {
			t.Errorf("normalizeSubpath(%q) = (%q, %v), want (%q, err=%v)", tt.in, got, err, tt.want, tt.wantErr)
		}
	}
}

func TestSiteFromTempName(t *testing.T) {
	for _, site := range []string{"site", "archive", "archive-2018", "a-b-c"} {
		for _, prefix := range []string{tmpRepoPrefix, oldRepoPrefix, tmpLinkPrefix} {
			name := tempName(prefix, site)
			got, ok := siteFromTempName(prefix, name)
			if !ok || got != site {
				t.Errorf("siteFromTempName(%q, %q) = (%q, %v), want (%q, true)", prefix, name, got, ok, site)
			}
		}
	}
	for _, entry := range []string{"site", ".tmp-", ".tmp-site", ".tmp-site-xyzxyzxy", ".old-site-0123abc"} {
		if got, ok := siteFromTempName(tmpRepoPrefix, entry); ok {
			t.Errorf("siteFromTempName(%q) = %q, want no match", entry, got)
		}
	}
}

func TestReplaceSymlink(t *testing.T) {
	root := t.TempDir()
	target1 := filepath.Join(root, "t1")
	target2 := filepath.Join(root, "t2")
	link := filepath.Join(root, "site")

	if err := replaceSymlink(target1, link); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := replaceSymlink(target2, link); err != nil {
		t.Fatalf("replace symlink: %v", err)
	}
	if got, _ := os.Readlink(link); got != target2 {
		t.Errorf("link -> %q, want %q", got, target2)
	}

	dirSite := filepath.Join(root, "legacy")
	if err := os.MkdirAll(filepath.Join(dirSite, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := replaceSymlink(target1, dirSite); err != nil {
		t.Fatalf("replace directory: %v", err)
	}
	if got, _ := os.Readlink(dirSite); got != target1 {
		t.Errorf("legacy -> %q, want %q", got, target1)
	}

	entries, _ := os.ReadDir(root)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), tmpLinkPrefix) {
			t.Errorf("temporary link %s left behind", e.Name())
		}
	}
}

func TestClassifyGitError(t *testing.T) {
	timeout := &gitError{Command: "clone", Err: context.DeadlineExceeded}
	auth := &gitError{Command: "clone", Stderr: "fatal: Authentication failed for 'https://x/'", Err: errors.New("exit status 128")}
	other := &gitError{Command: "fetch", Stderr: "fatal: unable to access", Err: errors.New("exit status 128")}

	for _, tt := range []struct {
		err  error
		want string
	}{
		{timeout, pagesv1.SyncReasonTimeout},
		{auth, pagesv1.SyncReasonAuthFailed},
		{other, pagesv1.SyncReasonFetchFailed},
	} {
		err := classifyGitError(pagesv1.SyncReasonFetchFailed, tt.err)
		if got := syncReason(err); got != tt.want {
			t.Errorf("syncReason(%v) = %q, want %q", tt.err, got, tt.want)
		}
		if !errors.Is(err, tt.err) {
			t.Errorf("classified error does not wrap %v", tt.err)
		}
	}
	if got := syncReason(fmt.Errorf("plain")); got != pagesv1.SyncReasonSyncFailed {
		t.Errorf("syncReason(plain) = %q, want %q", got, pagesv1.SyncReasonSyncFailed)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `GOTOOLCHAIN=go1.26.0 go test ./pkg/syncer/ -run 'TestNormalizeSubpath|TestSiteFromTempName|TestReplaceSymlink|TestClassifyGitError'`
Expected: FAIL to compile, `undefined: normalizeSubpath`.

- [ ] **Step 3: Implement**

Create `pkg/syncer/layout.go`:

```go
// Package syncer - on-disk layout of sites and clones
package syncer

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
)

const (
	reposDirName  = ".repos"
	tmpRepoPrefix = ".tmp-"
	oldRepoPrefix = ".old-"
	tmpLinkPrefix = ".tmp-link-"
)

// normalizeSubpath turns spec.path into a directory relative to the
// repository root. The empty string means the root itself.
func normalizeSubpath(p string) (string, error) {
	for _, segment := range strings.Split(p, "/") {
		if segment == ".." {
			return "", fmt.Errorf("path %q must not contain '..'", p)
		}
	}
	cleaned := strings.TrimPrefix(path.Clean("/"+p), "/")
	if strings.HasPrefix(cleaned, "-") {
		return "", fmt.Errorf("path %q must not start with '-'", p)
	}
	return cleaned, nil
}

// tempName returns "<prefix><site>-<8 hex>" for temporary entries.
func tempName(prefix, siteName string) string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return prefix + siteName + "-" + hex.EncodeToString(b)
}

// siteFromTempName returns the site a temporary entry belongs to.
func siteFromTempName(prefix, entry string) (string, bool) {
	rest, ok := strings.CutPrefix(entry, prefix)
	if !ok || len(rest) < 10 || rest[len(rest)-9] != '-' {
		return "", false
	}
	if _, err := hex.DecodeString(rest[len(rest)-8:]); err != nil {
		return "", false
	}
	return rest[:len(rest)-9], true
}

// replaceSymlink points link at target with an atomic rename. A directory at
// link (layout before sparse clones) is removed first.
func replaceSymlink(target, link string) error {
	if info, err := os.Lstat(link); err == nil && info.IsDir() {
		if err := os.RemoveAll(link); err != nil {
			return fmt.Errorf("remove legacy directory %s: %w", link, err)
		}
	}
	tmp := filepath.Join(filepath.Dir(link), tempName(tmpLinkPrefix, filepath.Base(link)))
	if err := os.Symlink(target, tmp); err != nil {
		return err
	}
	if err := os.Rename(tmp, link); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
```

Create `pkg/syncer/errors.go`:

```go
// Package syncer - classified sync errors
package syncer

import (
	"context"
	"errors"

	pagesv1 "github.com/kup6s/pages/pkg/apis/v1beta1"
)

// syncError carries the Synced condition reason of a failed sync.
type syncError struct {
	Reason string
	Err    error
}

func (e *syncError) Error() string { return e.Err.Error() }
func (e *syncError) Unwrap() error { return e.Err }

func newSyncError(reason string, err error) error {
	return &syncError{Reason: reason, Err: err}
}

// syncReason returns the condition reason of err.
func syncReason(err error) string {
	var se *syncError
	if errors.As(err, &se) {
		return se.Reason
	}
	return pagesv1.SyncReasonSyncFailed
}

// classifyGitError picks Timeout or AuthFailed when they apply, else fallback.
func classifyGitError(fallback string, err error) error {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return newSyncError(pagesv1.SyncReasonTimeout, err)
	case isAuthError(err):
		return newSyncError(pagesv1.SyncReasonAuthFailed, err)
	default:
		return newSyncError(fallback, err)
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `GOTOOLCHAIN=go1.26.0 go test -race ./pkg/syncer/ -run 'TestNormalizeSubpath|TestSiteFromTempName|TestReplaceSymlink|TestClassifyGitError' -v`
Expected: PASS.

- [ ] **Step 5: Lint and commit**

```bash
make lint
git add pkg/syncer/layout.go pkg/syncer/layout_test.go pkg/syncer/errors.go
git commit -m "feat: add layout helpers and classified sync errors"
```

---

### Task 5: Clone into a temporary directory and swap it in

**Files:**
- Create: `pkg/syncer/sync.go`
- Create: `pkg/syncer/sync_test.go`
- Modify: `pkg/syncer/git.go:44-58` (add `Git` and `GitTimeout` to `Syncer`)

**Interfaces:**
- Consumes: Tasks 2–4.
- Produces:
  - `Syncer` fields `Git *gitCLI` (tests) and `GitTimeout time.Duration` (exported, used by `main`)
  - `const markerFile = "pages-syncer"`
  - `func (s *Syncer) git() *gitCLI`
  - `func (s *Syncer) repoDir(name string) string`
  - `func (s *Syncer) cloneSite(ctx context.Context, site *staticSiteData, subpath string, auth *gitAuth) error`
  - `func (s *Syncer) linkSite(name, subpath string) error`
  - `func checkSubpath(repo, subpath string) error`
  - test helpers `newTestSyncer`, `testSite`, `servedFile`, `listDir`

- [ ] **Step 1: Write the failing tests**

Create `pkg/syncer/sync_test.go`:

```go
package syncer

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"

	pagesv1 "github.com/kup6s/pages/pkg/apis/v1beta1"
)

func newTestSyncer(t *testing.T, r *testRemote, secrets ...*corev1.Secret) *Syncer {
	t.Helper()
	return &Syncer{
		SitesRoot:     t.TempDir(),
		AllowedHosts:  []string{r.Host},
		DynamicClient: &fakeDynamicClient{activeSites: []string{"site"}},
		ClientSet:     newFakeClientset(secrets...),
		Git:           &gitCLI{Timeout: time.Minute},
	}
}

func testSite(r *testRemote, path string) *staticSiteData {
	return &staticSiteData{Name: "site", Namespace: "default", Repo: r.URL, Branch: "main", Path: path}
}

// servedFile reads a file through the site symlink, as nginx would.
func servedFile(t *testing.T, s *Syncer, rel string) string {
	t.Helper()
	return readFile(t, filepath.Join(s.SitesRoot, "site", rel))
}

func listDir(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func assertNoTempEntries(t *testing.T, s *Syncer) {
	t.Helper()
	for _, dir := range []string{s.SitesRoot, filepath.Join(s.SitesRoot, reposDirName)} {
		for _, name := range listDir(t, dir) {
			if strings.HasPrefix(name, ".tmp-") || strings.HasPrefix(name, ".old-") {
				t.Errorf("temporary entry %s left in %s", name, dir)
			}
		}
	}
}

func TestCloneSite_ServesSubpathThroughSymlink(t *testing.T) {
	r := newTestRemote(t, archiveFiles, remoteOptions{})
	s := newTestSyncer(t, r)

	if err := s.cloneSite(context.Background(), testSite(r, "/2018"), "2018", nil); err != nil {
		t.Fatalf("cloneSite: %v", err)
	}
	if got := servedFile(t, s, "index.html"); got != "2018 v1" {
		t.Errorf("served index.html = %q, want 2018 v1", got)
	}
	if link, _ := os.Readlink(filepath.Join(s.SitesRoot, "site")); link != filepath.Join(s.repoDir("site"), "2018") {
		t.Errorf("site -> %q", link)
	}
	if _, err := os.Stat(filepath.Join(s.repoDir("site"), ".git", markerFile)); err != nil {
		t.Errorf("marker missing: %v", err)
	}
	assertNoTempEntries(t, s)
}

func TestCloneSite_RootPathLinksRepository(t *testing.T) {
	r := newTestRemote(t, archiveFiles, remoteOptions{})
	s := newTestSyncer(t, r)

	if err := s.cloneSite(context.Background(), testSite(r, "/"), "", nil); err != nil {
		t.Fatalf("cloneSite: %v", err)
	}
	if got := servedFile(t, s, "index.html"); got != "root" {
		t.Errorf("served index.html = %q, want root", got)
	}
}

func TestCloneSite_ReplacesLegacyDirectory(t *testing.T) {
	r := newTestRemote(t, archiveFiles, remoteOptions{})
	s := newTestSyncer(t, r)
	legacy := filepath.Join(s.SitesRoot, "site")
	if err := os.MkdirAll(filepath.Join(legacy, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := s.cloneSite(context.Background(), testSite(r, "/"), "", nil); err != nil {
		t.Fatalf("cloneSite: %v", err)
	}
	if info, err := os.Lstat(legacy); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Errorf("site is not a symlink after migration (err=%v)", err)
	}
}

func TestCloneSite_FailureKeepsPreviousClone(t *testing.T) {
	r := newTestRemote(t, archiveFiles, remoteOptions{})
	s := newTestSyncer(t, r)
	ctx := context.Background()
	if err := s.cloneSite(ctx, testSite(r, "/2018"), "2018", nil); err != nil {
		t.Fatalf("first clone: %v", err)
	}

	broken := testSite(r, "/2018")
	broken.Branch = "does-not-exist"
	err := s.cloneSite(ctx, broken, "2018", nil)
	if syncReason(err) != pagesv1.SyncReasonCloneFailed {
		t.Fatalf("err = %v (reason %q), want CloneFailed", err, syncReason(err))
	}
	if got := servedFile(t, s, "index.html"); got != "2018 v1" {
		t.Errorf("served content changed to %q after failed clone", got)
	}
	assertNoTempEntries(t, s)
}

func TestCloneSite_MissingSubpath(t *testing.T) {
	r := newTestRemote(t, archiveFiles, remoteOptions{})
	s := newTestSyncer(t, r)

	err := s.cloneSite(context.Background(), testSite(r, "/1999"), "1999", nil)
	if syncReason(err) != pagesv1.SyncReasonSubpathMissing {
		t.Fatalf("err = %v (reason %q), want SubpathMissing", err, syncReason(err))
	}
	if _, err := os.Lstat(filepath.Join(s.SitesRoot, "site")); !os.IsNotExist(err) {
		t.Error("site link created for a missing subpath")
	}
	assertNoTempEntries(t, s)
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `GOTOOLCHAIN=go1.26.0 go test ./pkg/syncer/ -run TestCloneSite`
Expected: FAIL to compile, `s.cloneSite undefined` and `unknown field Git`.

- [ ] **Step 3: Add the Syncer fields**

In `pkg/syncer/git.go`, add to the `Syncer` struct after `AllowedHosts`:

```go
	// GitTimeout bounds every git invocation; zero means no limit.
	GitTimeout time.Duration

	// Git overrides the git wrapper (tests); nil uses git from PATH.
	Git *gitCLI
```

- [ ] **Step 4: Implement cloneSite**

Create `pkg/syncer/sync.go`:

```go
// Package syncer - sync flow of a single site
package syncer

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"sigs.k8s.io/controller-runtime/pkg/log"

	pagesv1 "github.com/kup6s/pages/pkg/apis/v1beta1"
)

// markerFile marks a complete clone made by this syncer. Clones without it,
// including those made by go-git, are replaced.
const markerFile = "pages-syncer"

func (s *Syncer) git() *gitCLI {
	if s.Git != nil {
		return s.Git
	}
	return &gitCLI{Timeout: s.GitTimeout}
}

func (s *Syncer) repoDir(name string) string {
	return filepath.Join(s.SitesRoot, reposDirName, name)
}

// cloneSite clones into a temporary directory and swaps the result into
// place, so an interrupted clone never looks like a valid one.
func (s *Syncer) cloneSite(ctx context.Context, site *staticSiteData, subpath string, auth *gitAuth) error {
	reposDir := filepath.Join(s.SitesRoot, reposDirName)
	if err := os.MkdirAll(reposDir, 0o755); err != nil {
		return newSyncError(pagesv1.SyncReasonCloneFailed, err)
	}
	tmp := filepath.Join(reposDir, tempName(tmpRepoPrefix, site.Name))
	log.FromContext(ctx).Info("Cloning repository", "site", site.Name, "repo", site.Repo, "path", subpath)

	if err := s.git().clone(ctx, site.Repo, site.Branch, subpath, tmp, auth); err != nil {
		_ = os.RemoveAll(tmp)
		return classifyGitError(pagesv1.SyncReasonCloneFailed, err)
	}
	if err := checkSubpath(tmp, subpath); err != nil {
		_ = os.RemoveAll(tmp)
		return err
	}
	if err := os.WriteFile(filepath.Join(tmp, ".git", markerFile), []byte("1\n"), 0o644); err != nil {
		_ = os.RemoveAll(tmp)
		return newSyncError(pagesv1.SyncReasonCloneFailed, err)
	}
	return s.swapClone(ctx, site.Name, subpath, tmp)
}

// swapClone moves a finished clone to .repos/<name> and points the site at it.
func (s *Syncer) swapClone(ctx context.Context, name, subpath, tmp string) error {
	final := s.repoDir(name)
	old := ""
	if _, err := os.Lstat(final); err == nil {
		old = filepath.Join(filepath.Dir(final), tempName(oldRepoPrefix, name))
		if err := os.Rename(final, old); err != nil {
			_ = os.RemoveAll(tmp)
			return newSyncError(pagesv1.SyncReasonCloneFailed, err)
		}
	}
	if err := os.Rename(tmp, final); err != nil {
		_ = os.RemoveAll(tmp)
		if old != "" {
			_ = os.Rename(old, final)
		}
		return newSyncError(pagesv1.SyncReasonCloneFailed, err)
	}
	if err := s.linkSite(name, subpath); err != nil {
		return newSyncError(pagesv1.SyncReasonSyncFailed, err)
	}
	if old != "" {
		if err := os.RemoveAll(old); err != nil {
			log.FromContext(ctx).Error(err, "Failed to remove previous clone", "path", old)
		}
	}
	return nil
}

// linkSite points /sites/<name> at the served directory of the clone.
func (s *Syncer) linkSite(name, subpath string) error {
	target := filepath.Join(s.repoDir(name), subpath)
	link := filepath.Join(s.SitesRoot, name)
	if current, err := os.Readlink(link); err == nil && current == target {
		return nil
	}
	return replaceSymlink(target, link)
}

// checkSubpath fails when the served directory does not exist in the clone.
func checkSubpath(repo, subpath string) error {
	if subpath == "" {
		return nil
	}
	info, err := os.Stat(filepath.Join(repo, subpath))
	if err != nil || !info.IsDir() {
		return newSyncError(pagesv1.SyncReasonSubpathMissing,
			fmt.Errorf("subpath %q does not exist in repository", subpath))
	}
	return nil
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `GOTOOLCHAIN=go1.26.0 go test -race ./pkg/syncer/ -run TestCloneSite -v`
Expected: PASS.

- [ ] **Step 6: Lint and commit**

```bash
make lint
git add pkg/syncer/sync.go pkg/syncer/sync_test.go pkg/syncer/git.go
git commit -m "feat: clone sites into a temporary directory and swap them in"
```

---

### Task 6: Sync flow on the git CLI, go-git removed

**Files:**
- Modify: `pkg/syncer/sync.go` (add `syncSite`, `syncSiteContent`, `siteAuth`, `syncRepo`, `recloneSite`, `cloneUsable`, `cloneOvergrown`, `dirSize`)
- Modify: `pkg/syncer/git.go`: delete `syncSite`, `pullRepo`, `setupSubpath` (lines 145-306 of the current file) and the go-git imports; in `SyncAll` remove the `s.updateStatus(ctx, site, "Error", err.Error(), "")` line
- Modify: `pkg/syncer/git_test.go`: delete `TestSetupSubpath`, `TestSyncSite_PullFailure`, `TestSyncSite_SubpathNotExist`, `TestSyncSite_CorruptedRepo`, `TestSyncSite_NonFastForwardUpdate`, `TestSyncSite_PullWithAuth`; in `TestSyncSite_CloneFailure`, `TestSyncSite_AuthWithUsername`, `TestSyncSite_AuthWithDefaultUsername` replace the `strings.Contains(err.Error(), "git clone failed")` check with `syncReason(err) != pagesv1.SyncReasonCloneFailed`; remove the go-git imports
- Modify: `pkg/syncer/sync_test.go` (new tests)
- Modify: `go.mod`, `go.sum` (`go mod tidy`)

**Interfaces:**
- Consumes: Task 5.
- Produces: `func (s *Syncer) syncSite(ctx context.Context, site *staticSiteData) error` (same signature as before), `func (s *Syncer) syncRepo(ctx context.Context, site *staticSiteData, subpath string, auth *gitAuth) (string, error)`, `func (s *Syncer) siteAuth(ctx context.Context, site *staticSiteData) (*gitAuth, error)`, package var `recloneSlackBytes int64`.

- [ ] **Step 1: Write the failing tests**

Append to `pkg/syncer/sync_test.go` (add `pagesv1` usage already imported; add helpers):

```go
// markClone drops a file into .git that survives updates but not re-clones.
func markClone(t *testing.T, s *Syncer) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(s.repoDir("site"), ".git", "sentinel"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
}

func cloneReplaced(s *Syncer) bool {
	_, err := os.Stat(filepath.Join(s.repoDir("site"), ".git", "sentinel"))
	return os.IsNotExist(err)
}

func mustSync(t *testing.T, s *Syncer, site *staticSiteData) {
	t.Helper()
	if err := s.syncSite(context.Background(), site); err != nil {
		t.Fatalf("syncSite: %v", err)
	}
}

func TestSyncSite_UpdatesInPlaceAfterCommitAndForcePush(t *testing.T) {
	r := newTestRemote(t, archiveFiles, remoteOptions{})
	s := newTestSyncer(t, r)
	site := testSite(r, "/2018")
	mustSync(t, s, site)
	markClone(t, s)

	r.commit(map[string]string{"2018/index.html": "2018 v2"}, "v2")
	mustSync(t, s, site)
	if got := servedFile(t, s, "index.html"); got != "2018 v2" {
		t.Errorf("after commit: %q", got)
	}

	r.amend(map[string]string{"2018/index.html": "2018 v3"}, "v3")
	mustSync(t, s, site)
	if got := servedFile(t, s, "index.html"); got != "2018 v3" {
		t.Errorf("after force-push: %q", got)
	}
	if cloneReplaced(s) {
		t.Error("updates re-cloned the site, want in-place update")
	}
}

func TestSyncSite_FetchFailureKeepsServedContent(t *testing.T) {
	r := newTestRemote(t, archiveFiles, remoteOptions{})
	s := newTestSyncer(t, r)
	site := testSite(r, "/2018")
	mustSync(t, s, site)

	r.close()
	err := s.syncSite(context.Background(), site)
	if syncReason(err) != pagesv1.SyncReasonFetchFailed {
		t.Fatalf("err = %v (reason %q), want FetchFailed", err, syncReason(err))
	}
	if got := servedFile(t, s, "index.html"); got != "2018 v1" {
		t.Errorf("served content = %q after failed fetch", got)
	}
}

func TestSyncSite_RecloneWhenSpecChanges(t *testing.T) {
	r := newTestRemote(t, archiveFiles, remoteOptions{})
	other := newTestRemote(t, map[string]string{"2018/index.html": "other repo"}, remoteOptions{})
	r.pushBranch("preview", map[string]string{"2018/index.html": "preview"})
	s := newTestSyncer(t, r)
	s.AllowedHosts = []string{r.Host, other.Host}
	site := testSite(r, "/2018")
	mustSync(t, s, site)

	for _, change := range []struct {
		name   string
		apply  func()
		served string
	}{
		{"path", func() { site.Path = "/2005" }, "2005 v1"},
		{"branch", func() { site.Path = "/2018"; site.Branch = "preview" }, "preview"},
		{"repo", func() { site.Repo = other.URL; site.Branch = "main" }, "other repo"},
	} {
		markClone(t, s)
		change.apply()
		mustSync(t, s, site)
		if !cloneReplaced(s) {
			t.Errorf("%s change did not re-clone", change.name)
		}
		if got := servedFile(t, s, "index.html"); got != change.served {
			t.Errorf("%s change: served %q, want %q", change.name, got, change.served)
		}
	}
}

func TestSyncSite_RecloneBrokenClone(t *testing.T) {
	r := newTestRemote(t, archiveFiles, remoteOptions{})
	s := newTestSyncer(t, r)
	site := testSite(r, "/2018")
	mustSync(t, s, site)
	markClone(t, s)

	// The state an interrupted clone used to leave: .git exists, HEAD points nowhere.
	head := filepath.Join(s.repoDir("site"), ".git", "HEAD")
	if err := os.WriteFile(head, []byte("ref: refs/heads/missing\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustSync(t, s, site)
	if !cloneReplaced(s) {
		t.Error("broken clone was not replaced")
	}
}

func TestSyncSite_RecloneLegacyClone(t *testing.T) {
	r := newTestRemote(t, archiveFiles, remoteOptions{})
	s := newTestSyncer(t, r)
	if err := os.MkdirAll(filepath.Join(s.SitesRoot, reposDirName), 0o755); err != nil {
		t.Fatal(err)
	}
	// A full clone without marker, as go-git left it.
	runGit(t, s.SitesRoot, "clone", "--quiet", "--branch", "main", r.URL, s.repoDir("site"))
	markClone(t, s)

	mustSync(t, s, testSite(r, "/2018"))
	if !cloneReplaced(s) {
		t.Error("legacy clone without marker was not replaced")
	}
}

func TestSyncSite_RecloneOvergrownClone(t *testing.T) {
	r := newTestRemote(t, archiveFiles, remoteOptions{})
	s := newTestSyncer(t, r)
	site := testSite(r, "/2018")
	mustSync(t, s, site)
	markClone(t, s)

	saved := recloneSlackBytes
	recloneSlackBytes = -1 << 40 // every clone counts as overgrown
	t.Cleanup(func() { recloneSlackBytes = saved })

	r.commit(map[string]string{"2018/index.html": "2018 v2"}, "v2")
	mustSync(t, s, site)
	if !cloneReplaced(s) {
		t.Error("overgrown clone was not replaced")
	}
	if got := servedFile(t, s, "index.html"); got != "2018 v2" {
		t.Errorf("served %q", got)
	}
}

func TestSyncSite_PrivateRepository(t *testing.T) {
	r := newTestRemote(t, archiveFiles, remoteOptions{User: "deploy", Password: "token"})
	secret := newTestSecret("default", "git-creds", map[string][]byte{
		"username": []byte("deploy"), "password": []byte("token"),
	})
	s := newTestSyncer(t, r, secret)

	site := testSite(r, "/2018")
	site.SecretRef = &secretRef{Name: "git-creds", Key: "password"}
	mustSync(t, s, site)
	markClone(t, s)
	r.commit(map[string]string{"2018/index.html": "2018 v2"}, "v2")
	mustSync(t, s, site)
	if got := servedFile(t, s, "index.html"); got != "2018 v2" || cloneReplaced(s) {
		t.Errorf("private update: served %q, replaced=%v", got, cloneReplaced(s))
	}

	anonymous := testSite(r, "/2005")
	anonymous.Name = "anon"
	if err := s.syncSite(context.Background(), anonymous); syncReason(err) != pagesv1.SyncReasonAuthFailed {
		t.Errorf("without secret: err = %v (reason %q), want AuthFailed", err, syncReason(err))
	}
}

func TestSyncSite_InvalidPath(t *testing.T) {
	r := newTestRemote(t, archiveFiles, remoteOptions{})
	s := newTestSyncer(t, r)
	err := s.syncSite(context.Background(), testSite(r, "/../etc"))
	if syncReason(err) != pagesv1.SyncReasonInvalidSpec {
		t.Errorf("err = %v (reason %q), want InvalidSpec", err, syncReason(err))
	}
}

// Review focus 1
func TestSyncSite_BranchWithSlashUpdatesInPlace(t *testing.T) {
	r := newTestRemote(t, archiveFiles, remoteOptions{})
	r.pushBranch("release/2026", map[string]string{"2018/index.html": "release v1"})
	s := newTestSyncer(t, r)
	site := testSite(r, "/2018")
	site.Branch = "release/2026"
	mustSync(t, s, site)
	markClone(t, s)

	mustSync(t, s, site)
	if cloneReplaced(s) {
		t.Error("branch with slash re-cloned on every sync")
	}
	if got := servedFile(t, s, "index.html"); got != "release v1" {
		t.Errorf("served %q", got)
	}
}

// Review focus 2
func TestSyncSite_PathSpellingsDoNotReclone(t *testing.T) {
	for _, path := range []string{"/docs/public/", "./docs/public", "docs/public", "/docs/public"} {
		t.Run(path, func(t *testing.T) {
			r := newTestRemote(t, archiveFiles, remoteOptions{})
			s := newTestSyncer(t, r)
			site := testSite(r, path)
			mustSync(t, s, site)
			markClone(t, s)
			mustSync(t, s, site)
			if cloneReplaced(s) {
				t.Errorf("path %q re-cloned on the second sync", path)
			}
			if got := servedFile(t, s, "index.html"); got != "docs v1" {
				t.Errorf("served %q", got)
			}
		})
	}
}

// Review focus 3
func TestSyncSite_ServerWithoutFilterUpdatesInPlace(t *testing.T) {
	r := newTestRemote(t, archiveFiles, remoteOptions{NoFilter: true})
	s := newTestSyncer(t, r)
	site := testSite(r, "/2018")
	mustSync(t, s, site)
	markClone(t, s)

	r.commit(map[string]string{"2018/index.html": "2018 v2"}, "v2")
	mustSync(t, s, site)
	if cloneReplaced(s) {
		t.Error("server without partial clone support caused a re-clone")
	}
	if got := servedFile(t, s, "index.html"); got != "2018 v2" {
		t.Errorf("served %q", got)
	}
}

// Review focus 4
func TestSyncSite_MissingBranchKeepsServedContent(t *testing.T) {
	r := newTestRemote(t, archiveFiles, remoteOptions{})
	s := newTestSyncer(t, r)
	site := testSite(r, "/2018")
	mustSync(t, s, site)

	site.Branch = "gone"
	err := s.syncSite(context.Background(), site)
	if syncReason(err) != pagesv1.SyncReasonCloneFailed {
		t.Fatalf("err = %v (reason %q), want CloneFailed", err, syncReason(err))
	}
	if !strings.Contains(err.Error(), "gone") {
		t.Errorf("error %q does not name the branch", err)
	}
	if got := servedFile(t, s, "index.html"); got != "2018 v1" {
		t.Errorf("served content changed to %q", got)
	}
	assertNoTempEntries(t, s)
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `GOTOOLCHAIN=go1.26.0 go test ./pkg/syncer/ -run TestSyncSite`
Expected: FAIL. The go-git `syncSite` still runs: the new tests fail on unexpected reasons (`SyncFailed` instead of `FetchFailed`, `CloneFailed`, `AuthFailed`, `InvalidSpec`) and on wrong served content, because go-git clones the whole repository into `/sites/site` without the new layout.

- [ ] **Step 3: Implement the flow**

Append to `pkg/syncer/sync.go` (add imports `io/fs`):

```go
// recloneSlackBytes is the allowance on top of twice the checkout size.
// git gc never prunes promisor packs, so an overgrown clone is replaced.
var recloneSlackBytes int64 = 50 << 20

// syncSite brings one site to the head of its branch and reports the result.
func (s *Syncer) syncSite(ctx context.Context, site *staticSiteData) error {
	commit, err := s.syncSiteContent(ctx, site)
	if err != nil {
		s.updateStatus(ctx, site, "Error", err.Error(), "")
		return err
	}
	s.updateStatus(ctx, site, "Ready", "Synced successfully", commit)
	log.FromContext(ctx).Info("Sync complete", "site", site.Name, "commit", commit)
	return nil
}

func (s *Syncer) syncSiteContent(ctx context.Context, site *staticSiteData) (string, error) {
	if err := s.validateRepoURL(site.Repo); err != nil {
		return "", newSyncError(pagesv1.SyncReasonInvalidSpec, fmt.Errorf("repo URL validation failed: %w", err))
	}
	subpath, err := normalizeSubpath(site.Path)
	if err != nil {
		return "", newSyncError(pagesv1.SyncReasonInvalidSpec, err)
	}
	auth, err := s.siteAuth(ctx, site)
	if err != nil {
		return "", newSyncError(pagesv1.SyncReasonAuthFailed, fmt.Errorf("failed to get git credentials: %w", err))
	}
	return s.syncRepo(ctx, site, subpath, auth)
}

// siteAuth reads the site's git credentials; nil means anonymous access.
func (s *Syncer) siteAuth(ctx context.Context, site *staticSiteData) (*gitAuth, error) {
	if site.SecretRef == nil {
		return nil, nil
	}
	password, err := s.getSecretValue(ctx, site.Namespace, site.SecretRef.Name, site.SecretRef.Key)
	if err != nil {
		return nil, err
	}
	username, _ := s.getSecretValue(ctx, site.Namespace, site.SecretRef.Name, "username")
	if username == "" {
		username = "git"
	}
	return &gitAuth{Username: username, Password: password}, nil
}

// syncRepo updates a usable clone in place and replaces anything else.
func (s *Syncer) syncRepo(ctx context.Context, site *staticSiteData, subpath string, auth *gitAuth) (string, error) {
	logger := log.FromContext(ctx)
	dir := s.repoDir(site.Name)
	if !s.cloneUsable(ctx, dir, site, subpath) {
		return s.recloneSite(ctx, site, subpath, auth)
	}

	before, _ := s.git().head(ctx, dir)
	if err := s.git().fetch(ctx, dir, site.Repo, site.Branch, auth); err != nil {
		return "", classifyGitError(pagesv1.SyncReasonFetchFailed, err)
	}
	if err := s.git().resetHard(ctx, dir, site.Repo, site.Branch, auth); err != nil {
		logger.Info("Reset failed, cloning again", "site", site.Name, "error", err.Error())
		return s.recloneSite(ctx, site, subpath, auth)
	}
	after, err := s.git().head(ctx, dir)
	if err != nil {
		return s.recloneSite(ctx, site, subpath, auth)
	}
	if after != before && cloneOvergrown(dir) {
		logger.Info("Clone outgrew its checkout, cloning again", "site", site.Name)
		return s.recloneSite(ctx, site, subpath, auth)
	}
	if err := checkSubpath(dir, subpath); err != nil {
		return "", err
	}
	if err := s.linkSite(site.Name, subpath); err != nil {
		return "", newSyncError(pagesv1.SyncReasonSyncFailed, err)
	}
	return after, nil
}

func (s *Syncer) recloneSite(ctx context.Context, site *staticSiteData, subpath string, auth *gitAuth) (string, error) {
	if err := s.cloneSite(ctx, site, subpath, auth); err != nil {
		return "", err
	}
	commit, err := s.git().head(ctx, s.repoDir(site.Name))
	if err != nil {
		return "", newSyncError(pagesv1.SyncReasonCloneFailed, err)
	}
	return commit, nil
}

// cloneUsable reports whether dir is a complete clone of the site's spec.
func (s *Syncer) cloneUsable(ctx context.Context, dir string, site *staticSiteData, subpath string) bool {
	if _, err := os.Stat(filepath.Join(dir, ".git", markerFile)); err != nil {
		return false
	}
	info, err := s.git().inspect(ctx, dir)
	if err != nil {
		return false
	}
	return info.OriginURL == site.Repo && info.Branch == site.Branch && info.SparsePath == subpath
}

func cloneOvergrown(dir string) bool {
	gitSize := dirSize(filepath.Join(dir, ".git"))
	checkout := dirSize(dir) - gitSize
	return gitSize > 2*checkout+recloneSlackBytes
}

func dirSize(root string) int64 {
	var size int64
	_ = filepath.WalkDir(root, func(_ string, d fs.DirEntry, err error) error {
		if err != nil || !d.Type().IsRegular() {
			return nil
		}
		if info, err := d.Info(); err == nil {
			size += info.Size()
		}
		return nil
	})
	return size
}
```

In `pkg/syncer/git.go`: delete `syncSite`, `pullRepo` and `setupSubpath`, remove the go-git imports (`github.com/go-git/go-git/v5`, `.../config`, `.../plumbing`, `.../plumbing/transport/http`), and in `SyncAll` replace

```go
		if err := s.syncSite(ctx, site); err != nil {
			logger.Error(err, "Failed to sync site", "name", site.Name)
			s.updateStatus(ctx, site, "Error", err.Error(), "")
			continue
		}
```

with

```go
		if err := s.syncSite(ctx, site); err != nil {
			logger.Error(err, "Failed to sync site", "name", site.Name)
		}
```

Then apply the `git_test.go` edits listed under **Files** and run:

```bash
GOTOOLCHAIN=go1.26.0 go mod tidy
grep -c go-git go.mod   # expected: 0
```

- [ ] **Step 4: Run all syncer tests**

Run: `GOTOOLCHAIN=go1.26.0 go test -race ./pkg/syncer/`
Expected: PASS.

- [ ] **Step 5: Lint and commit**

```bash
make lint
git add pkg/syncer/ go.mod go.sum
git commit -m "feat: sync sites with sparse partial clones via the git CLI

Replaces go-git. Each site keeps its own clone, updated in place and
replaced when it is broken, outgrown, or no longer matches the spec.
Fixes the endless 'reference not found' loop after interrupted clones."
```

---

### Task 7: Per-site locks and leftover cleanup

**Files:**
- Create: `pkg/syncer/leftovers.go`, `pkg/syncer/leftovers_test.go`
- Modify: `pkg/syncer/git.go` (`Syncer` gains `locks siteLocks`; `RunLoop`, `Cleanup`, `DeleteSite`)
- Modify: `pkg/syncer/sync.go` (`syncSite` takes the site lock)

**Interfaces:**
- Consumes: `siteFromTempName`, `removePathOrSymlink`, `tmpRepoPrefix`, `oldRepoPrefix`, `tmpLinkPrefix`.
- Produces: `type siteLocks struct`, `func (l *siteLocks) get(name string) *sync.Mutex`, `func (s *Syncer) removeLeftovers(ctx context.Context)`.

- [ ] **Step 1: Write the failing tests**

Create `pkg/syncer/leftovers_test.go`:

```go
package syncer

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func mkdirs(t *testing.T, paths ...string) {
	t.Helper()
	for _, p := range paths {
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRemoveLeftovers_RemovesTemporaryEntries(t *testing.T) {
	root := t.TempDir()
	repos := filepath.Join(root, reposDirName)
	mkdirs(t, filepath.Join(repos, ".tmp-site-0123abcd"), filepath.Join(repos, ".old-site-0123abcd"), filepath.Join(repos, "site"))
	if err := os.Symlink(filepath.Join(repos, "site"), filepath.Join(root, ".tmp-link-site-0123abcd")); err != nil {
		t.Fatal(err)
	}
	s := &Syncer{SitesRoot: root}

	s.removeLeftovers(context.Background())

	if got := listDir(t, repos); !slices.Equal(got, []string{"site"}) {
		t.Errorf(".repos = %v, want [site]", got)
	}
	if got := listDir(t, root); !slices.Equal(got, []string{reposDirName}) {
		t.Errorf("sites root = %v, want [.repos]", got)
	}
}

// Review focus 5
func TestRemoveLeftovers_DistinguishesSimilarNames(t *testing.T) {
	root := t.TempDir()
	repos := filepath.Join(root, reposDirName)
	busy := filepath.Join(repos, ".tmp-archive-0123abcd")
	idle := filepath.Join(repos, ".tmp-archive-2018-0123abcd")
	mkdirs(t, busy, idle)
	s := &Syncer{SitesRoot: root}

	lock := s.locks.get("archive")
	lock.Lock()
	s.removeLeftovers(context.Background())
	lock.Unlock()

	if _, err := os.Stat(busy); err != nil {
		t.Error("temp clone of the running sync of 'archive' was removed")
	}
	if _, err := os.Stat(idle); !os.IsNotExist(err) {
		t.Error("leftover of 'archive-2018' was kept")
	}
}

func TestCleanup_KeepsDotEntries(t *testing.T) {
	root := t.TempDir()
	link := filepath.Join(root, ".tmp-link-busy-0123abcd")
	if err := os.Symlink(root, link); err != nil {
		t.Fatal(err)
	}
	s := &Syncer{SitesRoot: root, DynamicClient: &fakeDynamicClient{activeSites: []string{"busy"}}}
	lock := s.locks.get("busy")
	lock.Lock()
	defer lock.Unlock()

	if err := s.Cleanup(context.Background()); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if _, err := os.Lstat(link); err != nil {
		t.Error("Cleanup removed the temporary link of a running sync")
	}
}

func TestSyncSite_WaitsForRunningSyncOfSameSite(t *testing.T) {
	r := newTestRemote(t, archiveFiles, remoteOptions{})
	s := newTestSyncer(t, r)
	lock := s.locks.get("site")
	lock.Lock()

	done := make(chan error, 1)
	go func() { done <- s.syncSite(context.Background(), testSite(r, "/2018")) }()

	time.Sleep(200 * time.Millisecond)
	if _, err := os.Stat(s.repoDir("site")); !os.IsNotExist(err) {
		t.Error("syncSite cloned while another sync held the site lock")
	}
	lock.Unlock()
	if err := <-done; err != nil {
		t.Fatalf("syncSite: %v", err)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `GOTOOLCHAIN=go1.26.0 go test ./pkg/syncer/ -run 'TestRemoveLeftovers|TestCleanup_KeepsDotEntries|TestSyncSite_WaitsForRunningSyncOfSameSite'`
Expected: FAIL to compile, `s.locks undefined`.

- [ ] **Step 3: Implement**

Create `pkg/syncer/leftovers.go`:

```go
// Package syncer - per-site locking and cleanup of interrupted work
package syncer

import (
	"context"
	"os"
	"path/filepath"
	"sync"

	"sigs.k8s.io/controller-runtime/pkg/log"
)

// siteLocks serialises work on one site directory. Directories are keyed by
// name only (see #158), so the lock is too.
type siteLocks struct {
	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

func (l *siteLocks) get(name string) *sync.Mutex {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.locks == nil {
		l.locks = map[string]*sync.Mutex{}
	}
	m, ok := l.locks[name]
	if !ok {
		m = &sync.Mutex{}
		l.locks[name] = m
	}
	return m
}

// removeLeftovers deletes temporary clones, previous clones and temporary
// links of interrupted syncs. Entries of a site that is syncing are kept.
func (s *Syncer) removeLeftovers(ctx context.Context) {
	s.removeTempEntries(ctx, filepath.Join(s.SitesRoot, reposDirName), tmpRepoPrefix, oldRepoPrefix)
	s.removeTempEntries(ctx, s.SitesRoot, tmpLinkPrefix)
}

func (s *Syncer) removeTempEntries(ctx context.Context, dir string, prefixes ...string) {
	logger := log.FromContext(ctx)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		for _, prefix := range prefixes {
			name, ok := siteFromTempName(prefix, entry.Name())
			if !ok {
				continue
			}
			lock := s.locks.get(name)
			if lock.TryLock() {
				p := filepath.Join(dir, entry.Name())
				if err := removePathOrSymlink(p); err != nil {
					logger.Error(err, "Failed to remove leftover", "path", p)
				} else {
					logger.Info("Removed leftover", "path", p)
				}
				lock.Unlock()
			}
			break
		}
	}
}
```

In `pkg/syncer/git.go`:
- add to `Syncer`: `locks siteLocks` (unexported, after `Git`).
- `RunLoop`: before `// Initial sync`, add `s.removeLeftovers(ctx)`.
- `Cleanup`: right after loading the list, add `s.removeLeftovers(ctx)`; in both directory loops replace `if name == ".repos" { continue }` (first loop) with `if strings.HasPrefix(name, ".") { continue }` and add the same check at the top of the `.repos` loop. Update the comment to: `// Dot entries are .repos and temporary entries, handled by removeLeftovers`.
- `DeleteSite`: after the log line add

```go
	lock := s.locks.get(name)
	lock.Lock()
	defer lock.Unlock()
```

In `pkg/syncer/sync.go`, in `syncSite`, add as first statements:

```go
	lock := s.locks.get(site.Name)
	lock.Lock()
	defer lock.Unlock()
```

- [ ] **Step 4: Run all syncer tests**

Run: `GOTOOLCHAIN=go1.26.0 go test -race ./pkg/syncer/`
Expected: PASS (including the existing `TestCleanup`).

- [ ] **Step 5: Lint and commit**

```bash
make lint
git add pkg/syncer/
git commit -m "feat: lock syncs per site and remove leftovers of interrupted syncs"
```

---

### Task 8: Synced condition and events from the syncer

**Files:**
- Create: `pkg/syncer/status.go`, `pkg/syncer/status_test.go`
- Modify: `pkg/syncer/git.go` (delete `statusPatch`, `statusPatchData`, `updateStatus`; add `Recorder events.EventRecorder` to `Syncer`)
- Modify: `pkg/syncer/sync.go` (`syncSite` reports via `reportStatus`; `syncRepo` marks `Syncing` before a clone)
- Modify: `pkg/syncer/git_test.go` (delete `TestUpdateStatusJSONEscaping`, `TestUpdateStatus_PatchError`)
- Modify: `cmd/syncer/main.go` (event recorder)

**Interfaces:**
- Consumes: `syncReason`, `pagesv1.ConditionSynced`, `pagesv1.SyncReason*`.
- Produces: `Syncer.Recorder events.EventRecorder` (`k8s.io/client-go/tools/events`); `func (s *Syncer) setSynced(ctx context.Context, site *staticSiteData, cond metav1.Condition, commit string) error`; `func (s *Syncer) reportStatus(ctx context.Context, site *staticSiteData, cond metav1.Condition, commit string)`; `func readConditions(obj *unstructured.Unstructured) ([]metav1.Condition, error)`; `func writeConditions(obj *unstructured.Unstructured, conditions []metav1.Condition) error`.

- [ ] **Step 1: Write the failing tests**

Create `pkg/syncer/status_test.go`:

```go
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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `GOTOOLCHAIN=go1.26.0 go test ./pkg/syncer/ -run 'TestSetSynced|TestSyncSite_Reports'`
Expected: FAIL to compile, `undefined: writeConditions`.

- [ ] **Step 3: Implement the status writer**

Create `pkg/syncer/status.go`:

```go
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
```

- [ ] **Step 4: Wire it into the flow**

In `pkg/syncer/git.go`: delete `statusPatch`, `statusPatchData` and `updateStatus`; drop now-unused imports (`encoding/json`, `k8s.io/apimachinery/pkg/types`); add to `Syncer`:

```go
	// Recorder emits SyncFailed and SyncRecovered events; nil disables events.
	Recorder events.EventRecorder
```

with import `"k8s.io/client-go/tools/events"`.

In `pkg/syncer/sync.go`, replace the body of `syncSite` after the lock with:

```go
	commit, err := s.syncSiteContent(ctx, site)
	if err != nil {
		s.reportStatus(ctx, site, metav1.Condition{
			Status: metav1.ConditionFalse, Reason: syncReason(err), Message: err.Error(),
		}, "")
		return err
	}
	s.reportStatus(ctx, site, metav1.Condition{
		Status: metav1.ConditionTrue, Reason: pagesv1.SyncReasonSynced, Message: "Synced " + commit,
	}, commit)
	log.FromContext(ctx).Info("Sync complete", "site", site.Name, "commit", commit)
	return nil
```

and at the start of `recloneSite` add:

```go
	s.reportStatus(ctx, site, metav1.Condition{
		Status: metav1.ConditionUnknown, Reason: pagesv1.SyncReasonSyncing, Message: "Cloning " + site.Repo,
	}, "")
```

(import `metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"`).

In `pkg/syncer/git_test.go` delete `TestUpdateStatusJSONEscaping` and `TestUpdateStatus_PatchError`.

In `cmd/syncer/main.go`, after `ctx, cancel := context.WithCancel(context.Background())` and `defer cancel()`, add:

```go
	broadcaster := events.NewBroadcaster(&events.EventSinkImpl{Interface: clientset.EventsV1()})
	broadcaster.StartRecordingToSink(ctx.Done())
	defer broadcaster.Shutdown()
	s.Recorder = broadcaster.NewRecorder(clientgoscheme.Scheme, "pages-syncer")
```

with imports `"k8s.io/client-go/tools/events"` and `clientgoscheme "k8s.io/client-go/kubernetes/scheme"`.

- [ ] **Step 5: Run all tests**

Run: `GOTOOLCHAIN=go1.26.0 go test -race ./... && go build ./...`
Expected: PASS, build succeeds.

- [ ] **Step 6: Lint and commit**

```bash
make lint
git add pkg/syncer/ cmd/syncer/main.go
git commit -m "feat: report sync results as Synced condition and events"
```

---

### Task 9: Free-space guard

**Files:**
- Create: `pkg/syncer/diskspace.go`, `pkg/syncer/diskspace_test.go`
- Modify: `pkg/syncer/git.go` (`Syncer.MinFreePercent float64`)
- Modify: `pkg/syncer/sync.go` (check before clone and update; watchdog in `cloneSite`)

**Interfaces:**
- Produces: `Syncer.MinFreePercent float64` (0 disables); package vars `statFS func(path string) (free, total uint64, err error)` and `freeSpaceCheckInterval time.Duration`; `func (s *Syncer) checkFreeSpace() error`; `func (s *Syncer) watchFreeSpace(ctx context.Context, cancel context.CancelCauseFunc)`.

- [ ] **Step 1: Write the failing tests**

Create `pkg/syncer/diskspace_test.go`:

```go
package syncer

import (
	"context"
	"errors"
	"testing"
	"time"

	pagesv1 "github.com/kup6s/pages/pkg/apis/v1beta1"
)

// stubFreeSpace makes statFS report percent free of a 100 GiB volume.
func stubFreeSpace(t *testing.T, percent uint64) {
	t.Helper()
	saved := statFS
	statFS = func(string) (uint64, uint64, error) {
		const total = 100 << 30
		return total / 100 * percent, total, nil
	}
	t.Cleanup(func() { statFS = saved })
}

func TestCheckFreeSpace(t *testing.T) {
	for _, tt := range []struct {
		min     float64
		free    uint64
		wantErr bool
	}{
		{min: 10, free: 50, wantErr: false},
		{min: 10, free: 9, wantErr: true},
		{min: 0, free: 1, wantErr: false},
	} {
		stubFreeSpace(t, tt.free)
		err := (&Syncer{SitesRoot: "/sites", MinFreePercent: tt.min}).checkFreeSpace()
		if (err != nil) != tt.wantErr {
			t.Errorf("min=%v free=%v%%: err = %v, wantErr %v", tt.min, tt.free, err, tt.wantErr)
		}
		if err != nil && syncReason(err) != pagesv1.SyncReasonDiskFull {
			t.Errorf("reason = %q, want DiskFull", syncReason(err))
		}
	}

	saved := statFS
	statFS = func(string) (uint64, uint64, error) { return 0, 0, errors.New("statfs failed") }
	t.Cleanup(func() { statFS = saved })
	if err := (&Syncer{MinFreePercent: 0}).checkFreeSpace(); err != nil {
		t.Errorf("disabled guard returned %v on statfs error", err)
	}
}

func TestSyncSite_DiskFullSkipsClone(t *testing.T) {
	r := newTestRemote(t, archiveFiles, remoteOptions{})
	s := newTestSyncer(t, r)
	s.MinFreePercent = 10
	stubFreeSpace(t, 5)

	err := s.syncSite(context.Background(), testSite(r, "/2018"))
	if syncReason(err) != pagesv1.SyncReasonDiskFull {
		t.Fatalf("err = %v (reason %q), want DiskFull", err, syncReason(err))
	}
	if names := listDir(t, s.SitesRoot); len(names) != 0 {
		t.Errorf("sites root = %v, want nothing cloned", names)
	}
}

func TestCloneSite_WatchdogCancelsClone(t *testing.T) {
	r := newTestRemote(t, archiveFiles, remoteOptions{})
	s := newTestSyncer(t, r)
	s.Git = &gitCLI{Binary: sleepingGit(t)}
	s.MinFreePercent = 10
	stubFreeSpace(t, 1)
	saved := freeSpaceCheckInterval
	freeSpaceCheckInterval = 10 * time.Millisecond
	t.Cleanup(func() { freeSpaceCheckInterval = saved })

	start := time.Now()
	err := s.cloneSite(context.Background(), testSite(r, "/2018"), "2018", nil)
	if syncReason(err) != pagesv1.SyncReasonDiskFull {
		t.Fatalf("err = %v (reason %q), want DiskFull", err, syncReason(err))
	}
	if time.Since(start) > 5*time.Second {
		t.Error("watchdog did not stop the clone")
	}
	assertNoTempEntries(t, s)
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `GOTOOLCHAIN=go1.26.0 go test ./pkg/syncer/ -run 'TestCheckFreeSpace|TestSyncSite_DiskFullSkipsClone|TestCloneSite_WatchdogCancelsClone'`
Expected: FAIL to compile, `undefined: statFS`.

- [ ] **Step 3: Implement**

Create `pkg/syncer/diskspace.go`:

```go
// Package syncer - free-space guard for the shared sites volume
package syncer

import (
	"context"
	"fmt"
	"syscall"
	"time"

	pagesv1 "github.com/kup6s/pages/pkg/apis/v1beta1"
)

// statFS reports free and total bytes of the filesystem holding path.
var statFS = func(path string) (free, total uint64, err error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, 0, err
	}
	return st.Bavail * uint64(st.Bsize), st.Blocks * uint64(st.Bsize), nil
}

// freeSpaceCheckInterval is how often a running clone checks free space.
var freeSpaceCheckInterval = 5 * time.Second

// checkFreeSpace fails with DiskFull below MinFreePercent.
func (s *Syncer) checkFreeSpace() error {
	free, total, err := statFS(s.SitesRoot)
	if s.MinFreePercent <= 0 {
		return nil
	}
	if err != nil {
		return newSyncError(pagesv1.SyncReasonDiskFull, fmt.Errorf("cannot read free space of %s: %w", s.SitesRoot, err))
	}
	if total == 0 {
		return nil
	}
	percent := float64(free) * 100 / float64(total)
	if percent < s.MinFreePercent {
		return newSyncError(pagesv1.SyncReasonDiskFull,
			fmt.Errorf("only %.1f%% free on %s, minimum is %.1f%%", percent, s.SitesRoot, s.MinFreePercent))
	}
	return nil
}

// watchFreeSpace cancels ctx with the DiskFull error once free space drops
// below the threshold.
func (s *Syncer) watchFreeSpace(ctx context.Context, cancel context.CancelCauseFunc) {
	if s.MinFreePercent <= 0 {
		return
	}
	ticker := time.NewTicker(freeSpaceCheckInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.checkFreeSpace(); err != nil {
				cancel(err)
				return
			}
		}
	}
}
```

In `pkg/syncer/git.go` add to `Syncer`:

```go
	// MinFreePercent is the free space on the sites volume below which clones
	// and updates are skipped; zero disables the check.
	MinFreePercent float64
```

In `pkg/syncer/sync.go`:
- in `syncRepo`, as the first statement: `if err := s.checkFreeSpace(); err != nil { return "", err }`.
- in `cloneSite`, replace the `s.git().clone(...)` block with:

```go
	cloneCtx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	go s.watchFreeSpace(cloneCtx, cancel)
	if err := s.git().clone(cloneCtx, site.Repo, site.Branch, subpath, tmp, auth); err != nil {
		_ = os.RemoveAll(tmp)
		var diskFull *syncError
		if errors.As(context.Cause(cloneCtx), &diskFull) {
			return diskFull
		}
		return classifyGitError(pagesv1.SyncReasonCloneFailed, err)
	}
```

(import `errors`).

- [ ] **Step 4: Run all syncer tests**

Run: `GOTOOLCHAIN=go1.26.0 go test -race ./pkg/syncer/`
Expected: PASS.

- [ ] **Step 5: Lint and commit**

```bash
make lint
git add pkg/syncer/
git commit -m "feat: skip and cancel clones when the sites volume runs full"
```

---

### Task 10: Syncer metrics and flags

**Files:**
- Create: `pkg/syncer/metrics.go`, `pkg/syncer/metrics_test.go`
- Modify: `pkg/syncer/git.go` (`Syncer.Metrics *Metrics`)
- Modify: `pkg/syncer/sync.go`, `pkg/syncer/diskspace.go` (observe)
- Modify: `cmd/syncer/main.go` (flags, registry, metrics server)
- Modify: `go.mod` (`github.com/prometheus/client_golang` becomes a direct dependency)

**Interfaces:**
- Produces: `type Metrics struct`, `func NewMetrics(reg prometheus.Registerer) *Metrics`, nil-safe methods `observeSync(err error)`, `observeGit(op string, start time.Time)`, `setVolume(free, total uint64)`; metric names `pages_syncer_sync_total{result,reason}`, `pages_syncer_git_duration_seconds{op}`, `pages_syncer_volume_free_bytes`, `pages_syncer_volume_size_bytes`; flags `--git-timeout`, `--min-free-percent`, `--metrics-addr`.

- [ ] **Step 1: Write the failing tests**

Create `pkg/syncer/metrics_test.go`:

```go
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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `GOTOOLCHAIN=go1.26.0 go test ./pkg/syncer/ -run TestMetrics`
Expected: FAIL to compile, `undefined: NewMetrics`.

- [ ] **Step 3: Implement**

Create `pkg/syncer/metrics.go`:

```go
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
```

Hook it in:
- `pkg/syncer/git.go`, `Syncer`: `// Metrics records sync results; nil disables metrics.` `Metrics *Metrics`.
- `pkg/syncer/sync.go`, `syncSite`: right after `commit, err := s.syncSiteContent(ctx, site)` add `s.Metrics.observeSync(err)`.
- `pkg/syncer/sync.go`, `cloneSite`: first statement `defer s.Metrics.observeGit("clone", time.Now())` (import `time`).
- `pkg/syncer/sync.go`, `syncRepo`: before the fetch, `start := time.Now()`; after a successful `resetHard`, `s.Metrics.observeGit("update", start)`.
- `pkg/syncer/diskspace.go`, `checkFreeSpace`: after the `statFS` call add `if err == nil { s.Metrics.setVolume(free, total) }`.

In `cmd/syncer/main.go`:

```go
	var gitTimeout time.Duration
	var minFreePercent float64
	var metricsAddr string

	flag.DurationVar(&gitTimeout, "git-timeout", 10*time.Minute, "Timeout for a single git operation")
	flag.Float64Var(&minFreePercent, "min-free-percent", 10, "Skip clones and updates below this free space on the sites volume (0 disables)")
	flag.StringVar(&metricsAddr, "metrics-addr", ":9090", "Address for the Prometheus metrics endpoint")
```

Set `GitTimeout: gitTimeout` and `MinFreePercent: minFreePercent` in the `syncer.Syncer` literal. After the recorder setup (Task 8), add:

```go
	registry := prometheus.NewRegistry()
	registry.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	s.Metrics = syncer.NewMetrics(registry)

	go func() {
		mux := http.NewServeMux()
		mux.Handle("/metrics", promhttp.HandlerFor(registry, promhttp.HandlerOpts{}))
		server := &http.Server{Addr: metricsAddr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
		go func() {
			<-ctx.Done()
			_ = server.Shutdown(context.Background())
		}()
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error(err, "metrics server failed")
		}
	}()
```

with imports `errors`, `net/http`, `github.com/prometheus/client_golang/prometheus`, `github.com/prometheus/client_golang/prometheus/collectors`, `github.com/prometheus/client_golang/prometheus/promhttp`. Add `"metricsAddr", metricsAddr, "minFreePercent", minFreePercent, "gitTimeout", gitTimeout` to the `Syncer started` log line. Then `GOTOOLCHAIN=go1.26.0 go mod tidy`.

- [ ] **Step 4: Run tests and build**

Run: `GOTOOLCHAIN=go1.26.0 go test -race ./... && go build ./... && go run ./cmd/syncer --help 2>&1 | grep -E 'git-timeout|min-free-percent|metrics-addr'`
Expected: tests PASS; the three flags are listed.

- [ ] **Step 5: Lint and commit**

```bash
make lint
git add pkg/syncer/ cmd/syncer/main.go go.mod go.sum
git commit -m "feat: expose syncer metrics and add git timeout and free-space flags"
```

---

### Task 11: Per-site metrics from the operator

**Files:**
- Create: `pkg/controller/metrics.go`, `pkg/controller/metrics_test.go`
- Modify: `cmd/operator/main.go` (register collector)

**Interfaces:**
- Consumes: `pagesv1.ConditionSynced`.
- Produces: `type SiteMetricsCollector struct{ Reader client.Reader }` implementing `prometheus.Collector`; metrics `pages_site_synced{namespace,name}`, `pages_site_last_sync_timestamp_seconds{namespace,name}`.

- [ ] **Step 1: Write the failing test**

Create `pkg/controller/metrics_test.go`:

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `GOTOOLCHAIN=go1.26.0 go test ./pkg/controller/ -run TestSiteMetricsCollector`
Expected: FAIL to compile, `undefined: SiteMetricsCollector`.

- [ ] **Step 3: Implement**

Create `pkg/controller/metrics.go`:

```go
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
```

In `cmd/operator/main.go`, after the controller registration block, add:

```go
	ctrlmetrics.Registry.MustRegister(&controller.SiteMetricsCollector{Reader: mgr.GetCache()})
```

with import `ctrlmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"`.

- [ ] **Step 4: Run tests and build**

Run: `GOTOOLCHAIN=go1.26.0 go test -race ./pkg/controller/ && go build ./...`
Expected: PASS.

- [ ] **Step 5: Lint and commit**

```bash
make lint
git add pkg/controller/metrics.go pkg/controller/metrics_test.go cmd/operator/main.go go.mod go.sum
git commit -m "feat: export per-site sync state from the operator"
```

---

### Task 12: Helm chart

**Files:**
- Modify: `charts/kup6s-pages/values.yaml` (syncer values, `metrics` block)
- Modify: `charts/kup6s-pages/values.schema.json`
- Modify: `charts/kup6s-pages/templates/deployment-syncer.yaml` (args, port)
- Modify: `charts/kup6s-pages/templates/clusterrole-syncer.yaml` (events)
- Modify: `charts/kup6s-pages/templates/configmap-nginx.yaml` (`.git` deny)
- Create: `charts/kup6s-pages/templates/podmonitor.yaml`, `charts/kup6s-pages/templates/prometheusrule.yaml`
- Modify: `charts/kup6s-pages/tests/deployment-syncer_test.yaml`, `rbac_test.yaml`, `configmap_test.yaml`
- Create: `charts/kup6s-pages/tests/monitoring_test.yaml`

**Interfaces:**
- Consumes: flags from Task 10, metric names from Tasks 10–11, port name `metrics` on both operator and syncer pods.

- [ ] **Step 1: Write the failing chart tests**

Append to `charts/kup6s-pages/tests/deployment-syncer_test.yaml` under `tests:`:

```yaml
  - it: should pass git timeout, free-space and metrics flags
    asserts:
      - contains:
          path: spec.template.spec.containers[0].args
          content: --git-timeout=10m
      - contains:
          path: spec.template.spec.containers[0].args
          content: --min-free-percent=10
      - contains:
          path: spec.template.spec.containers[0].args
          content: --metrics-addr=:9090
      - contains:
          path: spec.template.spec.containers[0].ports
          content:
            name: metrics
            containerPort: 9090
            protocol: TCP
```

Append to `charts/kup6s-pages/tests/rbac_test.yaml`:

```yaml
  - it: should let the syncer emit events
    template: templates/clusterrole-syncer.yaml
    asserts:
      - contains:
          path: rules
          content:
            apiGroups: ["events.k8s.io"]
            resources: ["events"]
            verbs: ["create", "patch"]
```

Append to `charts/kup6s-pages/tests/configmap_test.yaml`:

```yaml
  - it: should never serve git metadata
    asserts:
      - matchRegex:
          path: data["default.conf"]
          pattern: 'location ~ \(\^\|/\)\\\.git\(/\|\$\)'
```

Create `charts/kup6s-pages/tests/monitoring_test.yaml`:

```yaml
suite: monitoring tests
templates:
  - templates/podmonitor.yaml
  - templates/prometheusrule.yaml
tests:
  - it: should render nothing by default
    asserts:
      - hasDocuments:
          count: 0

  - it: should create PodMonitors for operator and syncer
    template: templates/podmonitor.yaml
    set:
      metrics.podMonitor.enabled: true
      metrics.podMonitor.labels:
        release: prometheus
    asserts:
      - hasDocuments:
          count: 2
      - isKind:
          of: PodMonitor
      - equal:
          path: spec.podMetricsEndpoints[0].port
          value: metrics
      - equal:
          path: metadata.labels.release
          value: prometheus
      - equal:
          path: spec.selector.matchLabels["app.kubernetes.io/component"]
          value: syncer
        documentIndex: 1

  - it: should create the alert rules
    template: templates/prometheusrule.yaml
    set:
      metrics.prometheusRule.enabled: true
      metrics.prometheusRule.staleAfterSeconds: 1800
    asserts:
      - isKind:
          of: PrometheusRule
      - equal:
          path: spec.groups[0].rules[0].alert
          value: PagesSiteSyncFailing
      - equal:
          path: spec.groups[0].rules[0].expr
          value: pages_site_synced == 0
      - equal:
          path: spec.groups[0].rules[1].expr
          value: time() - pages_site_last_sync_timestamp_seconds > 1800
      - equal:
          path: spec.groups[0].rules[2].alert
          value: PagesVolumeLow
```

- [ ] **Step 2: Run chart tests to verify they fail**

Run: `helm unittest charts/kup6s-pages`
Expected: FAIL on the new tests (missing args, rule, templates).

- [ ] **Step 3: Implement the chart changes**

`values.yaml`, in the `syncer:` block after `allowedHosts`:

```yaml
  # -- Timeout for a single git operation (clone, fetch, reset)
  gitTimeout: "10m"

  # -- Minimum free space on the sites volume in percent. Clones and updates
  # are skipped below it and the site reports Synced=False/DiskFull. 0 disables the check.
  minFreePercent: 10

  # -- Port of the syncer's Prometheus metrics endpoint (not routed through the webhook IngressRoute)
  metricsPort: 9090
```

Above `syncer.resources`, add the comment line:

```yaml
  # 256Mi suffices for sites with a `path`. Sites with `path: /` and repositories
  # above about 1 GB need a higher memory limit.
```

At the end of `values.yaml`:

```yaml
# =============================================================================
# Monitoring
# =============================================================================
metrics:
  podMonitor:
    # -- Create PodMonitors for operator and syncer (requires the Prometheus Operator CRDs)
    enabled: false
    # -- Scrape interval
    interval: 30s
    # -- Extra labels, e.g. to match the Prometheus podMonitorSelector
    labels: {}

  prometheusRule:
    # -- Create a PrometheusRule with sync and volume alerts
    enabled: false
    # -- Extra labels, e.g. to match the Prometheus ruleSelector
    labels: {}
    # -- How long a site may report Synced other than True before PagesSiteSyncFailing fires
    failingFor: 15m
    # -- Seconds since the last successful sync before PagesSiteSyncStale fires (about 3x syncer.syncInterval)
    staleAfterSeconds: 900
    # -- Free space in percent below which PagesVolumeLow fires
    volumeLowPercent: 15
```

`values.schema.json`: add to `properties.syncer.properties`:

```json
"gitTimeout": { "type": "string", "description": "Timeout for a single git operation", "default": "10m" },
"minFreePercent": { "type": "number", "minimum": 0, "maximum": 100, "description": "Minimum free space on the sites volume in percent", "default": 10 },
"metricsPort": { "type": "integer", "description": "Syncer metrics port", "default": 9090 }
```

and to top-level `properties`:

```json
"metrics": {
  "type": "object",
  "properties": {
    "podMonitor": {
      "type": "object",
      "properties": {
        "enabled": { "type": "boolean", "default": false },
        "interval": { "type": "string", "default": "30s" },
        "labels": { "type": "object" }
      }
    },
    "prometheusRule": {
      "type": "object",
      "properties": {
        "enabled": { "type": "boolean", "default": false },
        "labels": { "type": "object" },
        "failingFor": { "type": "string", "default": "15m" },
        "staleAfterSeconds": { "type": "integer", "minimum": 1, "default": 900 },
        "volumeLowPercent": { "type": "number", "minimum": 0, "maximum": 100, "default": 15 }
      }
    }
  }
}
```

`templates/deployment-syncer.yaml`, after `- --allowed-hosts=...`:

```yaml
            - --git-timeout={{ .Values.syncer.gitTimeout }}
            - --min-free-percent={{ .Values.syncer.minFreePercent }}
            - --metrics-addr=:{{ .Values.syncer.metricsPort }}
```

and in `ports:` after the `webhook` port:

```yaml
            - name: metrics
              containerPort: {{ .Values.syncer.metricsPort }}
              protocol: TCP
```

`templates/clusterrole-syncer.yaml`, after the status rule:

```yaml
  # Events for sync failures and recoveries
  - apiGroups: ["events.k8s.io"]
    resources: ["events"]
    verbs: ["create", "patch"]
```

`templates/configmap-nginx.yaml`, directly before `location / {`:

```
        # Never serve git metadata (.git directory or .git file)
        location ~ (^|/)\.git(/|$) {
            return 404;
        }

```

Create `templates/podmonitor.yaml`:

```yaml
{{- if .Values.metrics.podMonitor.enabled }}
apiVersion: monitoring.coreos.com/v1
kind: PodMonitor
metadata:
  name: {{ include "kup6s-pages.fullname" . }}-operator
  namespace: {{ include "kup6s-pages.namespace" . }}
  labels:
    {{- include "kup6s-pages.operator.labels" . | nindent 4 }}
    {{- with .Values.metrics.podMonitor.labels }}
    {{- toYaml . | nindent 4 }}
    {{- end }}
spec:
  selector:
    matchLabels:
      {{- include "kup6s-pages.operator.selectorLabels" . | nindent 6 }}
  podMetricsEndpoints:
    - port: metrics
      interval: {{ .Values.metrics.podMonitor.interval }}
---
apiVersion: monitoring.coreos.com/v1
kind: PodMonitor
metadata:
  name: {{ include "kup6s-pages.fullname" . }}-syncer
  namespace: {{ include "kup6s-pages.namespace" . }}
  labels:
    {{- include "kup6s-pages.syncer.labels" . | nindent 4 }}
    {{- with .Values.metrics.podMonitor.labels }}
    {{- toYaml . | nindent 4 }}
    {{- end }}
spec:
  selector:
    matchLabels:
      {{- include "kup6s-pages.syncer.selectorLabels" . | nindent 6 }}
  podMetricsEndpoints:
    - port: metrics
      interval: {{ .Values.metrics.podMonitor.interval }}
{{- end }}
```

Create `templates/prometheusrule.yaml`:

```yaml
{{- if .Values.metrics.prometheusRule.enabled }}
apiVersion: monitoring.coreos.com/v1
kind: PrometheusRule
metadata:
  name: {{ include "kup6s-pages.fullname" . }}
  namespace: {{ include "kup6s-pages.namespace" . }}
  labels:
    {{- include "kup6s-pages.labels" . | nindent 4 }}
    {{- with .Values.metrics.prometheusRule.labels }}
    {{- toYaml . | nindent 4 }}
    {{- end }}
spec:
  groups:
    - name: kup6s-pages
      rules:
        - alert: PagesSiteSyncFailing
          expr: pages_site_synced == 0
          for: {{ .Values.metrics.prometheusRule.failingFor }}
          labels:
            severity: warning
          annotations:
            summary: StaticSite {{`{{ $labels.namespace }}/{{ $labels.name }}`}} is not synced
            description: The Synced condition is not True. Check it with `kubectl describe staticsite -n {{`{{ $labels.namespace }} {{ $labels.name }}`}}`.
        - alert: PagesSiteSyncStale
          expr: time() - pages_site_last_sync_timestamp_seconds > {{ .Values.metrics.prometheusRule.staleAfterSeconds }}
          for: 15m
          labels:
            severity: warning
          annotations:
            summary: StaticSite {{`{{ $labels.namespace }}/{{ $labels.name }}`}} has not synced recently
            description: The last successful sync is older than {{ .Values.metrics.prometheusRule.staleAfterSeconds }} seconds. The syncer may not be running.
        - alert: PagesVolumeLow
          expr: pages_syncer_volume_free_bytes / pages_syncer_volume_size_bytes * 100 < {{ .Values.metrics.prometheusRule.volumeLowPercent }}
          for: 10m
          labels:
            severity: warning
          annotations:
            summary: The kup6s-pages sites volume is running full
            description: Less than {{ .Values.metrics.prometheusRule.volumeLowPercent }}% of the sites volume is free. Clones stop below syncer.minFreePercent.
{{- end }}
```

- [ ] **Step 4: Run chart checks**

Run:

```bash
helm lint charts/kup6s-pages --set 'syncer.allowedHosts={github.com}'
helm unittest charts/kup6s-pages
for v in charts/kup6s-pages/ci/*.yaml; do helm template test charts/kup6s-pages -f "$v" | kubeconform -strict -summary -skip CustomResourceDefinition,IngressRoute,Middleware,Certificate; done
helm template test charts/kup6s-pages --set 'syncer.allowedHosts={github.com}' --set metrics.podMonitor.enabled=true --set metrics.prometheusRule.enabled=true | grep -E '^kind: (PodMonitor|PrometheusRule)'
```

Expected: lint OK, all unit tests PASS, kubeconform reports no invalid resources, the last command prints two `PodMonitor` lines and one `PrometheusRule` line. If `kubeconform` is not installed: `go run github.com/yannh/kubeconform/cmd/kubeconform@v0.6.7` in its place.

- [ ] **Step 5: Commit**

```bash
git add charts/kup6s-pages/
git commit -m "feat(chart): syncer flags, events RBAC, .git denial, PodMonitors and alerts"
```

---

### Task 13: Documentation

**Files:**
- Modify: `docs/content/architecture.md` (Syncer section line ~100, Sync Flow line ~162, Limitations line ~271)
- Modify: `docs/content/troubleshooting.md` (replace "Site Stuck in "Pending"", add "Sync Failures")
- Modify: `docs/content/reference/crd.md` (Status Fields, Conditions)
- Modify: `docs/content/reference/cli-flags.md` (Syncer Flags table)
- Modify: `docs/content/reference/helm-values.md` (Syncer table, new Monitoring section)
- Modify: `docs/content/usage/path-prefix.md` or `docs/content/usage/_index.md:77` (layout sentence)
- Modify: `docs/content/security.md` (Syncer Permissions, Additional Hardening)
- Modify: `CLAUDE.md` (Dependencies line)

Read each file section first and keep its tone. Insert the content below; adjust heading levels to the surrounding file.

- [ ] **Step 1: architecture.md**

Replace the Syncer bullet list under `### Syncer` with:

```markdown
- Keeps one clone per StaticSite under `/sites/.repos/<name>`, using the git CLI
- Clones are shallow partial clones (`--depth 1 --filter=blob:none`). When `spec.path` is set, only that directory is checked out (sparse checkout), so ten sites that each serve one directory of a large repository download about the repository's size once, not ten times
- `/sites/<name>` is always a symlink to the served directory of the clone
- Serves webhooks for instant syncs
- Reports results in the `Synced` condition and as events, and exposes Prometheus metrics on a separate port
```

Replace the diagram text block under `### Periodic Sync` (the `git clone --depth=1 <repo> /sites/<name>` box) with this list:

```markdown
For every StaticSite, at each interval:

1. Validate `repo` and `path`, read credentials from `secretRef`.
2. Skip the site with `Synced=False/DiskFull` if the sites volume has less than `--min-free-percent` free.
3. If `/sites/.repos/<name>` is a complete clone of the configured repo, branch and path: `git fetch --depth 1` and `git reset --hard`. A failed fetch keeps the current content online; a failed reset triggers a fresh clone.
4. Otherwise clone into `/sites/.repos/.tmp-<name>-<random>`, then swap it into place and point `/sites/<name>` at it with an atomic rename. An interrupted clone never replaces a working site.
5. Replace the clone with a fresh one when `.git` grows beyond twice the checkout plus 50 MiB; git does not prune old objects of partial clones.
6. Write `Synced`, `lastSync` and `lastCommit`.
```

Add to `## Limitations`:

```markdown
- Updates reset the served checkout in place; during the reset, nginx can serve a mix of old and new files (#157).
- Site directories are keyed by name only; two StaticSites with the same name in different namespaces collide (#158).
- Git servers without partial-clone support receive `--filter` and ignore it; the syncer then downloads the full repository.
```

- [ ] **Step 2: troubleshooting.md**

Replace the `## Site Stuck in "Pending"` section with:

````markdown
## Site Stuck in "Pending" or "Syncing"

`Pending` means the syncer has not reported a result yet; `Syncing` means a clone is running. Check the `Synced` condition:

```bash
kubectl get staticsite <name> -o jsonpath='{.status.conditions[?(@.type=="Synced")]}'
kubectl -n kup6s-pages logs deploy/kup6s-pages-syncer --tail=100
```

A site that stays `Syncing` for long while the syncer pod restarts was most likely killed during the clone (`kubectl -n kup6s-pages describe pod -l app.kubernetes.io/component=syncer`, look for `OOMKilled`). See "Sync Failures" below.

## Sync Failures

The syncer sets `Synced=False` with one of these reasons, and the site keeps serving its last good content:

| Reason | Meaning | What to do |
|---|---|---|
| `CloneFailed` | `git clone` failed, for example unknown branch or unreachable host | Check `spec.branch` and `spec.repo`; the message contains git's error |
| `FetchFailed` | Updating an existing clone failed | Usually network or server trouble; the syncer retries every interval |
| `AuthFailed` | The secret is missing or the server rejected the credentials | See "Private Repo Authentication Fails" |
| `SubpathMissing` | `spec.path` does not exist on the branch | Fix `spec.path` |
| `DiskFull` | The sites volume has less free space than `--min-free-percent` | Grow the PVC or remove unused sites |
| `Timeout` | A git operation took longer than `--git-timeout` | Raise `syncer.gitTimeout` for very large repositories |
| `InvalidSpec` | `repo` is not allowed by `--allowed-hosts`, or `path` is invalid | Fix the StaticSite |

Broken or interrupted clones need no manual cleanup: the syncer detects them, deletes leftovers, and clones again on the next sync.

The syncer emits a `SyncFailed` event when a site starts failing and `SyncRecovered` when it recovers:

```bash
kubectl get events --field-selector involvedObject.name=<name>
```

With `metrics.prometheusRule.enabled`, these alerts fire: `PagesSiteSyncFailing` (not synced for 15 minutes), `PagesSiteSyncStale` (no successful sync for `staleAfterSeconds`, for example because the syncer is down), `PagesVolumeLow`.

### Syncer runs out of memory

Sites with a `path` need little memory, because only that directory is downloaded. Sites with `path: /` and repositories above about 1 GB can exceed the default limit of 256Mi; raise `syncer.resources.limits.memory`.
````

- [ ] **Step 3: reference/crd.md**

In the Status Fields table, change the `lastSync` row to `Timestamp of the last successful sync` (unchanged meaning, now enforced) and the `phase` row description to `Pending, Syncing, Ready or Error; derived from the Synced condition and reconcile errors`. Add to the Conditions section:

```markdown
### Synced

Written by the syncer only.

| Status | Reason | Meaning |
|---|---|---|
| `True` | `Synced` | The configured branch and path are checked out; `lastCommit` names the commit |
| `Unknown` | `Syncing` | A clone is running |
| `False` | `CloneFailed`, `FetchFailed`, `AuthFailed`, `SubpathMissing`, `DiskFull`, `Timeout`, `InvalidSpec`, `SyncFailed` | The last sync failed; the message contains the error. The site keeps serving its last good content |

`kubectl get staticsites` shows the condition in the `Synced` column.
```

- [ ] **Step 4: reference/cli-flags.md**

Add three rows to the Syncer Flags table:

```markdown
| `--git-timeout` | `10m` | Timeout for a single git operation |
| `--min-free-percent` | `10` | Skip clones and updates when the sites volume has less free space (0 disables) |
| `--metrics-addr` | `:9090` | Address of the Prometheus metrics endpoint |
```

- [ ] **Step 5: reference/helm-values.md**

Add to the Syncer table:

```markdown
| `syncer.gitTimeout` | `10m` | Timeout for a single git operation |
| `syncer.minFreePercent` | `10` | Minimum free space on the sites volume in percent (0 disables) |
| `syncer.metricsPort` | `9090` | Syncer metrics port |
```

Add a section:

```markdown
## Monitoring

| Value | Default | Description |
|-------|---------|-------------|
| `metrics.podMonitor.enabled` | `false` | Create PodMonitors for operator and syncer (requires Prometheus Operator CRDs) |
| `metrics.podMonitor.interval` | `30s` | Scrape interval |
| `metrics.podMonitor.labels` | `{}` | Extra labels, for the Prometheus podMonitorSelector |
| `metrics.prometheusRule.enabled` | `false` | Create a PrometheusRule with sync and volume alerts |
| `metrics.prometheusRule.labels` | `{}` | Extra labels, for the Prometheus ruleSelector |
| `metrics.prometheusRule.failingFor` | `15m` | Delay before `PagesSiteSyncFailing` fires |
| `metrics.prometheusRule.staleAfterSeconds` | `900` | Age of the last successful sync before `PagesSiteSyncStale` fires; about 3× `syncer.syncInterval` |
| `metrics.prometheusRule.volumeLowPercent` | `15` | Free space below which `PagesVolumeLow` fires |

Metrics: `pages_site_synced`, `pages_site_last_sync_timestamp_seconds` (operator, per site); `pages_syncer_sync_total`, `pages_syncer_git_duration_seconds`, `pages_syncer_volume_free_bytes`, `pages_syncer_volume_size_bytes` (syncer).
```

Next to the syncer resources row, add: `256Mi suffices for sites with a path; sites with path / and repositories above about 1 GB need more.`

- [ ] **Step 6: usage pages**

In `docs/content/usage/_index.md`, replace line 30 (`The Syncer clones the repo to /sites/my-website/`) with `The Syncer clones the repo to /sites/.repos/my-website/ and links /sites/my-website/ to it` and line 77 with `The Syncer checks out only dist/ into /sites/.repos/docs/ and links /sites/docs/ → /sites/.repos/docs/dist/.`

In `docs/content/usage/path-prefix.md` under `## How It Works`, add:

```markdown
Several StaticSites can serve different directories of one repository, each with its own `path` and `pathPrefix`. Each site keeps its own clone, but only checks out and downloads its own directory, so disk use stays close to the size of the repository. This needs a git server with partial-clone support (GitHub, GitLab, Gitea and Forgejo have it); other servers send the full repository to each site.
```

- [ ] **Step 7: security.md**

In the Syncer Permissions YAML block, add:

```yaml
# Events: SyncFailed / SyncRecovered
- apiGroups: ["events.k8s.io"]
  resources: ["events"]
  verbs: ["create", "patch"]
```

Under `## Additional Hardening`, add:

```markdown
- **Git credentials** never reach the command line or `.git/config`. The syncer passes them to git as an environment-only `http.<origin>.extraHeader` entry, scoped to the repository's scheme and host, so git does not send them to other hosts.
- **git runs isolated** from system and user configuration (`GIT_CONFIG_NOSYSTEM`, `GIT_CONFIG_GLOBAL=/dev/null`), never prompts, and may only use `http` and `https`.
- **nginx never serves `.git`**: requests for a `.git` file or directory return 404.
- **No shared clones**: every StaticSite has its own clone and credentials; content of one site's repository is never served to another.
```

- [ ] **Step 8: CLAUDE.md**

Replace `- Go, controller-runtime, go-git (see go.mod for versions)` with `- Go, controller-runtime, git CLI in the syncer image (see go.mod and Dockerfile.syncer)`.

- [ ] **Step 9: Build docs and commit**

Run: `hugo --source docs --minify` (if Hugo is not installed locally, the CI job `docs-build` checks it).
Expected: build succeeds without errors.

```bash
git add docs/content CLAUDE.md
git commit -m "docs: sparse clones, Synced condition, sync failures and monitoring"
```

---

### Task 14: Full verification and pull request

- [ ] **Step 1: Full test and lint run**

```bash
GOTOOLCHAIN=go1.26.0 go test -race -count=1 ./...
make lint
helm unittest charts/kup6s-pages
grep -c go-git go.mod   # expected: 0
```

Expected: all PASS, `0 issues`, `0`.

- [ ] **Step 2: Image smoke test**

```bash
docker build -f Dockerfile.syncer -t pages-syncer:153 .
docker run --rm --entrypoint git pages-syncer:153 --version
docker run --rm pages-syncer:153 --help 2>&1 | grep -E 'git-timeout|min-free-percent|metrics-addr'
docker rmi pages-syncer:153
```

Expected: `git version 2.54.0`; the three flags are listed.

- [ ] **Step 3: Push and open the PR**

```bash
git push -u origin fix/153-syncer-sparse-clones
gh pr create --base main --title "Syncer: sparse partial clones, recovery, Synced condition and monitoring" --body "Fixes #153.

- The syncer uses the git CLI instead of go-git. Each StaticSite keeps its own shallow partial clone; with \`path\` set, only that directory is downloaded and checked out. Ten sites serving one directory each of a 1.1 GB repository now use about the repository's size on disk.
- Clones go to a temporary directory and are swapped in. Interrupted, broken, outgrown or outdated clones are replaced automatically; leftovers are removed.
- The syncer owns a new \`Synced\` condition; the controller derives \`phase\` from it instead of overwriting errors with \`Ready\`. Events \`SyncFailed\`/\`SyncRecovered\` on transitions.
- Free-space guard (\`--min-free-percent\`) before and during clones.
- Metrics: per-site sync state from the operator, syncer metrics on a separate port. Optional PodMonitors and PrometheusRule in the chart.
- nginx returns 404 for \`.git\`.

Spike measurements, design and plan: \`docs/superpowers/specs/2026-10-13-syncer-sparse-clones-design.md\`, \`docs/superpowers/plans/2026-10-13-syncer-sparse-clones.md\`.

Follow-ups: #157 (atomic content swap), #158 (name collisions across namespaces)."
gh pr checks --watch --interval 20
```

Expected: all checks pass. Do not merge; the maintainer merges.
