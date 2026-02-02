package syncer

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestValidateWebhookSignature(t *testing.T) {
	tests := []struct {
		name      string
		secret    string
		body      string
		signature string
		prefix    string
		want      bool
	}{
		{
			name:      "no secret configured allows all",
			secret:    "",
			body:      "test body",
			signature: "anything",
			prefix:    "sha256=",
			want:      true,
		},
		{
			name:      "valid signature",
			secret:    "mysecret",
			body:      "test body",
			signature: "sha256=" + computeHMAC("test body", "mysecret"),
			prefix:    "sha256=",
			want:      true,
		},
		{
			name:      "invalid signature",
			secret:    "mysecret",
			body:      "test body",
			signature: "sha256=invalid",
			prefix:    "sha256=",
			want:      false,
		},
		{
			name:      "missing prefix",
			secret:    "mysecret",
			body:      "test body",
			signature: computeHMAC("test body", "mysecret"),
			prefix:    "sha256=",
			want:      false,
		},
		{
			name:      "empty prefix validation",
			secret:    "mysecret",
			body:      "test body",
			signature: computeHMAC("test body", "mysecret"),
			prefix:    "",
			want:      true,
		},
		{
			name:      "wrong body content",
			secret:    "mysecret",
			body:      "different body",
			signature: "sha256=" + computeHMAC("test body", "mysecret"),
			prefix:    "sha256=",
			want:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := &WebhookServer{
				WebhookSecret: tt.secret,
			}
			got := w.validateWebhookSignature([]byte(tt.body), tt.signature, tt.prefix)
			if got != tt.want {
				t.Errorf("validateWebhookSignature() = %v, want %v", got, tt.want)
			}
		})
	}
}

// computeHMAC berechnet den HMAC-SHA256 für Tests
func computeHMAC(body, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(body))
	return hex.EncodeToString(mac.Sum(nil))
}

func TestHealthEndpoint(t *testing.T) {
	w := &WebhookServer{}

	req := httptest.NewRequest("GET", "/health", nil)
	rr := httptest.NewRecorder()

	w.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	if rr.Body.String() != "ok" {
		t.Errorf("body = %q, want %q", rr.Body.String(), "ok")
	}
}

func TestRouting(t *testing.T) {
	tests := []struct {
		method     string
		path       string
		wantStatus int
	}{
		{"GET", "/health", http.StatusOK},
		{"GET", "/unknown", http.StatusNotFound},
		{"POST", "/unknown", http.StatusNotFound},
		// Sync und Webhook endpoints brauchen einen echten Syncer,
		// daher testen wir hier nur die Routing-Logik
	}

	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			w := &WebhookServer{}
			req := httptest.NewRequest(tt.method, tt.path, nil)
			rr := httptest.NewRecorder()

			w.ServeHTTP(rr, req)

			if rr.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rr.Code, tt.wantStatus)
			}
		})
	}
}

func TestBranchExtraction(t *testing.T) {
	tests := []struct {
		ref        string
		wantBranch string
	}{
		{"refs/heads/main", "main"},
		{"refs/heads/develop", "develop"},
		{"refs/heads/feature/test", "feature/test"},
		{"main", "main"}, // Falls kein Prefix
	}

	for _, tt := range tests {
		t.Run(tt.ref, func(t *testing.T) {
			branch := strings.TrimPrefix(tt.ref, "refs/heads/")
			if branch != tt.wantBranch {
				t.Errorf("branch = %q, want %q", branch, tt.wantBranch)
			}
		})
	}
}

func TestForgejoPayloadParsing(t *testing.T) {
	payload := `{
		"ref": "refs/heads/main",
		"repository": {
			"full_name": "user/repo",
			"clone_url": "https://forgejo.example.com/user/repo.git"
		}
	}`

	var p WebhookPayload
	if err := json.Unmarshal([]byte(payload), &p); err != nil {
		t.Fatalf("failed to parse payload: %v", err)
	}

	if p.Ref != "refs/heads/main" {
		t.Errorf("Ref = %q, want %q", p.Ref, "refs/heads/main")
	}
	if p.Repository.FullName != "user/repo" {
		t.Errorf("FullName = %q, want %q", p.Repository.FullName, "user/repo")
	}
	if p.Repository.CloneURL != "https://forgejo.example.com/user/repo.git" {
		t.Errorf("CloneURL = %q, want %q", p.Repository.CloneURL, "https://forgejo.example.com/user/repo.git")
	}
}

