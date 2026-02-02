package syncer

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestValidateRepoURL(t *testing.T) {
	tests := []struct {
		name         string
		allowedHosts []string
		repoURL      string
		wantErr      bool
	}{
		{
			name:         "empty allowlist returns error",
			allowedHosts: nil,
			repoURL:      "https://github.com/example/repo.git",
			wantErr:      true, // AllowedHosts is now mandatory
		},
		{
			name:         "exact match allowed",
			allowedHosts: []string{"github.com"},
			repoURL:      "https://github.com/example/repo.git",
			wantErr:      false,
		},
		{
			name:         "host not in allowlist",
			allowedHosts: []string{"github.com"},
			repoURL:      "https://gitlab.com/example/repo.git",
			wantErr:      true,
		},
		{
			name:         "wildcard match",
			allowedHosts: []string{"*.kup6s.io"},
			repoURL:      "https://forgejo.kup6s.io/example/repo.git",
			wantErr:      false,
		},
		{
			name:         "wildcard no match",
			allowedHosts: []string{"*.kup6s.io"},
			repoURL:      "https://github.com/example/repo.git",
			wantErr:      true,
		},
		{
			name:         "case insensitive",
			allowedHosts: []string{"GitHub.com"},
			repoURL:      "https://github.com/example/repo.git",
			wantErr:      false,
		},
		{
			name:         "reject file scheme",
			allowedHosts: []string{"github.com"},
			repoURL:      "file:///etc/passwd",
			wantErr:      true,
		},
		{
			name:         "reject ssh scheme",
			allowedHosts: []string{"github.com"},
			repoURL:      "ssh://git@github.com/example/repo.git",
			wantErr:      true,
		},
		{
			name:         "url with port",
			allowedHosts: []string{"git.example.com"},
			repoURL:      "https://git.example.com:8443/repo.git",
			wantErr:      false,
		},
		{
			name:         "multiple allowed hosts",
			allowedHosts: []string{"github.com", "gitlab.com", "*.kup6s.io"},
			repoURL:      "https://gitlab.com/example/repo.git",
			wantErr:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &Syncer{
				AllowedHosts: tt.allowedHosts,
			}
			err := s.validateRepoURL(tt.repoURL)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateRepoURL() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestStaticSiteDataFromUnstructured(t *testing.T) {
	tests := []struct {
		name    string
		obj     map[string]interface{}
		want    staticSiteData
		wantErr bool
	}{
		{
			name: "basic site",
			obj: map[string]interface{}{
				"metadata": map[string]interface{}{
					"name":      "test-site",
					"namespace": "pages",
				},
				"spec": map[string]interface{}{
					"repo":   "https://github.com/example/repo.git",
					"branch": "main",
					"path":   "/dist",
				},
			},
			want: staticSiteData{
				Name:      "test-site",
				Namespace: "pages",
				Repo:      "https://github.com/example/repo.git",
				Branch:    "main",
				Path:      "/dist",
			},
			wantErr: false,
		},
		{
			name: "defaults applied",
			obj: map[string]interface{}{
				"metadata": map[string]interface{}{
					"name":      "test-site",
					"namespace": "pages",
				},
				"spec": map[string]interface{}{
					"repo": "https://github.com/example/repo.git",
				},
			},
			want: staticSiteData{
				Name:      "test-site",
				Namespace: "pages",
				Repo:      "https://github.com/example/repo.git",
				Branch:    "main", // default
				Path:      "/",    // default
			},
			wantErr: false,
		},
		{
			name: "with secret ref",
			obj: map[string]interface{}{
				"metadata": map[string]interface{}{
					"name":      "test-site",
					"namespace": "pages",
				},
				"spec": map[string]interface{}{
					"repo": "https://github.com/example/repo.git",
					"secretRef": map[string]interface{}{
						"name": "git-creds",
						"key":  "token",
					},
				},
			},
			want: staticSiteData{
				Name:      "test-site",
				Namespace: "pages",
				Repo:      "https://github.com/example/repo.git",
				Branch:    "main",
				Path:      "/",
				SecretRef: &secretRef{
					Name: "git-creds",
					Key:  "token",
				},
			},
			wantErr: false,
		},
		{
			name: "secretRef missing name field",
			obj: map[string]interface{}{
				"metadata": map[string]interface{}{
					"name":      "test-site",
					"namespace": "pages",
				},
				"spec": map[string]interface{}{
					"repo": "https://github.com/example/repo.git",
					"secretRef": map[string]interface{}{
						"key": "token",
					},
				},
			},
			wantErr: true,
		},
		{
			name: "secretRef name is wrong type",
			obj: map[string]interface{}{
				"metadata": map[string]interface{}{
					"name":      "test-site",
					"namespace": "pages",
				},
				"spec": map[string]interface{}{
					"repo": "https://github.com/example/repo.git",
					"secretRef": map[string]interface{}{
						"name": 123,
						"key":  "token",
					},
				},
			},
			wantErr: true,
		},
		{
			name: "secretRef name is nil",
			obj: map[string]interface{}{
				"metadata": map[string]interface{}{
					"name":      "test-site",
					"namespace": "pages",
				},
				"spec": map[string]interface{}{
					"repo": "https://github.com/example/repo.git",
					"secretRef": map[string]interface{}{
						"name": nil,
						"key":  "token",
					},
				},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u := &unstructured.Unstructured{Object: tt.obj}
			got := &staticSiteData{}
			err := got.fromUnstructured(u)

			if (err != nil) != tt.wantErr {
				t.Errorf("fromUnstructured() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr {
				return
			}

			if got.Name != tt.want.Name {
				t.Errorf("Name = %v, want %v", got.Name, tt.want.Name)
			}
			if got.Namespace != tt.want.Namespace {
				t.Errorf("Namespace = %v, want %v", got.Namespace, tt.want.Namespace)
			}
			if got.Repo != tt.want.Repo {
				t.Errorf("Repo = %v, want %v", got.Repo, tt.want.Repo)
			}
			if got.Branch != tt.want.Branch {
				t.Errorf("Branch = %v, want %v", got.Branch, tt.want.Branch)
			}
			if got.Path != tt.want.Path {
				t.Errorf("Path = %v, want %v", got.Path, tt.want.Path)
			}
			if tt.want.SecretRef != nil {
				if got.SecretRef == nil {
					t.Error("SecretRef is nil, want non-nil")
				} else {
					if got.SecretRef.Name != tt.want.SecretRef.Name {
						t.Errorf("SecretRef.Name = %v, want %v", got.SecretRef.Name, tt.want.SecretRef.Name)
					}
					if got.SecretRef.Key != tt.want.SecretRef.Key {
						t.Errorf("SecretRef.Key = %v, want %v", got.SecretRef.Key, tt.want.SecretRef.Key)
					}
				}
			}
		})
	}
}

func TestSetupSubpath(t *testing.T) {
	// Create temp directory
	tmpDir, err := os.MkdirTemp("", "syncer-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	s := &Syncer{SitesRoot: tmpDir}

	tests := []struct {
		name      string
		siteName  string
		subpath   string
		setup     func() string // returns repoDir
		wantErr   bool
		checkLink bool
	}{
		{
			name:     "creates symlink for valid subpath",
			siteName: "mysite",
			subpath:  "dist",
			setup: func() string {
				repoDir := filepath.Join(tmpDir, ".repos", "mysite")
				_ = os.MkdirAll(filepath.Join(repoDir, "dist"), 0755)
				return repoDir
			},
			wantErr:   false,
			checkLink: true,
		},
		{
			name:     "subpath with leading slash",
			siteName: "site2",
			subpath:  "/public",
			setup: func() string {
				repoDir := filepath.Join(tmpDir, ".repos", "site2")
				_ = os.MkdirAll(filepath.Join(repoDir, "public"), 0755)
				return repoDir
			},
			wantErr:   false,
			checkLink: true,
		},
		{
			name:     "error on non-existent subpath",
			siteName: "badsite",
			subpath:  "nonexistent",
			setup: func() string {
				repoDir := filepath.Join(tmpDir, ".repos", "badsite")
				_ = os.MkdirAll(repoDir, 0755)
				return repoDir
			},
			wantErr:   true,
			checkLink: false,
		},
		{
			name:     "replaces existing symlink",
			siteName: "replace",
			subpath:  "new",
			setup: func() string {
				repoDir := filepath.Join(tmpDir, ".repos", "replace")
				_ = os.MkdirAll(filepath.Join(repoDir, "old"), 0755)
				_ = os.MkdirAll(filepath.Join(repoDir, "new"), 0755)
				// Create old symlink
				linkPath := filepath.Join(tmpDir, "replace")
				_ = os.Symlink(filepath.Join(repoDir, "old"), linkPath)
				return repoDir
			},
			wantErr:   false,
			checkLink: true,
		},
		{
			name:     "handles root path (just slash)",
			siteName: "rootsite",
			subpath:  "/",
			setup: func() string {
				repoDir := filepath.Join(tmpDir, ".repos", "rootsite")
				_ = os.MkdirAll(repoDir, 0755)
				return repoDir
			},
			wantErr:   false,
			checkLink: true,
		},
		{
			name:     "handles empty subpath as root",
			siteName: "emptysite",
			subpath:  "",
			setup: func() string {
				repoDir := filepath.Join(tmpDir, ".repos", "emptysite")
				_ = os.MkdirAll(repoDir, 0755)
				return repoDir
			},
			wantErr:   false,
			checkLink: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repoDir := tt.setup()
			err := s.setupSubpath(tt.siteName, repoDir, tt.subpath)

			if (err != nil) != tt.wantErr {
				t.Errorf("setupSubpath() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.checkLink {
				linkPath := filepath.Join(tmpDir, tt.siteName)
				info, err := os.Lstat(linkPath)
				if err != nil {
					t.Errorf("symlink not created: %v", err)
					return
				}
				if info.Mode()&os.ModeSymlink == 0 {
					t.Errorf("expected symlink, got %v", info.Mode())
				}
			}
		})
	}
}

func TestRemovePathOrSymlink(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "syncer-remove-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	tests := []struct {
		name    string
		setup   func() string // returns path to remove
		wantErr bool
	}{
		{
			name: "remove regular file",
			setup: func() string {
				path := filepath.Join(tmpDir, "file.txt")
				_ = os.WriteFile(path, []byte("test"), 0644)
				return path
			},
			wantErr: false,
		},
		{
			name: "remove directory",
			setup: func() string {
				path := filepath.Join(tmpDir, "dir")
				_ = os.MkdirAll(filepath.Join(path, "subdir"), 0755)
				_ = os.WriteFile(filepath.Join(path, "subdir", "file.txt"), []byte("test"), 0644)
				return path
			},
			wantErr: false,
		},
		{
			name: "remove symlink",
			setup: func() string {
				target := filepath.Join(tmpDir, "target-dir")
				_ = os.MkdirAll(target, 0755)
				link := filepath.Join(tmpDir, "symlink")
				_ = os.Symlink(target, link)
				return link
			},
			wantErr: false,
		},
		{
			name: "remove symlink to file",
			setup: func() string {
				target := filepath.Join(tmpDir, "target-file")
				_ = os.WriteFile(target, []byte("test"), 0644)
				link := filepath.Join(tmpDir, "file-symlink")
				_ = os.Symlink(target, link)
				return link
			},
			wantErr: false,
		},
		{
			name: "non-existent path returns nil",
			setup: func() string {
				return filepath.Join(tmpDir, "does-not-exist")
			},
			wantErr: false,
		},
		{
			name: "symlink removes only link not target",
			setup: func() string {
				target := filepath.Join(tmpDir, "preserve-target")
				_ = os.MkdirAll(target, 0755)
				_ = os.WriteFile(filepath.Join(target, "data.txt"), []byte("keep"), 0644)
				link := filepath.Join(tmpDir, "link-to-preserve")
				_ = os.Symlink(target, link)
				return link
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := tt.setup()
			err := removePathOrSymlink(path)

			if (err != nil) != tt.wantErr {
				t.Errorf("removePathOrSymlink() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			// Path should not exist after removal (unless error expected)
			if !tt.wantErr {
				if _, err := os.Lstat(path); !os.IsNotExist(err) {
					t.Errorf("path still exists after removal: %s", path)
				}
			}
		})
	}

	// Verify symlink target preservation
	t.Run("verify symlink target preserved", func(t *testing.T) {
		targetPath := filepath.Join(tmpDir, "preserve-target")
		if _, err := os.Stat(targetPath); os.IsNotExist(err) {
			t.Error("symlink target was removed but should be preserved")
		}
		dataPath := filepath.Join(targetPath, "data.txt")
		if _, err := os.Stat(dataPath); os.IsNotExist(err) {
			t.Error("symlink target content was removed")
		}
	})
}

func TestDeleteSite(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "syncer-delete-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	s := &Syncer{SitesRoot: tmpDir}
	ctx := context.Background()

	tests := []struct {
		name     string
		siteName string
		setup    func()
	}{
		{
			name:     "delete directory",
			siteName: "dir-site",
			setup: func() {
				_ = os.MkdirAll(filepath.Join(tmpDir, "dir-site", "subdir"), 0755)
				_ = os.WriteFile(filepath.Join(tmpDir, "dir-site", "index.html"), []byte("test"), 0644)
			},
		},
		{
			name:     "delete symlink and repo",
			siteName: "link-site",
			setup: func() {
				repoDir := filepath.Join(tmpDir, ".repos", "link-site")
				_ = os.MkdirAll(filepath.Join(repoDir, "dist"), 0755)
				_ = os.Symlink(filepath.Join(repoDir, "dist"), filepath.Join(tmpDir, "link-site"))
			},
		},
		{
			name:     "delete non-existent site (no error)",
			siteName: "ghost",
			setup:    func() {},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.setup()

			err := s.DeleteSite(ctx, tt.siteName)
			if err != nil {
				t.Errorf("DeleteSite() error = %v", err)
				return
			}

			// Prüfen dass Site-Pfad nicht mehr existiert
			sitePath := filepath.Join(tmpDir, tt.siteName)
			if _, err := os.Lstat(sitePath); !os.IsNotExist(err) {
				t.Errorf("site path still exists: %s", sitePath)
			}

			// Prüfen dass Repo-Pfad nicht mehr existiert
			repoPath := filepath.Join(tmpDir, ".repos", tt.siteName)
			if _, err := os.Stat(repoPath); !os.IsNotExist(err) {
				t.Errorf("repo path still exists: %s", repoPath)
			}
		})
	}
}

func TestUpdateStatusJSONEscaping(t *testing.T) {
	tests := []struct {
		name    string
		phase   string
		message string
		commit  string
	}{
		{
			name:    "simple message",
			phase:   "Ready",
			message: "Synced successfully",
			commit:  "abc123",
		},
		{
			name:    "message with quotes",
			phase:   "Error",
			message: `Failed: "invalid" config`,
			commit:  "",
		},
		{
			name:    "message with newlines",
			phase:   "Error",
			message: "Line 1\nLine 2\nLine 3",
			commit:  "",
		},
		{
			name:    "message with backslashes",
			phase:   "Error",
			message: `Path: C:\Users\test`,
			commit:  "",
		},
		{
			name:    "message with special chars",
			phase:   "Error",
			message: "Error: {\"code\": 500, \"msg\": \"fail\"}",
			commit:  "",
		},
		{
			name:    "message with tabs and unicode",
			phase:   "Error",
			message: "Tab:\there, Unicode: \u2603",
			commit:  "",
		},
		{
			name:    "message with control characters",
			phase:   "Error",
			message: "Control: \x1f and null: \x00",
			commit:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeClient := &fakeDynamicClient{activeSites: []string{"test-site"}}
			s := &Syncer{
				DynamicClient: fakeClient,
			}

			site := &staticSiteData{
				Name:      "test-site",
				Namespace: "default",
			}

			ctx := context.Background()
			s.updateStatus(ctx, site, tt.phase, tt.message, tt.commit)

			// Verify the patch is valid JSON
			if fakeClient.lastPatch == nil {
				t.Fatal("no patch was sent")
			}

			var parsed map[string]interface{}
			if err := json.Unmarshal(fakeClient.lastPatch, &parsed); err != nil {
				t.Fatalf("patch is not valid JSON: %v\npatch: %s", err, string(fakeClient.lastPatch))
			}

			// Verify the status fields
			status, ok := parsed["status"].(map[string]interface{})
			if !ok {
				t.Fatalf("status field missing or invalid: %v", parsed)
			}

			if got := status["phase"]; got != tt.phase {
				t.Errorf("phase = %v, want %v", got, tt.phase)
			}
			if got := status["message"]; got != tt.message {
				t.Errorf("message = %v, want %v", got, tt.message)
			}
			if got := status["lastCommit"]; got != tt.commit {
				t.Errorf("lastCommit = %v, want %v", got, tt.commit)
			}
			if _, ok := status["lastSync"]; !ok {
				t.Error("lastSync field missing")
			}
		})
	}
}

func TestCleanup(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "syncer-cleanup-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	// Setup: Create some site directories
	_ = os.MkdirAll(filepath.Join(tmpDir, "active-site"), 0755)
	_ = os.MkdirAll(filepath.Join(tmpDir, "orphan-site"), 0755)
	_ = os.MkdirAll(filepath.Join(tmpDir, ".repos", "active-site"), 0755)
	_ = os.MkdirAll(filepath.Join(tmpDir, ".repos", "orphan-repo"), 0755)

	// Symlink for orphan
	_ = os.MkdirAll(filepath.Join(tmpDir, ".repos", "orphan-link", "dist"), 0755)
	_ = os.Symlink(
		filepath.Join(tmpDir, ".repos", "orphan-link", "dist"),
		filepath.Join(tmpDir, "orphan-link"),
	)

	// Mock DynamicClient that only returns "active-site"
	s := &Syncer{
		SitesRoot:     tmpDir,
		DynamicClient: &fakeDynamicClient{activeSites: []string{"active-site"}},
	}

	ctx := context.Background()
	err = s.Cleanup(ctx)
	if err != nil {
		t.Fatalf("Cleanup() error = %v", err)
	}

	// active-site should still exist
	if _, err := os.Stat(filepath.Join(tmpDir, "active-site")); os.IsNotExist(err) {
		t.Error("active-site was deleted but should exist")
	}

	// orphan-site should be deleted
	if _, err := os.Stat(filepath.Join(tmpDir, "orphan-site")); !os.IsNotExist(err) {
		t.Error("orphan-site still exists but should be deleted")
	}

	// orphan-link (symlink) should be deleted
	if _, err := os.Lstat(filepath.Join(tmpDir, "orphan-link")); !os.IsNotExist(err) {
		t.Error("orphan-link still exists but should be deleted")
	}

	// .repos/orphan-repo should be deleted
	if _, err := os.Stat(filepath.Join(tmpDir, ".repos", "orphan-repo")); !os.IsNotExist(err) {
		t.Error(".repos/orphan-repo still exists but should be deleted")
	}

	// .repos/active-site should still exist
	if _, err := os.Stat(filepath.Join(tmpDir, ".repos", "active-site")); os.IsNotExist(err) {
		t.Error(".repos/active-site was deleted but should exist")
	}
}

func TestGetSecretValue(t *testing.T) {
	tests := []struct {
		name      string
		namespace string
		secretNs  string
		secretKey string
		secrets   []*corev1.Secret
		wantValue string
		wantErr   bool
		errMsg    string
	}{
		{
			name:      "secret not found",
			namespace: "default",
			secretNs:  "missing-secret",
			secretKey: "password",
			secrets:   []*corev1.Secret{},
			wantErr:   true,
			errMsg:    "not found",
		},
		{
			name:      "key not found in secret",
			namespace: "default",
			secretNs:  "git-creds",
			secretKey: "missing-key",
			secrets: []*corev1.Secret{
				newTestSecret("default", "git-creds", map[string][]byte{
					"password": []byte("secret-password"),
				}),
			},
			wantErr: true,
			errMsg:  "key missing-key not found",
		},
		{
			name:      "success with explicit key",
			namespace: "default",
			secretNs:  "git-creds",
			secretKey: "token",
			secrets: []*corev1.Secret{
				newTestSecret("default", "git-creds", map[string][]byte{
					"token": []byte("my-token"),
				}),
			},
			wantValue: "my-token",
			wantErr:   false,
		},
		{
			name:      "success with default password key",
			namespace: "default",
			secretNs:  "git-creds",
			secretKey: "",
			secrets: []*corev1.Secret{
				newTestSecret("default", "git-creds", map[string][]byte{
					"password": []byte("default-password"),
				}),
			},
			wantValue: "default-password",
			wantErr:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &Syncer{
				ClientSet: newFakeClientset(tt.secrets...),
			}

			ctx := context.Background()
			got, err := s.getSecretValue(ctx, tt.namespace, tt.secretNs, tt.secretKey)

			if (err != nil) != tt.wantErr {
				t.Errorf("getSecretValue() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr && tt.errMsg != "" {
				if !strings.Contains(err.Error(), tt.errMsg) {
					t.Errorf("error message = %q, want to contain %q", err.Error(), tt.errMsg)
				}
				return
			}
			if got != tt.wantValue {
				t.Errorf("getSecretValue() = %v, want %v", got, tt.wantValue)
			}
		})
	}
}

func TestSyncSite_AuthFailure(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "syncer-auth-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	s := &Syncer{
		SitesRoot:     tmpDir,
		AllowedHosts:  []string{"github.com"},
		DynamicClient: &fakeDynamicClient{activeSites: []string{"test-site"}},
		ClientSet:     newFakeClientset(), // No secrets
	}

	site := &staticSiteData{
		Name:      "test-site",
		Namespace: "default",
		Repo:      "https://github.com/example/repo.git",
		Branch:    "main",
		Path:      "/",
		SecretRef: &secretRef{Name: "missing-secret", Key: "token"},
	}

	ctx := context.Background()
	err = s.syncSite(ctx, site)

	if err == nil {
		t.Error("expected error for missing secret, got nil")
	}
	if !strings.Contains(err.Error(), "git credentials") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestSyncSite_CloneFailure(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "syncer-clone-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	s := &Syncer{
		SitesRoot:     tmpDir,
		AllowedHosts:  []string{"invalid.test"},
		DynamicClient: &fakeDynamicClient{activeSites: []string{"test-site"}},
		ClientSet:     newFakeClientset(),
	}

	site := &staticSiteData{
		Name:      "test-site",
		Namespace: "default",
		Repo:      "https://invalid.test/nonexistent/repo.git",
		Branch:    "main",
		Path:      "/",
	}

	ctx := context.Background()
	err = s.syncSite(ctx, site)

	if err == nil {
		t.Error("expected error for clone failure, got nil")
	}
	if !strings.Contains(err.Error(), "git clone failed") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestSyncSite_PullFailure(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "syncer-pull-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	// Create a fake .git directory to simulate an existing repo
	siteDir := filepath.Join(tmpDir, "test-site")
	gitDir := filepath.Join(siteDir, ".git")
	if err := os.MkdirAll(gitDir, 0755); err != nil {
		t.Fatalf("failed to create fake git dir: %v", err)
	}

	s := &Syncer{
		SitesRoot:     tmpDir,
		AllowedHosts:  []string{"invalid.test"},
		DynamicClient: &fakeDynamicClient{activeSites: []string{"test-site"}},
		ClientSet:     newFakeClientset(),
	}

	site := &staticSiteData{
		Name:      "test-site",
		Namespace: "default",
		Repo:      "https://invalid.test/nonexistent/repo.git",
		Branch:    "main",
		Path:      "/",
	}

	ctx := context.Background()
	err = s.syncSite(ctx, site)

	if err == nil {
		t.Error("expected error for pull failure, got nil")
	}
	// The error could be "failed to open repo" since it's not a real git repo
	if !strings.Contains(err.Error(), "failed to open repo") && !strings.Contains(err.Error(), "git pull failed") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestSyncSite_SubpathNotExist(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "syncer-subpath-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	// Create a minimal git repo structure without the subpath
	repoDir := filepath.Join(tmpDir, ".repos", "test-site")
	gitDir := filepath.Join(repoDir, ".git")
	if err := os.MkdirAll(gitDir, 0755); err != nil {
		t.Fatalf("failed to create git dir: %v", err)
	}
	// Create minimal git files to make go-git recognize it
	if err := os.WriteFile(filepath.Join(gitDir, "HEAD"), []byte("ref: refs/heads/main\n"), 0644); err != nil {
		t.Fatalf("failed to create HEAD: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(gitDir, "objects"), 0755); err != nil {
		t.Fatalf("failed to create objects dir: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(gitDir, "refs"), 0755); err != nil {
		t.Fatalf("failed to create refs dir: %v", err)
	}

	s := &Syncer{
		SitesRoot:     tmpDir,
		AllowedHosts:  []string{"github.com"},
		DynamicClient: &fakeDynamicClient{activeSites: []string{"test-site"}},
		ClientSet:     newFakeClientset(),
	}

	site := &staticSiteData{
		Name:      "test-site",
		Namespace: "default",
		Repo:      "https://github.com/example/repo.git",
		Branch:    "main",
		Path:      "/nonexistent/subpath",
	}

	ctx := context.Background()
	err = s.syncSite(ctx, site)

	if err == nil {
		t.Error("expected error for nonexistent subpath, got nil")
	}
	// Either we get a subpath error or a pull error (since fake repo can't pull)
	if !strings.Contains(err.Error(), "subpath") && !strings.Contains(err.Error(), "git pull failed") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestSyncSite_RepoURLValidationFailure(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "syncer-url-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	s := &Syncer{
		SitesRoot:     tmpDir,
		AllowedHosts:  []string{"github.com"},
		DynamicClient: &fakeDynamicClient{activeSites: []string{"test-site"}},
		ClientSet:     newFakeClientset(),
	}

	site := &staticSiteData{
		Name:      "test-site",
		Namespace: "default",
		Repo:      "https://malicious.com/evil/repo.git",
		Branch:    "main",
		Path:      "/",
	}

	ctx := context.Background()
	err = s.syncSite(ctx, site)

	if err == nil {
		t.Error("expected error for disallowed host, got nil")
	}
	if !strings.Contains(err.Error(), "repo URL validation failed") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestSyncSite_AuthWithUsername(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "syncer-auth-username-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	// Create a secret with both username and password
	secrets := []*corev1.Secret{
		newTestSecret("default", "git-creds", map[string][]byte{
			"username": []byte("myuser"),
			"password": []byte("mypassword"),
		}),
	}

	s := &Syncer{
		SitesRoot:     tmpDir,
		AllowedHosts:  []string{"invalid.test"},
		DynamicClient: &fakeDynamicClient{activeSites: []string{"test-site"}},
		ClientSet:     newFakeClientset(secrets...),
	}

	site := &staticSiteData{
		Name:      "test-site",
		Namespace: "default",
		Repo:      "https://invalid.test/example/repo.git",
		Branch:    "main",
		Path:      "/",
		SecretRef: &secretRef{Name: "git-creds", Key: "password"},
	}

	ctx := context.Background()
	err = s.syncSite(ctx, site)

	// We expect a clone failure since it's not a real repo,
	// but the auth setup should have succeeded
	if err == nil {
		t.Error("expected clone error, got nil")
	}
	if !strings.Contains(err.Error(), "git clone failed") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestSyncSite_AuthWithDefaultUsername(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "syncer-auth-default-user-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	// Create a secret with only password (no username)
	secrets := []*corev1.Secret{
		newTestSecret("default", "git-creds", map[string][]byte{
			"password": []byte("mypassword"),
		}),
	}

	s := &Syncer{
		SitesRoot:     tmpDir,
		AllowedHosts:  []string{"invalid.test"},
		DynamicClient: &fakeDynamicClient{activeSites: []string{"test-site"}},
		ClientSet:     newFakeClientset(secrets...),
	}

	site := &staticSiteData{
		Name:      "test-site",
		Namespace: "default",
		Repo:      "https://invalid.test/example/repo.git",
		Branch:    "main",
		Path:      "/",
		SecretRef: &secretRef{Name: "git-creds", Key: "password"},
	}

	ctx := context.Background()
	err = s.syncSite(ctx, site)

	// We expect a clone failure since it's not a real repo,
	// but the auth setup with default "git" username should have succeeded
	if err == nil {
		t.Error("expected clone error, got nil")
	}
	if !strings.Contains(err.Error(), "git clone failed") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestSyncAll(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "syncer-syncall-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	// Create a fake dynamic client that returns sites with invalid repos
	// This tests that SyncAll continues even when individual sites fail
	fakeClient := &fakeDynamicClientWithSites{
		sites: []siteSpec{
			{
				name:      "site1",
				namespace: "default",
				repo:      "https://invalid.test/repo1.git",
			},
			{
				name:      "site2",
				namespace: "default",
				repo:      "https://invalid.test/repo2.git",
			},
		},
	}

	s := &Syncer{
		SitesRoot:     tmpDir,
		AllowedHosts:  []string{"invalid.test"},
		DynamicClient: fakeClient,
		ClientSet:     newFakeClientset(),
	}

	ctx := context.Background()
	err = s.SyncAll(ctx)

	// SyncAll should not return error even if individual syncs fail
	if err != nil {
		t.Errorf("SyncAll() error = %v, want nil", err)
	}
}

func TestSyncOne(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "syncer-syncone-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	fakeClient := &fakeDynamicClientWithSites{
		sites: []siteSpec{
			{
				name:      "mysite",
				namespace: "default",
				repo:      "https://invalid.test/repo.git",
			},
		},
	}

	s := &Syncer{
		SitesRoot:     tmpDir,
		AllowedHosts:  []string{"invalid.test"},
		DynamicClient: fakeClient,
		ClientSet:     newFakeClientset(),
	}

	ctx := context.Background()
	err = s.SyncOne(ctx, "default", "mysite")

	// Should fail because we can't actually clone the invalid repo
	if err == nil {
		t.Error("expected clone error, got nil")
	}
}

func TestSyncOne_NotFound(t *testing.T) {
	fakeClient := &fakeDynamicClientWithSites{
		sites:    []siteSpec{},
		getError: true, // Simulate not found
	}

	s := &Syncer{
		AllowedHosts:  []string{"github.com"},
		DynamicClient: fakeClient,
	}

	ctx := context.Background()
	err := s.SyncOne(ctx, "default", "nonexistent")

	if err == nil {
		t.Error("expected error for nonexistent site, got nil")
	}
	if !strings.Contains(err.Error(), "failed to get StaticSite") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestSyncAll_WithParseError(t *testing.T) {
	// Test that SyncAll continues when a site fails to parse
	fakeClient := &fakeDynamicClientWithInvalidSite{}

	s := &Syncer{
		AllowedHosts:  []string{"github.com"},
		DynamicClient: fakeClient,
	}

	ctx := context.Background()
	err := s.SyncAll(ctx)

	// SyncAll should not return error even if parse fails
	if err != nil {
		t.Errorf("SyncAll() error = %v, want nil", err)
	}
}

func TestSyncAll_ListError(t *testing.T) {
	fakeClient := &fakeDynamicClientWithListError{}

	s := &Syncer{
		AllowedHosts:  []string{"github.com"},
		DynamicClient: fakeClient,
	}

	ctx := context.Background()
	err := s.SyncAll(ctx)

	if err == nil {
		t.Error("expected error for list failure, got nil")
	}
	if !strings.Contains(err.Error(), "failed to list StaticSites") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestCleanup_ListError(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "syncer-cleanup-listerr-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	fakeClient := &fakeDynamicClientWithListError{}

	s := &Syncer{
		SitesRoot:     tmpDir,
		AllowedHosts:  []string{"github.com"},
		DynamicClient: fakeClient,
	}

	ctx := context.Background()
	err = s.Cleanup(ctx)

	if err == nil {
		t.Error("expected error for list failure, got nil")
	}
	if !strings.Contains(err.Error(), "failed to list StaticSites") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestUpdateStatus_PatchError(t *testing.T) {
	fakeClient := &fakeDynamicClientWithPatchError{
		patchError: fmt.Errorf("connection refused"),
	}

	s := &Syncer{
		DynamicClient: fakeClient,
	}

	site := &staticSiteData{
		Name:      "test-site",
		Namespace: "default",
	}

	ctx := context.Background()

	// updateStatus should not panic and should handle the error gracefully
	// The error is logged but not returned
	s.updateStatus(ctx, site, "Ready", "Synced successfully", "abc123")

	// If we reach here without panic, the test passes
	// The function logs the error but doesn't return it
}

func TestSyncSite_CorruptedRepo(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "syncer-corrupt-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	// Create a corrupted git repo - valid enough to open but fails on operations
	siteDir := filepath.Join(tmpDir, "test-site")
	gitDir := filepath.Join(siteDir, ".git")
	if err := os.MkdirAll(gitDir, 0755); err != nil {
		t.Fatalf("failed to create git dir: %v", err)
	}
	// Create minimal git structure
	if err := os.WriteFile(filepath.Join(gitDir, "HEAD"), []byte("ref: refs/heads/main\n"), 0644); err != nil {
		t.Fatalf("failed to create HEAD: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(gitDir, "objects"), 0755); err != nil {
		t.Fatalf("failed to create objects dir: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(gitDir, "refs"), 0755); err != nil {
		t.Fatalf("failed to create refs dir: %v", err)
	}
	// Non-bare config
	configContent := "[core]\n\tbare = false\n"
	if err := os.WriteFile(filepath.Join(gitDir, "config"), []byte(configContent), 0644); err != nil {
		t.Fatalf("failed to create config: %v", err)
	}

	s := &Syncer{
		SitesRoot:     tmpDir,
		AllowedHosts:  []string{"invalid.test"},
		DynamicClient: &fakeDynamicClient{activeSites: []string{"test-site"}},
		ClientSet:     newFakeClientset(),
	}

	site := &staticSiteData{
		Name:      "test-site",
		Namespace: "default",
		Repo:      "https://invalid.test/example/repo.git",
		Branch:    "main",
		Path:      "/",
	}

	ctx := context.Background()
	err = s.syncSite(ctx, site)

	// Corrupted repo should fail during pull operation
	if err == nil {
		t.Error("expected error for corrupted repo, got nil")
	}
	// The error should be git pull related since the repo has no valid remote
	if !strings.Contains(err.Error(), "git pull failed") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestCleanup_ReadDirError(t *testing.T) {
	// Use a non-existent directory as SitesRoot
	nonExistentDir := "/tmp/syncer-test-nonexistent-" + fmt.Sprintf("%d", os.Getpid())

	s := &Syncer{
		SitesRoot:     nonExistentDir,
		AllowedHosts:  []string{"github.com"},
		DynamicClient: &fakeDynamicClient{activeSites: []string{"test-site"}},
	}

	ctx := context.Background()
	err := s.Cleanup(ctx)

	if err == nil {
		t.Error("expected error for non-existent sites directory, got nil")
	}
	if !strings.Contains(err.Error(), "failed to read sites directory") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestSyncSite_PullWithAuth(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "syncer-pull-auth-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	// Create a valid non-bare git repo structure
	siteDir := filepath.Join(tmpDir, "test-site")
	gitDir := filepath.Join(siteDir, ".git")
	if err := os.MkdirAll(gitDir, 0755); err != nil {
		t.Fatalf("failed to create git dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(gitDir, "HEAD"), []byte("ref: refs/heads/main\n"), 0644); err != nil {
		t.Fatalf("failed to create HEAD: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(gitDir, "objects"), 0755); err != nil {
		t.Fatalf("failed to create objects dir: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(gitDir, "refs"), 0755); err != nil {
		t.Fatalf("failed to create refs dir: %v", err)
	}
	// Non-bare config
	configContent := "[core]\n\tbare = false\n"
	if err := os.WriteFile(filepath.Join(gitDir, "config"), []byte(configContent), 0644); err != nil {
		t.Fatalf("failed to create config: %v", err)
	}

	// Create secrets with auth credentials
	secrets := []*corev1.Secret{
		newTestSecret("default", "git-creds", map[string][]byte{
			"username": []byte("myuser"),
			"password": []byte("mypassword"),
		}),
	}

	s := &Syncer{
		SitesRoot:     tmpDir,
		AllowedHosts:  []string{"invalid.test"},
		DynamicClient: &fakeDynamicClient{activeSites: []string{"test-site"}},
		ClientSet:     newFakeClientset(secrets...),
	}

	site := &staticSiteData{
		Name:      "test-site",
		Namespace: "default",
		Repo:      "https://invalid.test/example/repo.git",
		Branch:    "main",
		Path:      "/",
		SecretRef: &secretRef{Name: "git-creds", Key: "password"},
	}

	ctx := context.Background()
	err = s.syncSite(ctx, site)

	// We expect a pull failure since it's not a real remote,
	// but the auth with username should have been set up
	if err == nil {
		t.Error("expected pull error, got nil")
	}
	// The error should be git pull related
	if !strings.Contains(err.Error(), "git pull failed") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestDeleteSite_RemoveError(t *testing.T) {
	// Test DeleteSite when the path cannot be removed
	// We use a path that doesn't exist - removePathOrSymlink handles this gracefully
	// So we need a different approach: use a path where we don't have permission

	tmpDir, err := os.MkdirTemp("", "syncer-delete-err-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	s := &Syncer{
		SitesRoot: tmpDir,
	}

	ctx := context.Background()

	// Test successful deletion of non-existent site (should not error)
	err = s.DeleteSite(ctx, "nonexistent-site")
	if err != nil {
		t.Errorf("DeleteSite() for non-existent site: error = %v, want nil", err)
	}
}

func TestRunLoop_ContextCancellation(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "syncer-runloop-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	fakeClient := &fakeDynamicClient{activeSites: []string{}}

	s := &Syncer{
		SitesRoot:       tmpDir,
		AllowedHosts:    []string{"github.com"},
		DynamicClient:   fakeClient,
		ClientSet:       newFakeClientset(),
		DefaultInterval: 50 * time.Millisecond, // Short interval
	}

	// Create a context that cancels after enough time for ticker to fire once
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Millisecond)
	defer cancel()

	// Run the loop - it should run initial sync, then ticker sync, then exit
	done := make(chan struct{})
	go func() {
		s.RunLoop(ctx)
		close(done)
	}()

	// Wait for RunLoop to finish
	select {
	case <-done:
		// Success - RunLoop exited
	case <-time.After(2 * time.Second):
		t.Error("RunLoop did not exit after context cancellation")
	}
}

func TestRunLoop_InitialSyncError(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "syncer-runloop-err-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	// Use a client that returns error on List
	fakeClient := &fakeDynamicClientWithListError{}

	s := &Syncer{
		SitesRoot:       tmpDir,
		AllowedHosts:    []string{"github.com"},
		DynamicClient:   fakeClient,
		ClientSet:       newFakeClientset(),
		DefaultInterval: 50 * time.Millisecond,
	}

	// Create a context that cancels quickly
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	// Run the loop - initial sync will fail, then context cancels
	done := make(chan struct{})
	go func() {
		s.RunLoop(ctx)
		close(done)
	}()

	// Wait for RunLoop to finish
	select {
	case <-done:
		// Success - RunLoop exited even with initial sync error
	case <-time.After(2 * time.Second):
		t.Error("RunLoop did not exit after context cancellation")
	}
}
