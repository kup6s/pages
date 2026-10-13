package syncer

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

var archiveFiles = map[string]string{
	"index.html":             "root",
	"2018/index.html":        "2018 v1",
	"2005/index.html":        "2005 v1",
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

// sleepingGit returns a fake git binary that hangs in a child process which
// keeps the output pipes open, like git-remote-https does.
func sleepingGit(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "git")
	if err := os.WriteFile(p, []byte("#!/bin/sh\nsleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestGitCLI_CredentialsAreNotSentToRedirectTargets(t *testing.T) {
	r := newTestRemote(t, archiveFiles, remoteOptions{})
	backend, err := url.Parse(r.URL)
	if err != nil {
		t.Fatal(err)
	}
	proxy := httputil.NewSingleHostReverseProxy(backend)
	var leaked atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Header.Get("Authorization") != "" {
			leaked.Store(true)
		}
		proxy.ServeHTTP(w, req)
	}))
	defer target.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		// Same loopback address under another name, i.e. another host.
		to := strings.Replace(target.URL, "127.0.0.1", "localhost", 1) + req.URL.RequestURI()
		http.Redirect(w, req, to, http.StatusFound)
	}))
	defer origin.Close()

	err = (&gitCLI{}).clone(context.Background(), origin.URL+"/", "main", "", filepath.Join(t.TempDir(), "c"),
		&gitAuth{Username: "u", Password: "p"})
	if err == nil {
		t.Error("clone followed a redirect, want failure")
	}
	if leaked.Load() {
		t.Error("Authorization header reached the redirect target")
	}
}

func TestGitCLI_SubpathStartingWithDashIsNotAnOption(t *testing.T) {
	r := newTestRemote(t, map[string]string{"index.html": "root", "--stdin/index.html": "dash"}, remoteOptions{})
	dest := filepath.Join(t.TempDir(), "clone")
	if err := (&gitCLI{}).clone(context.Background(), r.URL, "main", "--stdin", dest, nil); err != nil {
		t.Fatalf("clone: %v", err)
	}
	if got := readFile(t, filepath.Join(dest, "--stdin", "index.html")); got != "dash" {
		t.Errorf("--stdin/index.html = %q", got)
	}
}

func TestGitCLI_InspectUnquotesSparsePath(t *testing.T) {
	for _, subpath := range []string{"übersicht", `quo"te`, "with space"} {
		t.Run(subpath, func(t *testing.T) {
			r := newTestRemote(t, map[string]string{subpath + "/index.html": "x"}, remoteOptions{})
			g := &gitCLI{}
			dest := filepath.Join(t.TempDir(), "clone")
			if err := g.clone(context.Background(), r.URL, "main", subpath, dest, nil); err != nil {
				t.Skipf("git does not accept %q in cone mode: %v", subpath, err)
			}
			info, err := g.inspect(context.Background(), dest)
			if err != nil {
				t.Fatalf("inspect: %v", err)
			}
			if info.SparsePath != subpath {
				t.Errorf("SparsePath = %q, want %q", info.SparsePath, subpath)
			}
		})
	}
}

func TestIsLockError(t *testing.T) {
	for stderr, want := range map[string]bool{
		"fatal: Unable to create '/x/.git/shallow.lock': File exists.":                  true,
		"error: cannot lock ref 'refs/remotes/origin/main': Unable to create '/x.lock'": true,
		"fatal: unable to access 'https://h/r.git/': Could not resolve host: h":         false,
		"fatal: couldn't find remote ref main":                                          false,
	} {
		if got := isLockError(&gitError{Stderr: stderr}); got != want {
			t.Errorf("isLockError(%q) = %v, want %v", stderr, got, want)
		}
	}
	if isLockError(errors.New("plain")) {
		t.Error("non-git error counted as lock error")
	}
}