func TestGitHubPayloadParsing(t *testing.T) {
	payload := `{
		"ref": "refs/heads/main",
		"repository": {
			"full_name": "user/repo",
			"clone_url": "https://github.com/user/repo.git"
		}
	}`

	var p WebhookPayload
	if err := json.Unmarshal([]byte(payload), &p); err != nil {
		t.Fatalf("failed to parse payload: %v", err)
	}

	if p.Ref != "refs/heads/main" {
		t.Errorf("Ref = %q, want %q", p.Ref, "refs/heads/main")
	}
	if p.Repository.FullName != "user/repo" {
		t.Errorf("FullName = %q, want %q", p.Repository.FullName, "user/repo")
	}
}

func TestSyncEndpointRequiresAuth(t *testing.T) {
	// Without a Syncer/DynamicClient, validateSiteToken will fail
	// and return 401 Unauthorized
	w := &WebhookServer{
		Syncer: &Syncer{}, // No DynamicClient configured
	}

	req := httptest.NewRequest("POST", "/sync/default/mysite", nil)
	rr := httptest.NewRecorder()

	w.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d (Unauthorized)", rr.Code, http.StatusUnauthorized)
	}
}

func TestSyncEndpointRequiresAPIKeyHeader(t *testing.T) {
	w := &WebhookServer{
		Syncer: &Syncer{},
	}

	// Request without X-API-Key header
	req := httptest.NewRequest("POST", "/sync/default/mysite", nil)
	rr := httptest.NewRecorder()

	w.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("without X-API-Key: status = %d, want %d", rr.Code, http.StatusUnauthorized)
	}

	// Request with empty X-API-Key header
	req = httptest.NewRequest("POST", "/sync/default/mysite", nil)
	req.Header.Set("X-API-Key", "")
	rr = httptest.NewRecorder()

	w.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("with empty X-API-Key: status = %d, want %d", rr.Code, http.StatusUnauthorized)
	}
}

func TestDeleteEndpointRequiresAuth(t *testing.T) {
	w := &WebhookServer{
		Syncer: &Syncer{},
	}

	// New endpoint format: DELETE /site/{namespace}/{name}
	req := httptest.NewRequest("DELETE", "/site/default/mysite", nil)
	rr := httptest.NewRecorder()

	w.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d (Unauthorized)", rr.Code, http.StatusUnauthorized)
	}
}

func TestDeleteEndpointOldFormatReturns404(t *testing.T) {
	w := &WebhookServer{
		Syncer: &Syncer{},
	}

	// Old format: DELETE /site/{name} should now return 404
	req := httptest.NewRequest("DELETE", "/site/mysite", nil)
	rr := httptest.NewRecorder()

	w.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("old format: status = %d, want %d (NotFound)", rr.Code, http.StatusNotFound)
	}
}

func TestShutdownTimeoutDefault(t *testing.T) {
	w := &WebhookServer{}

	if w.ShutdownTimeout != 0 {
		t.Errorf("default ShutdownTimeout = %v, want 0 (uses DefaultShutdownTimeout)", w.ShutdownTimeout)
	}
}

func TestDefaultShutdownTimeoutValue(t *testing.T) {
	if DefaultShutdownTimeout != 30*time.Second {
		t.Errorf("DefaultShutdownTimeout = %v, want 30s", DefaultShutdownTimeout)
	}
}

func TestHandleSync_Success(t *testing.T) {
	tmpDir := t.TempDir()

	fakeClient := &fakeDynamicClientWithSites{
		sites: []siteSpec{
			{name: "mysite", namespace: "default", repo: "https://invalid.test/repo.git"},
		},
	}

	w := &WebhookServer{
		Syncer: &Syncer{
			SitesRoot:     tmpDir,
			AllowedHosts:  []string{"invalid.test"},
			DynamicClient: fakeClient,
			ClientSet:     newFakeClientset(),
		},
	}

	req := httptest.NewRequest("POST", "/sync/default/mysite", nil)
	req.Header.Set("X-API-Key", "test-token")
	rr := httptest.NewRecorder()

	// Call handleSync directly to bypass auth
	w.handleSync(req.Context(), rr, req, "default", "mysite")

	// Should fail due to clone error, but handler should return 500
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d (Internal Server Error)", rr.Code, http.StatusInternalServerError)
	}
}

