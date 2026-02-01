# kup6s-pages: Concept

A Kubernetes-native service for static website hosting, inspired by GitHub Pages.

## Problem Statement

Existing solutions for static website hosting on Kubernetes are either inefficient or difficult to integrate:

**Kubero** and similar PaaS solutions start one Pod per website. With many small static sites, this leads to significant resource overhead.

**Codeberg pages-server** is efficient (one container for all sites) but handles TLS on its own. This conflicts with the standard Kubernetes pattern (Ingress Controller + cert-manager) and requires SSL passthrough, which prevents Layer-7 features like path routing.

**git-sync as sidecar** only synchronizes one repository per container. For many sites, you'd need many sidecars.

## Design Goals

1. **Resource efficiency**: One nginx Pod serves all sites
2. **Kubernetes-native**: Integration with Traefik IngressController and cert-manager
3. **Declarative**: Sites are defined as Custom Resources
4. **Git-based**: Automatic synchronization from Git repositories
5. **Custom Domains**: Each site can have its own domain
6. **Simple**: Minimal configuration for the end user

## Architecture

### Overview

```
┌─────────────────────────────────────────────────────────────────────────┐
│                                                                         │
│  ┌──────────────┐      ┌──────────────┐      ┌──────────────┐          │
│  │ StaticSite   │      │ StaticSite   │      │ StaticSite   │   ...    │
│  │ CRD          │      │ CRD          │      │ CRD          │          │
│  └──────┬───────┘      └──────┬───────┘      └──────┬───────┘          │
│         │                     │                     │                   │
│         └─────────────────────┼─────────────────────┘                   │
│                               │                                         │
│                               ▼                                         │
│                      ┌────────────────┐                                 │
│                      │    Operator    │                                 │
│                      │                │                                 │
│                      │ • Watches CRDs │                                 │
│                      │ • Creates:     │                                 │
│                      │   - Ingress    │                                 │
│                      │   - Middleware │                                 │
│                      │   - Certific.  │                                 │
│                      └────────────────┘                                 │
│                                                                         │
│         ┌──────────────────────┼──────────────────────┐                 │
│         │                      │                      │                 │
│         ▼                      ▼                      ▼                 │
│  ┌────────────┐        ┌─────────────┐        ┌─────────────┐          │
│  │ Traefik    │        │ Traefik     │        │ cert-manager│          │
│  │ Ingress-   │        │ Middleware  │        │ Certificate │          │
│  │ Route      │        │ (addPrefix) │        │             │          │
│  └────────────┘        └─────────────┘        └─────────────┘          │
│                                                                         │
└─────────────────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────────────────┐
│                                                                         │
│                        ┌────────────────┐                               │
│                        │     Syncer     │                               │
│                        │                │                               │
│                        │ • Reads CRDs   │                               │
│                        │ • Git clone/   │                               │
│                        │   pull         │                               │
│                        │ • Webhook API  │                               │
│                        └───────┬────────┘                               │
│                                │                                        │
│                                ▼                                        │
│                     ┌─────────────────────┐                             │
│                     │   PVC: /sites       │                             │
│                     │   ├── site-a/       │                             │
│                     │   ├── site-b/       │                             │
│                     │   └── site-c/       │                             │
│                     └──────────┬──────────┘                             │
│                                │                                        │
│                                ▼                                        │
│                     ┌─────────────────────┐                             │
│                     │   nginx (1 Pod)     │                             │
│                     │   root /sites;      │                             │
│                     └─────────────────────┘                             │
│                                                                         │
└─────────────────────────────────────────────────────────────────────────┘
```

### Components

#### 1. StaticSite CRD

The Custom Resource Definition is the central configuration element:

```yaml
apiVersion: pages.kup6s.com/v1alpha1
kind: StaticSite
metadata:
  name: customer-website      # Becomes the path /customer-website
  namespace: pages
spec:
  repo: https://forgejo.kup6s.io/customer/website.git
  branch: main             # Optional, default: main
  path: /dist              # Optional, default: / (repo root)
  domain: www.customer.com # Optional, otherwise: <name>.pages.kup6s.com
  secretRef:               # Optional, for private repos
    name: git-credentials
    key: password
  syncInterval: 5m         # Optional, default: 5m
```

