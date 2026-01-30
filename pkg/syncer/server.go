// Package syncer - HTTP Server for Webhooks
package syncer

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// WebhookServer provides HTTP endpoints for webhooks
type WebhookServer struct {
	Syncer *Syncer

	// Optional: Webhook secret for validation
	WebhookSecret string
}

// ServeHTTP implements http.Handler
func (w *WebhookServer) ServeHTTP(rw http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	_ = log.FromContext(ctx) // Logger for later use

	// Routing
	path := strings.TrimPrefix(r.URL.Path, "/")
	parts := strings.Split(path, "/")

	switch {
	case r.Method == "GET" && path == "health":
		// Health check
		rw.WriteHeader(http.StatusOK)
		fmt.Fprint(rw, "ok")

	case r.Method == "POST" && len(parts) == 3 && parts[0] == "sync":
		// POST /sync/{namespace}/{name}
		namespace := parts[1]
		name := parts[2]
		w.handleSync(ctx, rw, r, namespace, name)

	case r.Method == "POST" && path == "webhook/forgejo":
		// Forgejo/Gitea Webhook
		w.handleForgejoWebhook(ctx, rw, r)

	case r.Method == "POST" && path == "webhook/github":
		// GitHub Webhook
		w.handleGitHubWebhook(ctx, rw, r)

	case r.Method == "DELETE" && len(parts) == 2 && parts[0] == "site":
		// DELETE /site/{name} - Deletes site directories
		name := parts[1]
		w.handleDelete(ctx, rw, name)

	default:
		http.NotFound(rw, r)
	}
}

// handleSync triggers a sync for a specific site
func (w *WebhookServer) handleSync(ctx context.Context, rw http.ResponseWriter, r *http.Request, namespace, name string) {
	logger := log.FromContext(ctx)
	logger.Info("Webhook triggered", "namespace", namespace, "name", name)

	if err := w.Syncer.SyncOne(ctx, namespace, name); err != nil {
		logger.Error(err, "Sync failed", "namespace", namespace, "name", name)
		http.Error(rw, err.Error(), http.StatusInternalServerError)
		return
	}

	rw.WriteHeader(http.StatusOK)
	fmt.Fprintf(rw, "Synced %s/%s", namespace, name)
}

// handleDelete deletes the files of a site
func (w *WebhookServer) handleDelete(ctx context.Context, rw http.ResponseWriter, name string) {
	logger := log.FromContext(ctx)
	logger.Info("Delete triggered", "name", name)

	if err := w.Syncer.DeleteSite(ctx, name); err != nil {
		logger.Error(err, "Delete failed", "name", name)
		http.Error(rw, err.Error(), http.StatusInternalServerError)
		return
	}

	rw.WriteHeader(http.StatusOK)
	fmt.Fprintf(rw, "Deleted %s", name)
}

// validateWebhookSignature validates the HMAC-SHA256 signature of a webhook
func (w *WebhookServer) validateWebhookSignature(body []byte, signature, prefix string) bool {
	if w.WebhookSecret == "" {
		return true // No validation if no secret configured
	}

	// Parse signature header (e.g. "sha256=abc123...")
	if !strings.HasPrefix(signature, prefix) {
		return false
	}
	sigHex := strings.TrimPrefix(signature, prefix)

	// Calculate expected signature
	mac := hmac.New(sha256.New, []byte(w.WebhookSecret))
	mac.Write(body)
	expectedSig := hex.EncodeToString(mac.Sum(nil))

	return hmac.Equal([]byte(sigHex), []byte(expectedSig))
}

// ForgejoWebhookPayload is the webhook payload from Forgejo/Gitea
type ForgejoWebhookPayload struct {
	Ref        string `json:"ref"`
	Repository struct {
		FullName string `json:"full_name"`
		CloneURL string `json:"clone_url"`
	} `json:"repository"`
}

