package syncer

import (
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
			name:         "empty allowlist allows all",
			allowedHosts: nil,
			repoURL:      "https://github.com/example/repo.git",
			wantErr:      false,
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