The `metadata.name` is central: It defines the path under which the site is located in nginx (`/sites/<name>/`).

#### 2. Operator

The Operator watches StaticSite resources and creates for each:

**Traefik Middleware (addPrefix)**
```yaml
apiVersion: traefik.io/v1alpha1
kind: Middleware
metadata:
  name: customer-website-prefix
spec:
  addPrefix:
    prefix: /customer-website
```

**Traefik IngressRoute**
```yaml
apiVersion: traefik.io/v1alpha1
kind: IngressRoute
metadata:
  name: customer-website
spec:
  entryPoints: [websecure]
  routes:
    - match: Host(`www.customer.com`)
      middlewares:
        - name: customer-website-prefix
      services:
        - name: pages-nginx-proxy  # ExternalName service pointing to nginx
          namespace: pages         # Same namespace as IngressRoute
          port: 80
  tls:
    secretName: customer-website-tls
```

Note: The IngressRoute references a local `pages-nginx-proxy` ExternalName service (created by the operator) instead of the nginx service directly. This is required because Traefik doesn't allow cross-namespace service references by default.

**cert-manager Certificate** (only for custom domains)
```yaml
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: customer-website-tls
spec:
  secretName: customer-website-tls
  issuerRef:
    name: letsencrypt-prod
    kind: ClusterIssuer
  dnsNames:
    - www.customer.com
```

The Operator sets Owner References so that all created resources are automatically deleted when the StaticSite is deleted.

#### 3. Syncer

A central service that synchronizes all StaticSites:

- Runs as a Deployment with access to the PVC
- Periodically polls all StaticSite CRDs (default: every 5 minutes)
- Clones new repos, pulls existing ones
- Supports private repos via Secrets
- Provides HTTP API for webhooks (instant sync on push)

**Webhook Endpoints:**
- `POST /sync/{namespace}/{name}` - Sync a specific site
- `POST /webhook/forgejo` - Forgejo/Gitea push webhook
- `POST /webhook/github` - GitHub push webhook
- `GET /health` - Health check

#### 4. nginx

A single nginx Deployment serves all sites:

```nginx
server {
    listen 80;
    root /sites;

    location / {
        try_files $uri $uri/ $uri/index.html =404;
    }
}
```

The configuration is static and never needs to be adjusted. Routing is handled by Traefik via addPrefix.

#### 5. Shared PVC

A PersistentVolumeClaim with ReadWriteMany (RWX):
- Syncer writes: `/sites/<name>/`
- nginx reads: `/sites/<name>/`

## Request Flow

```
HTTPS Request: www.customer.com/about.html
         │
         │ 1. TLS Termination (Traefik)
         ▼
┌─────────────────────────────────────────────────────────┐
│  Traefik                                                │
│                                                         │
│  Route Match: Host(`www.customer.com`)                  │
│  Middleware:  addPrefix(/customer-website)              │
│                                                         │
│  Internal Request: /customer-website/about.html         │
└────────────────────────┬────────────────────────────────┘
                         │
                         │ 2. HTTP to nginx Service
                         ▼
┌─────────────────────────────────────────────────────────┐
│  nginx                                                  │
│                                                         │
│  root /sites;                                           │
│  Request: /customer-website/about.html                  │
│  Served:  /sites/customer-website/about.html            │
└────────────────────────┬────────────────────────────────┘
                         │
                         │ 3. File from PVC
                         ▼
┌─────────────────────────────────────────────────────────┐
│  PVC: /sites                                            │
│                                                         │
│  /sites/customer-website/                               │
│  ├── index.html                                         │
│  ├── about.html  ◄── This file                          │
│  └── assets/                                            │
└─────────────────────────────────────────────────────────┘
```

## Sync Flow

### Periodic Sync

```
┌──────────────┐     ┌─────────────────────────────────────┐
│   Syncer     │     │  Kubernetes API                     │
│              │     │                                     │
│  Timer: 5m   │────▶│  GET /apis/pages.kup6s.com/v1alpha1/ │
│              │     │      staticsites                    │
└──────┬───────┘     └─────────────────────────────────────┘
       │
       │ For each StaticSite:
       ▼
┌──────────────────────────────────────────────────────────┐
│                                                          │
│  if /sites/<name>/.git exists:                           │
│      git pull                                            │
│  else:                                                   │
│      git clone --depth=1 <repo> /sites/<name>            │
│                                                          │
│  Status Update: lastSync, lastCommit                     │
│                                                          │
└──────────────────────────────────────────────────────────┘
```