// handleForgejoWebhook processes Forgejo/Gitea webhooks
func (w *WebhookServer) handleForgejoWebhook(ctx context.Context, rw http.ResponseWriter, r *http.Request) {
	logger := log.FromContext(ctx)

	// Read body for signature validation
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(rw, "failed to read body", http.StatusBadRequest)
		return
	}

	// Validate signature (Forgejo/Gitea: X-Gitea-Signature or X-Hub-Signature-256)
	signature := r.Header.Get("X-Gitea-Signature")
	if signature == "" {
		signature = r.Header.Get("X-Hub-Signature-256")
	}
	if !w.validateWebhookSignature(body, signature, "sha256=") && !w.validateWebhookSignature(body, signature, "") {
		if w.WebhookSecret != "" {
			logger.Info("Invalid webhook signature")
			http.Error(rw, "invalid signature", http.StatusUnauthorized)
			return
		}
	}

	var payload ForgejoWebhookPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		http.Error(rw, "invalid payload", http.StatusBadRequest)
		return
	}

	logger.Info("Forgejo webhook received",
		"repo", payload.Repository.FullName,
		"ref", payload.Ref,
	)

	// Extract branch from ref (refs/heads/main -> main)
	branch := strings.TrimPrefix(payload.Ref, "refs/heads/")

	// Find and sync all sites with this repo URL
	// This is somewhat inefficient but simple
	// Alternative: Annotation on the site with webhook ID
	if err := w.syncByRepo(ctx, payload.Repository.CloneURL, branch); err != nil {
		logger.Error(err, "Webhook sync failed")
		http.Error(rw, err.Error(), http.StatusInternalServerError)
		return
	}

	rw.WriteHeader(http.StatusOK)
	fmt.Fprint(rw, "ok")
}

// GitHubWebhookPayload is the webhook payload from GitHub
type GitHubWebhookPayload struct {
	Ref        string `json:"ref"`
	Repository struct {
		FullName string `json:"full_name"`
		CloneURL string `json:"clone_url"`
	} `json:"repository"`
}

// handleGitHubWebhook processes GitHub webhooks
func (w *WebhookServer) handleGitHubWebhook(ctx context.Context, rw http.ResponseWriter, r *http.Request) {
	logger := log.FromContext(ctx)

	// GitHub sends event type in header
	eventType := r.Header.Get("X-GitHub-Event")
	if eventType != "push" {
		rw.WriteHeader(http.StatusOK)
		fmt.Fprintf(rw, "ignored event: %s", eventType)
		return
	}

	// Read body for signature validation
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(rw, "failed to read body", http.StatusBadRequest)
		return
	}

	// Validate signature (GitHub: X-Hub-Signature-256)
	signature := r.Header.Get("X-Hub-Signature-256")
	if !w.validateWebhookSignature(body, signature, "sha256=") {
		if w.WebhookSecret != "" {
			logger.Info("Invalid webhook signature")
			http.Error(rw, "invalid signature", http.StatusUnauthorized)
			return
		}
	}

	var payload GitHubWebhookPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		http.Error(rw, "invalid payload", http.StatusBadRequest)
		return
	}

	logger.Info("GitHub webhook received",
		"repo", payload.Repository.FullName,
		"ref", payload.Ref,
	)

	branch := strings.TrimPrefix(payload.Ref, "refs/heads/")

	if err := w.syncByRepo(ctx, payload.Repository.CloneURL, branch); err != nil {
		logger.Error(err, "Webhook sync failed")
		http.Error(rw, err.Error(), http.StatusInternalServerError)
		return
	}

	rw.WriteHeader(http.StatusOK)
	fmt.Fprint(rw, "ok")
}

// syncByRepo finds all sites with a repo URL and syncs them
func (w *WebhookServer) syncByRepo(ctx context.Context, repoURL, branch string) error {
	logger := log.FromContext(ctx)

	// Load all StaticSites
	list, err := w.Syncer.DynamicClient.Resource(staticSiteGVR).Namespace("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}

	synced := 0
	for _, item := range list.Items {
		site := &staticSiteData{}
		if err := site.fromUnstructured(&item); err != nil {
			continue
		}

		// Check if repo and branch match
		if site.Repo == repoURL && site.Branch == branch {
			logger.Info("Syncing site from webhook", "name", site.Name)
			if err := w.Syncer.syncSite(ctx, site); err != nil {
				logger.Error(err, "Failed to sync", "name", site.Name)
			} else {
				synced++
			}
		}
	}

	logger.Info("Webhook sync complete", "synced", synced)
	return nil
}

// Start starts the HTTP server
func (w *WebhookServer) Start(ctx context.Context, addr string) error {
	logger := log.FromContext(ctx)
	
	server := &http.Server{
		Addr:    addr,
		Handler: w,
	}

	go func() {
		<-ctx.Done()
		server.Shutdown(context.Background())
	}()

	logger.Info("Starting webhook server", "addr", addr)
	return server.ListenAndServe()
}
