# kup6s-pages

Cloud native multi-tenant static web-hosting.

## Overview

kup6s-pages deploys static websites from Git repositories to Kubernetes. A single nginx pod serves all sites efficiently, with Traefik handling routing via `addPrefix` middleware. The operator automatically manages IngressRoutes and TLS certificates - no manual ingress configuration needed.

**Key Features:**
- Single nginx pod for all sites (no per-site overhead)
- CRD-based declarative configuration
- Automatic TLS via cert-manager
- Traefik IngressRoute integration
- Git-based deployments (Forgejo, GitLab, GitHub)
- Webhook support for instant updates on push
- Private repository support via deploy tokens
- Subpath support for build outputs (e.g., `/dist`)

## Architecture

```
┌──────────────────────────────────────────────────────────────────────────┐
│                           Request Flow                                   │
├──────────────────────────────────────────────────────────────────────────┤
│                                                                          │
│   https://www.customer.com/about.html                                    │
│            │                                                             │
│            ▼                                                             │
│   ┌─────────────────┐                                                    │
│   │     Traefik     │  Host(`www.customer.com`) matched                  │
│   │                 │  Middleware: addPrefix(/customer-website)          │
│   └────────┬────────┘                                                    │
│            │  /customer-website/about.html                               │
│            ▼                                                             │
│   ┌─────────────────┐                                                    │
│   │  nginx (1 Pod)  │  root /sites;                                      │
│   │                 │  serves /sites/customer-website/about.html         │
│   └────────┬────────┘                                                    │
│            │                                                             │
│            ▼                                                             │
│   ┌─────────────────────────────────────┐                                │
│   │  PVC: /sites                        │                                │
│   │  ├── customer-website/ ← from repo  │                                │
│   │  ├── user-blog/                     │                                │
│   └─────────────────────────────────────┘                                │
│                                                                          │
└──────────────────────────────────────────────────────────────────────────┘
```

### Components

| Component | Description |
|-----------|-------------|
| **Operator** | Watches StaticSite CRDs, creates IngressRoutes, Middlewares, and Certificates |
| **Syncer** | Clones/pulls Git repos to shared PVC, handles webhooks |
| **nginx** | Serves static files from the shared PVC |

## Prerequisites