func TestHandleDelete_Success(t *testing.T) {
	tmpDir := t.TempDir()

	w := &WebhookServer{
		Syncer: &Syncer{
			SitesRoot: tmpDir,
		},
	}

	req := httptest.NewRequest("DELETE", "/site/default/mysite", nil)
	rr := httptest.NewRecorder()

	// Call handleDelete directly
	w.handleDelete(req.Context(), rr, "default", "mysite")

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	if !strings.Contains(rr.Body.String(), "Deleted") {
		t.Errorf("body = %q, want to contain 'Deleted'", rr.Body.String())
	}
}

func TestGetSiteToken(t *testing.T) {
	fakeClient := &fakeDynamicClientWithSites{
		sites: []siteSpec{
			{name: "mysite", namespace: "default", repo: "https://github.com/test/repo.git"},
		},
	}

	w := &WebhookServer{
		Syncer: &Syncer{
			DynamicClient: fakeClient,
		},
	}

	ctx := context.Background()
	token, err := w.getSiteToken(ctx, "default", "mysite")

	// Token will be empty since our fake doesn't set status.syncToken
	if err != nil {
		t.Errorf("getSiteToken() error = %v, want nil", err)
	}
	// Token is empty since fake doesn't have it in status
	if token != "" {
		t.Errorf("getSiteToken() = %q, want empty", token)
	}
}

func TestGetSiteToken_NotFound(t *testing.T) {
	fakeClient := &fakeDynamicClientWithSites{
		sites:    []siteSpec{},
		getError: true,
	}

	w := &WebhookServer{
		Syncer: &Syncer{
			DynamicClient: fakeClient,
		},
	}

	ctx := context.Background()
	_, err := w.getSiteToken(ctx, "default", "nonexistent")

	if err == nil {
		t.Error("getSiteToken() expected error for nonexistent site")
	}
}

func TestValidateSiteToken_Valid(t *testing.T) {
	fakeClient := &fakeDynamicClientWithToken{
		token: "secret-token",
	}

	w := &WebhookServer{
		Syncer: &Syncer{
			DynamicClient: fakeClient,
		},
	}

	req := httptest.NewRequest("POST", "/sync/default/mysite", nil)
	req.Header.Set("X-API-Key", "secret-token")

	valid := w.validateSiteToken(req.Context(), req, "default", "mysite")
	if !valid {
		t.Error("validateSiteToken() = false, want true")
	}
}

func TestValidateSiteToken_Invalid(t *testing.T) {
	fakeClient := &fakeDynamicClientWithToken{
		token: "secret-token",
	}

	w := &WebhookServer{
		Syncer: &Syncer{
			DynamicClient: fakeClient,
		},
	}

	req := httptest.NewRequest("POST", "/sync/default/mysite", nil)
	req.Header.Set("X-API-Key", "wrong-token")

	valid := w.validateSiteToken(req.Context(), req, "default", "mysite")
	if valid {
		t.Error("validateSiteToken() = true, want false")
	}
}

func TestHandleForgejoWebhook_ValidPayload(t *testing.T) {
	tmpDir := t.TempDir()

	fakeClient := &fakeDynamicClientWithSites{
		sites: []siteSpec{
			{name: "mysite", namespace: "default", repo: "https://forgejo.example.com/user/repo.git", branch: "main"},
		},
	}

	w := &WebhookServer{
		Syncer: &Syncer{
			SitesRoot:     tmpDir,
			AllowedHosts:  []string{"forgejo.example.com"},
			DynamicClient: fakeClient,
			ClientSet:     newFakeClientset(),
		},
	}

	payload := `{"ref": "refs/heads/main", "repository": {"full_name": "user/repo", "clone_url": "https://forgejo.example.com/user/repo.git"}}`
	req := httptest.NewRequest("POST", "/webhook/forgejo", strings.NewReader(payload))
	rr := httptest.NewRecorder()

	w.handleForgejoWebhook(req.Context(), rr, req)

	// Should return OK (sync errors are logged but don't fail the webhook)
	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusOK)
	}
}

func TestHandleForgejoWebhook_InvalidPayload(t *testing.T) {
	w := &WebhookServer{
		Syncer: &Syncer{},
	}

	req := httptest.NewRequest("POST", "/webhook/forgejo", strings.NewReader("invalid json"))
	rr := httptest.NewRecorder()

	w.handleForgejoWebhook(req.Context(), rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d (Bad Request)", rr.Code, http.StatusBadRequest)
	}
}