### Webhook Sync (Instant)

```
┌──────────────┐     ┌──────────────┐     ┌──────────────┐
│   Forgejo    │     │   Traefik    │     │   Syncer     │
│              │     │              │     │              │
│  git push    │────▶│  webhook.    │────▶│  POST        │
│              │     │  pages.      │     │  /webhook/   │
│              │     │  kup6s.io    │     │  forgejo     │
└──────────────┘     └──────────────┘     └──────┬───────┘
                                                 │
       ┌─────────────────────────────────────────┘
       │
       ▼
┌──────────────────────────────────────────────────────────┐
│                                                          │
│  1. Parse Webhook Payload (repo URL, branch)             │
│  2. Find all StaticSites with this repo URL              │
│  3. git pull for each matching site                      │
│  4. Status Update                                        │
│                                                          │
└──────────────────────────────────────────────────────────┘
```

## Benefits of This Design

### Resource Efficiency

| Approach | 100 Sites | 1000 Sites |
|----------|-----------|------------|
| Pod per Site | 100 Pods | 1000 Pods |
| kup6s-pages | 3 Pods | 3 Pods |

The three Pods are: Operator (1), Syncer (1), nginx (1-2 for HA).

### No Dynamic nginx Configuration

The addPrefix pattern eliminates the need to reconfigure nginx for each new site:

- No ConfigMap updates
- No nginx reload
- No race conditions

### Kubernetes-native Integration

- **Traefik**: Standard IngressController, full feature support
- **cert-manager**: Automatic TLS certificates, also for custom domains
- **RBAC**: Fine-grained permissions
- **Owner References**: Automatic cleanup

### Easy Extensibility

Future features can be added easily:

- **Basic Auth**: Additional Traefik middleware
- **Rate Limiting**: Traefik middleware
- **Custom Headers**: Traefik middleware
- **Redirects**: Traefik middleware

## Deployment Overview

```
Namespace: kup6s-pages (System)
├── Deployment: pages-operator
│   └── Pod: operator
├── Deployment: pages-syncer
│   └── Pod: syncer
├── Deployment: static-sites-nginx
│   └── Pod: nginx (replicas: 2)
├── Service: static-sites-nginx
├── Service: pages-syncer (for webhooks)
├── PVC: static-sites-data
├── ConfigMap: nginx-config
└── ServiceAccounts + RBAC

Namespace: pages (User Sites)
├── StaticSite: customer-a-website
├── StaticSite: customer-b-docs
├── Secret: git-credentials (optional)
├── IngressRoute: customer-a-website (generated)
├── IngressRoute: customer-b-docs (generated)
├── Middleware: customer-a-website-prefix (generated)
├── Middleware: customer-b-docs-prefix (generated)
├── Certificate: customer-a-website-tls (generated)
└── Certificate: customer-b-docs-tls (generated)
```

## Limitations

1. **RWX Storage required**: The PVC must support ReadWriteMany (e.g., Longhorn, NFS, CephFS)

2. **No Build Pipeline**: kup6s-pages only serves static files. Build steps (e.g., npm build) must be done beforehand in CI/CD

3. **No Preview Deployments**: Each StaticSite is a fixed configuration, no automatic branch previews

4. **Single Point of Sync**: The Syncer is a single Pod. If it fails, updates are delayed (but serving continues)

## Future Extensions

- **Preview Deployments**: Automatic sites for Pull Requests
- **Build Integration**: Optional build container before sync
- **Metrics**: Prometheus metrics for sync status and errors
- **UI**: Web dashboard for site management
- **Multi-Cluster**: Sync to multiple clusters

## Conclusion

kup6s-pages provides a resource-efficient, Kubernetes-native solution for static website hosting. Through the combination of CRD-based configuration, centralized Git sync, and the addPrefix pattern, complexity is minimized while ensuring full integration with the Kubernetes ecosystem.
