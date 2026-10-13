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
	"strconv"
	"strings"
	"syscall"
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
	_, err := g.run(ctx, dest, repoURL, auth, "sparse-checkout", "set", "--", subpath)
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
		listed, err := g.run(ctx, dir, "", nil, "sparse-checkout", "list")
		if err != nil {
			return cloneInfo{}, err
		}
		// git C-quotes paths containing a double quote or control characters.
		if strings.HasPrefix(listed, `"`) {
			if listed, err = strconv.Unquote(listed); err != nil {
				return cloneInfo{}, fmt.Errorf("sparse path %q: %w", listed, err)
			}
		}
		info.SparsePath = listed
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
	// Redirects would carry the credentials, and the request, to another host.
	full := append([]string{"-c", "http.followRedirects=false", "-c", "core.quotePath=false"}, args...)
	cmd := exec.CommandContext(ctx, bin, full...)
	// git spawns helpers (git-remote-https, index-pack) that hold the output
	// pipes open; kill the whole process group so the timeout really bounds git.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 5 * time.Second
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

// isLockError reports whether git failed on a stale lock file that an
// interrupted earlier run left behind.
func isLockError(err error) bool {
	var gerr *gitError
	if !errors.As(err, &gerr) {
		return false
	}
	return strings.Contains(gerr.Stderr, ".lock': File exists") ||
		strings.Contains(gerr.Stderr, "cannot lock ref") ||
		(strings.Contains(gerr.Stderr, "Unable to create") && strings.Contains(gerr.Stderr, ".lock"))
}
