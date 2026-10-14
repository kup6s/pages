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

func TestTestRemote_ExercisesAmendPushBranchClose(t *testing.T) {
	r := newTestRemote(t, map[string]string{"index.html": "hi"}, remoteOptions{})

	// Exercise amend method
	amended := r.amend(map[string]string{"index.html": "amended"}, "amended commit")
	out := runGit(t, t.TempDir(), "ls-remote", r.URL, "refs/heads/main")
	if !strings.HasPrefix(out, amended) {
		t.Errorf("ls-remote after amend = %q, want prefix %q", out, amended)
	}

	// Exercise pushBranch method
	newBranch := r.pushBranch("test-branch", map[string]string{"new.txt": "content"})
	out = runGit(t, t.TempDir(), "ls-remote", r.URL, "refs/heads/test-branch")
	if !strings.HasPrefix(out, newBranch) {
		t.Errorf("ls-remote for test-branch = %q, want prefix %q", out, newBranch)
	}

	// Exercise close method
	r.close()
	if _, err := tryGit(t.TempDir(), "ls-remote", r.URL); err == nil {
		t.Error("ls-remote after close succeeded, want failure")
	}
}