func TestHandleForgejoWebhook_InvalidSignature(t *testing.T) {
	w := &WebhookServer{
		WebhookSecret: "mysecret",
		Syncer:        &Syncer{},
	}

	payload := `{"ref": "refs/heads/main", "repository": {"full_name": "user/repo", "clone_url": "https://example.com/repo.git"}}`
	req := httptest.NewRequest("POST", "/webhook/forgejo", strings.NewReader(payload))
	req.Header.Set("X-Gitea-Signature", "invalid-signature")
	rr := httptest.NewRecorder()

	w.handleForgejoWebhook(req.Context(), rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d (Unauthorized)", rr.Code, http.StatusUnauthorized)
	}
}

func TestHandleGitHubWebhook_NonPushEvent(t *testing.T) {
	w := &WebhookServer{
		Syncer: &Syncer{},
	}

	req := httptest.NewRequest("POST", "/webhook/github", strings.NewReader("{}"))
	req.Header.Set("X-GitHub-Event", "pull_request")
	rr := httptest.NewRecorder()

	w.handleGitHubWebhook(req.Context(), rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	if !strings.Contains(rr.Body.String(), "ignored event") {
		t.Errorf("body = %q, want to contain 'ignored event'", rr.Body.String())
	}
}

func TestHandleGitHubWebhook_ValidPush(t *testing.T) {
	tmpDir := t.TempDir()

	fakeClient := &fakeDynamicClientWithSites{
		sites: []siteSpec{
			{name: "mysite", namespace: "default", repo: "https://github.com/user/repo.git", branch: "main"},
		},
	}

	w := &WebhookServer{
		Syncer: &Syncer{
			SitesRoot:     tmpDir,
			AllowedHosts:  []string{"github.com"},
			DynamicClient: fakeClient,
			ClientSet:     newFakeClientset(),
		},
	}

	payload := `{"ref": "refs/heads/main", "repository": {"full_name": "user/repo", "clone_url": "https://github.com/user/repo.git"}}`
	req := httptest.NewRequest("POST", "/webhook/github", strings.NewReader(payload))
	req.Header.Set("X-GitHub-Event", "push")
	rr := httptest.NewRecorder()

	w.handleGitHubWebhook(req.Context(), rr, req)

	// Should return OK (sync errors are logged but don't fail the webhook)
	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusOK)
	}
}

func TestHandleGitHubWebhook_InvalidSignature(t *testing.T) {
	w := &WebhookServer{
		WebhookSecret: "mysecret",
		Syncer:        &Syncer{},
	}

	payload := `{"ref": "refs/heads/main", "repository": {"full_name": "user/repo", "clone_url": "https://example.com/repo.git"}}`
	req := httptest.NewRequest("POST", "/webhook/github", strings.NewReader(payload))
	req.Header.Set("X-GitHub-Event", "push")
	req.Header.Set("X-Hub-Signature-256", "sha256=invalid")
	rr := httptest.NewRecorder()

	w.handleGitHubWebhook(req.Context(), rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d (Unauthorized)", rr.Code, http.StatusUnauthorized)
	}
}

func TestSyncByRepo(t *testing.T) {
	tmpDir := t.TempDir()

	fakeClient := &fakeDynamicClientWithSites{
		sites: []siteSpec{
			{name: "site1", namespace: "default", repo: "https://github.com/user/repo.git", branch: "main"},
			{name: "site2", namespace: "default", repo: "https://github.com/user/other.git", branch: "main"},
		},
	}

	w := &WebhookServer{
		Syncer: &Syncer{
			SitesRoot:     tmpDir,
			AllowedHosts:  []string{"github.com"},
			DynamicClient: fakeClient,
			ClientSet:     newFakeClientset(),
		},
	}

	ctx := context.Background()
	err := w.syncByRepo(ctx, "https://github.com/user/repo.git", "main")

	// syncByRepo should not return an error even if individual syncs fail
	if err != nil {
		t.Errorf("syncByRepo() error = %v, want nil", err)
	}
}

func TestSyncByRepo_NoMatchingSites(t *testing.T) {
	tmpDir := t.TempDir()

	fakeClient := &fakeDynamicClientWithSites{
		sites: []siteSpec{
			{name: "site1", namespace: "default", repo: "https://github.com/user/other.git", branch: "main"},
		},
	}

	w := &WebhookServer{
		Syncer: &Syncer{
			SitesRoot:     tmpDir,
			AllowedHosts:  []string{"github.com"},
			DynamicClient: fakeClient,
			ClientSet:     newFakeClientset(),
		},
	}

	ctx := context.Background()
	err := w.syncByRepo(ctx, "https://github.com/user/nomatch.git", "main")

	if err != nil {
		t.Errorf("syncByRepo() error = %v, want nil", err)
	}
}