- Kubernetes cluster with:
  - [Traefik](https://traefik.io/) as Ingress Controller
  - [cert-manager](https://cert-manager.io/) for TLS certificates
  - A RWX-capable StorageClass (e.g., Longhorn, NFS)
- A ClusterIssuer configured (e.g., `letsencrypt-prod`)

## Installation

```bash
# Deploy CRD, RBAC, Operator, Syncer, and nginx
kubectl apply -f deploy/crd.yaml
kubectl apply -f deploy/rbac.yaml
kubectl apply -f deploy/operator.yaml
kubectl apply -f deploy/nginx.yaml

# Optional: Deploy example namespace and sites
kubectl apply -f deploy/examples.yaml
```

### Verify Installation

```bash
# Check operator and syncer are running
kubectl get pods -n kup6s-pages

# Check CRD is registered
kubectl get crd staticsites.pages.kup6s.io
```

## Usage

### Basic Site (Public Repository)

```yaml
apiVersion: pages.kup6s.io/v1alpha1
kind: StaticSite
metadata:
  name: my-website
  namespace: pages
spec:
  repo: https://github.com/user/my-website.git
  domain: www.example.com
```

The operator will:
1. Create a Traefik Middleware (`my-website-prefix`) with `addPrefix: /my-website`
2. Create a Traefik IngressRoute for `Host(\`www.example.com\`)`
3. Create a cert-manager Certificate for the domain
4. The Syncer clones the repo to `/sites/my-website/`

### Site with Build Output Subpath

For sites with build tools (Vite, Hugo, Sphinx, etc.) where the output is in a subdirectory:

```yaml
apiVersion: pages.kup6s.io/v1alpha1
kind: StaticSite
metadata:
  name: docs
  namespace: pages
spec:
  repo: https://github.com/user/docs.git
  branch: main
  path: /dist          # Serve only the /dist directory
  domain: docs.example.com
```

The Syncer clones to `/sites/.repos/docs/` and creates a symlink `/sites/docs/` → `/sites/.repos/docs/dist/`.

### Private Repository with Deploy Token

1. Create a Secret with your deploy token:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: my-repo-token
  namespace: pages
type: Opaque
stringData:
  password: "glpat-xxxxxxxxxxxx"  # Your Forgejo/GitLab/GitHub token
```

2. Reference the Secret in your StaticSite:

```yaml
apiVersion: pages.kup6s.io/v1alpha1
kind: StaticSite
metadata:
  name: internal-docs
  namespace: pages
spec:
  repo: https://forgejo.example.com/org/private-repo.git
  domain: docs.internal.example.com
  secretRef:
    name: my-repo-token
    key: password           # Optional, defaults to "password"
```

The Secret must be in the same namespace as the StaticSite.

### Auto-Generated Domain

If no `domain` is specified, the site gets a subdomain of the configured pages domain:

```yaml
apiVersion: pages.kup6s.io/v1alpha1
kind: StaticSite
metadata:
  name: my-project
  namespace: pages
spec:
  repo: https://github.com/user/my-project.git
  # No domain specified → https://my-project.pages.kup6s.io
```

## CRD Reference

### StaticSite Spec

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `repo` | string | Yes | - | Git repository URL (HTTPS) |
| `branch` | string | No | `main` | Git branch to track |
| `path` | string | No | `/` | Subpath in repo to serve |
| `domain` | string | No | `<name>.<pages-domain>` | Custom domain |
| `secretRef.name` | string | No | - | Secret name with Git credentials |
| `secretRef.key` | string | No | `password` | Key in Secret for the token |
| `syncInterval` | string | No | `5m` | How often to pull updates |

### StaticSite Status

| Field | Description |
|-------|-------------|
| `phase` | `Pending`, `Syncing`, `Ready`, or `Error` |
| `message` | Human-readable status message |
| `lastSync` | Timestamp of last successful sync |
| `lastCommit` | Short SHA of the last synced commit |
| `url` | Full URL of the deployed site |

### Check Status

```bash
# List all sites
kubectl get staticsites -A

# Detailed status
kubectl describe staticsite my-website -n pages

# Short form
kubectl get ss -n pages
```

## Webhooks

For instant deployments on push, configure webhooks in your Git provider.

### Webhook Endpoints

| Provider | URL |
|----------|-----|
| Forgejo/Gitea | `https://webhook.pages.kup6s.io/webhook/forgejo` |
| GitHub | `https://webhook.pages.kup6s.io/webhook/github` |
| Manual trigger | `POST https://webhook.pages.kup6s.io/sync/{namespace}/{name}` |

### Setup Webhook Ingress

Deploy the webhook IngressRoute (see `deploy/examples.yaml`):

```yaml
apiVersion: traefik.io/v1alpha1
kind: IngressRoute
metadata:
  name: pages-webhook
  namespace: kup6s-pages
spec:
  entryPoints:
    - websecure
  routes:
    - match: Host(`webhook.pages.kup6s.io`)
      kind: Rule
      services:
        - name: pages-syncer
          port: 80
  tls:
    secretName: pages-webhook-tls
```

### Configure in Forgejo/Gitea

1. Go to Repository → Settings → Webhooks → Add Webhook
2. URL: `https://webhook.pages.kup6s.io/webhook/forgejo`
3. Content Type: `application/json`
4. Secret: (optional, for signature validation)
5. Events: Push events

### Configure in GitHub

1. Go to Repository → Settings → Webhooks → Add webhook
2. Payload URL: `https://webhook.pages.kup6s.io/webhook/github`
3. Content type: `application/json`
4. Secret: (optional, for signature validation)
5. Events: Just the push event

## Operator Configuration

The operator accepts these flags:

| Flag | Default | Description |
|------|---------|-------------|
| `--pages-domain` | `pages.kup6s.io` | Base domain for auto-generated subdomains |
| `--cluster-issuer` | `letsencrypt-prod` | cert-manager ClusterIssuer name |
| `--metrics-bind-address` | `:8080` | Metrics endpoint |
| `--health-probe-bind-address` | `:8081` | Health probe endpoint |

The syncer accepts these flags:

| Flag | Default | Description |
|------|---------|-------------|
| `--sites-root` | `/sites` | Directory where sites are stored |
| `--sync-interval` | `5m` | Default interval for polling repos |
| `--webhook-addr` | `:8080` | Webhook HTTP server address |
| `--allowed-hosts` | (none) | Comma-separated allowlist of Git hosts (SSRF protection) |

## Troubleshooting

### Site stuck in "Pending"

Check the Syncer logs:
```bash
kubectl logs -n kup6s-pages -l app=pages-syncer
```

### Certificate not ready

Check cert-manager:
```bash
kubectl get certificate -n pages
kubectl describe certificate my-website-tls -n pages
```

### 404 errors

1. Verify the site directory exists:
```bash
kubectl exec -n kup6s-pages deploy/pages-syncer -- ls -la /sites/
```

2. Check if the repo was cloned successfully:
```bash
kubectl get staticsite my-website -n pages -o yaml
```

### Private repo authentication fails

1. Verify the Secret exists and has the correct key:
```bash
kubectl get secret my-repo-token -n pages -o yaml
```

2. Test the token manually (replace with your values):
```bash
git clone https://git:YOUR_TOKEN@forgejo.example.com/org/repo.git
```

### Force re-sync

Trigger a manual sync via webhook:
```bash
curl -X POST https://webhook.pages.kup6s.io/sync/pages/my-website
```

## Development

### Project Structure

```
pages/
├── cmd/
│   ├── operator/      # Operator entrypoint
│   └── syncer/        # Syncer entrypoint
├── pkg/
│   ├── apis/v1alpha1/ # CRD types
│   ├── controller/    # Reconciliation logic
│   └── syncer/        # Git sync and webhook server
└── deploy/            # Kubernetes manifests
```

### Build

```bash
# Build operator
go build -o bin/operator ./cmd/operator

# Build syncer
go build -o bin/syncer ./cmd/syncer
```

### Run Tests

```bash
go test ./...
```

### Run Locally (for development)

```bash
# Operator (requires kubeconfig)
go run ./cmd/operator --pages-domain=pages.local --cluster-issuer=selfsigned

# Syncer
go run ./cmd/syncer --sites-root=/tmp/sites --sync-interval=1m
```

## License

EUPL-1.2
