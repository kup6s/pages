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
	if err := os.WriteFile(filepath.Join(dirSite, "sub", "index.html"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := replaceSymlink(target1, dirSite); err != nil {
		t.Fatalf("replace directory: %v", err)
	}
	if got, _ := os.Readlink(dirSite); got != target1 {
		t.Errorf("legacy -> %q, want %q", got, target1)
	}
	if _, err := os.Stat(filepath.Join(dirSite, "sub")); err == nil {
		t.Error("legacy content still reachable through the link")
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
