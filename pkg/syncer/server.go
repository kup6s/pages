// Package syncer - HTTP Server für Webhooks
package syncer

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// WebhookServer stellt HTTP Endpoints für Webhooks bereit
type WebhookServer struct {
	Syncer *Syncer
	
	// Optional: Webhook Secret für Validierung
	WebhookSecret string
}

// ServeHTTP implementiert http.Handler
func (w *WebhookServer) ServeHTTP(rw http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	logger := log.FromContext(ctx)

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

	default:
		http.NotFound(rw, r)
	}
}

// handleSync triggered einen Sync für eine spezifische Site
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

// ForgejoWebhookPayload ist die Webhook Payload von Forgejo/Gitea
type ForgejoWebhookPayload struct {
	Ref        string `json:"ref"`
	Repository struct {
		FullName string `json:"full_name"`
		CloneURL string `json:"clone_url"`
	} `json:"repository"`
}

// handleForgejoWebhook verarbeitet Forgejo/Gitea Webhooks
func (w *WebhookServer) handleForgejoWebhook(ctx context.Context, rw http.ResponseWriter, r *http.Request) {
	logger := log.FromContext(ctx)

	var payload ForgejoWebhookPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(rw, "invalid payload", http.StatusBadRequest)
		return
	}

	logger.Info("Forgejo webhook received",
		"repo", payload.Repository.FullName,
		"ref", payload.Ref,
	)

	// Branch aus ref extrahieren (refs/heads/main -> main)
	branch := strings.TrimPrefix(payload.Ref, "refs/heads/")

	// Alle Sites mit dieser Repo URL finden und syncen
	// Das ist etwas ineffizient, aber einfach
	// Alternativ: Annotation an der Site mit Webhook-ID
	if err := w.syncByRepo(ctx, payload.Repository.CloneURL, branch); err != nil {
		logger.Error(err, "Webhook sync failed")
		http.Error(rw, err.Error(), http.StatusInternalServerError)
		return
	}

	rw.WriteHeader(http.StatusOK)
	fmt.Fprint(rw, "ok")
}

// GitHubWebhookPayload ist die Webhook Payload von GitHub
type GitHubWebhookPayload struct {
	Ref        string `json:"ref"`
	Repository struct {
		FullName string `json:"full_name"`
		CloneURL string `json:"clone_url"`
	} `json:"repository"`
}

// handleGitHubWebhook verarbeitet GitHub Webhooks
func (w *WebhookServer) handleGitHubWebhook(ctx context.Context, rw http.ResponseWriter, r *http.Request) {
	logger := log.FromContext(ctx)

	// GitHub sendet Event-Type im Header
	eventType := r.Header.Get("X-GitHub-Event")
	if eventType != "push" {
		rw.WriteHeader(http.StatusOK)
		fmt.Fprintf(rw, "ignored event: %s", eventType)
		return
	}

	var payload GitHubWebhookPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
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

// syncByRepo findet alle Sites mit einer Repo URL und synct sie
func (w *WebhookServer) syncByRepo(ctx context.Context, repoURL, branch string) error {
	logger := log.FromContext(ctx)

	// Alle StaticSites laden
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

		// Prüfen ob Repo und Branch matchen
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

// Start startet den HTTP Server
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