func TestHandleGitHubWebhook_InvalidPayload(t *testing.T) {
	w := &WebhookServer{
		Syncer: &Syncer{},
	}

	req := httptest.NewRequest("POST", "/webhook/github", strings.NewReader("invalid json"))
	req.Header.Set("X-GitHub-Event", "push")
	rr := httptest.NewRecorder()

	w.handleGitHubWebhook(req.Context(), rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d (Bad Request)", rr.Code, http.StatusBadRequest)
	}
}

func TestHandleForgejoWebhook_ValidSignature(t *testing.T) {
	tmpDir := t.TempDir()

	fakeClient := &fakeDynamicClientWithSites{
		sites: []siteSpec{},
	}

	secret := "mysecret"
	payload := `{"ref": "refs/heads/main", "repository": {"full_name": "user/repo", "clone_url": "https://example.com/repo.git"}}`
	signature := computeHMAC(payload, secret)

	w := &WebhookServer{
		WebhookSecret: secret,
		Syncer: &Syncer{
			SitesRoot:     tmpDir,
			AllowedHosts:  []string{"example.com"},
			DynamicClient: fakeClient,
			ClientSet:     newFakeClientset(),
		},
	}

	req := httptest.NewRequest("POST", "/webhook/forgejo", strings.NewReader(payload))
	req.Header.Set("X-Gitea-Signature", signature)
	rr := httptest.NewRecorder()

	w.handleForgejoWebhook(req.Context(), rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusOK)
	}
}

func TestHandleGitHubWebhook_ValidSignature(t *testing.T) {
	tmpDir := t.TempDir()

	fakeClient := &fakeDynamicClientWithSites{
		sites: []siteSpec{},
	}

	secret := "mysecret"
	payload := `{"ref": "refs/heads/main", "repository": {"full_name": "user/repo", "clone_url": "https://example.com/repo.git"}}`
	signature := "sha256=" + computeHMAC(payload, secret)

	w := &WebhookServer{
		WebhookSecret: secret,
		Syncer: &Syncer{
			SitesRoot:     tmpDir,
			AllowedHosts:  []string{"example.com"},
			DynamicClient: fakeClient,
			ClientSet:     newFakeClientset(),
		},
	}

	req := httptest.NewRequest("POST", "/webhook/github", strings.NewReader(payload))
	req.Header.Set("X-GitHub-Event", "push")
	req.Header.Set("X-Hub-Signature-256", signature)
	rr := httptest.NewRecorder()

	w.handleGitHubWebhook(req.Context(), rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusOK)
	}
}

func TestWebhookEndpointRouting(t *testing.T) {
	tmpDir := t.TempDir()

	fakeClient := &fakeDynamicClientWithSites{
		sites: []siteSpec{},
	}

	w := &WebhookServer{
		Syncer: &Syncer{
			SitesRoot:     tmpDir,
			AllowedHosts:  []string{"github.com"},
			DynamicClient: fakeClient,
			ClientSet:     newFakeClientset(),
		},
	}

	// Test Forgejo endpoint via ServeHTTP
	payload := `{"ref": "refs/heads/main", "repository": {"full_name": "user/repo", "clone_url": "https://github.com/user/repo.git"}}`
	req := httptest.NewRequest("POST", "/webhook/forgejo", strings.NewReader(payload))
	rr := httptest.NewRecorder()

	w.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("forgejo webhook: status = %d, want %d", rr.Code, http.StatusOK)
	}

	// Test GitHub endpoint via ServeHTTP
	req = httptest.NewRequest("POST", "/webhook/github", strings.NewReader(payload))
	req.Header.Set("X-GitHub-Event", "push")
	rr = httptest.NewRecorder()

	w.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("github webhook: status = %d, want %d", rr.Code, http.StatusOK)
	}
}

func TestSyncByRepo_ListError(t *testing.T) {
	fakeClient := &fakeDynamicClientWithListError{}

	w := &WebhookServer{
		Syncer: &Syncer{
			AllowedHosts:  []string{"github.com"},
			DynamicClient: fakeClient,
		},
	}

	ctx := context.Background()
	err := w.syncByRepo(ctx, "https://github.com/user/repo.git", "main")

	if err == nil {
		t.Error("syncByRepo() expected error for list failure, got nil")
	}
}
