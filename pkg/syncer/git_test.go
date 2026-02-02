package syncer

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

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
	// Temp-Verzeichnis erstellen
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
				// Alten Symlink erstellen
				linkPath := filepath.Join(tmpDir, "replace")
				_ = os.Symlink(filepath.Join(repoDir, "old"), linkPath)
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

	// Setup: Einige Site-Verzeichnisse erstellen
	_ = os.MkdirAll(filepath.Join(tmpDir, "active-site"), 0755)
	_ = os.MkdirAll(filepath.Join(tmpDir, "orphan-site"), 0755)
	_ = os.MkdirAll(filepath.Join(tmpDir, ".repos", "active-site"), 0755)
	_ = os.MkdirAll(filepath.Join(tmpDir, ".repos", "orphan-repo"), 0755)

	// Symlink für orphan
	_ = os.MkdirAll(filepath.Join(tmpDir, ".repos", "orphan-link", "dist"), 0755)
	_ = os.Symlink(
		filepath.Join(tmpDir, ".repos", "orphan-link", "dist"),
		filepath.Join(tmpDir, "orphan-link"),
	)

	// Mock DynamicClient der nur "active-site" zurückgibt
	s := &Syncer{
		SitesRoot:     tmpDir,
		DynamicClient: &fakeDynamicClient{activeSites: []string{"active-site"}},
	}

	ctx := context.Background()
	err = s.Cleanup(ctx)
	if err != nil {
		t.Fatalf("Cleanup() error = %v", err)
	}

	// active-site sollte noch existieren
	if _, err := os.Stat(filepath.Join(tmpDir, "active-site")); os.IsNotExist(err) {
		t.Error("active-site was deleted but should exist")
	}

	// orphan-site sollte gelöscht sein
	if _, err := os.Stat(filepath.Join(tmpDir, "orphan-site")); !os.IsNotExist(err) {
		t.Error("orphan-site still exists but should be deleted")
	}

	// orphan-link (symlink) sollte gelöscht sein
	if _, err := os.Lstat(filepath.Join(tmpDir, "orphan-link")); !os.IsNotExist(err) {
		t.Error("orphan-link still exists but should be deleted")
	}

	// .repos/orphan-repo sollte gelöscht sein
	if _, err := os.Stat(filepath.Join(tmpDir, ".repos", "orphan-repo")); !os.IsNotExist(err) {
		t.Error(".repos/orphan-repo still exists but should be deleted")
	}

	// .repos/active-site sollte noch existieren
	if _, err := os.Stat(filepath.Join(tmpDir, ".repos", "active-site")); os.IsNotExist(err) {
		t.Error(".repos/active-site was deleted but should exist")
	}
}
