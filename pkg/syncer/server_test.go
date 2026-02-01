package syncer

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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

	var p ForgejoWebhookPayload
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

	var p GitHubWebhookPayload
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
