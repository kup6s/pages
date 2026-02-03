---
title: Architecture
weight: 50
---

# Architecture

Technical overview of the kup6s-pages architecture.

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

## Components

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

### Operator

Watches StaticSite resources across all namespaces and creates:
- **Traefik IngressRoute** for routing traffic
- **Traefik Middleware** with `addPrefix` for path-based routing
- **cert-manager Certificate** for TLS

All generated resources are created in the system namespace (`kup6s-pages`) for improved security.

### Syncer

Synchronizes Git repositories to the shared PVC:
- Periodically polls all StaticSite CRDs
- Clones new repos, pulls existing ones
- Supports private repos via Secrets
- Provides HTTP API for webhooks

### nginx

A single nginx Deployment serves all sites with a static configuration:

```nginx
server {
    listen 80;
    root /sites;
    location / {
        try_files $uri $uri/ $uri/index.html =404;
    }
}
```

No dynamic configuration needed - routing is handled by Traefik via addPrefix.

## Request Flow

```
HTTPS Request: www.customer.com/about.html
         │
         │ 1. TLS Termination (Traefik)
         ▼
┌─────────────────────────────────────────────────────┐
│  Traefik                                            │
│                                                     │
│  Route Match: Host(`www.customer.com`)              │
│  Middleware:  addPrefix(/customer-website)          │
│                                                     │
│  Internal Request: /customer-website/about.html     │
└────────────────────────┬────────────────────────────┘
                         │
                         │ 2. HTTP to nginx Service
                         ▼
┌─────────────────────────────────────────────────────┐
│  nginx                                              │
│                                                     │
│  root /sites;                                       │
│  Request: /customer-website/about.html              │
│  Served:  /sites/customer-website/about.html        │
└────────────────────────┬────────────────────────────┘
                         │
                         │ 3. File from PVC
                         ▼
┌─────────────────────────────────────────────────────┐
│  PVC: /sites                                        │
│                                                     │
│  /sites/customer-website/                           │
│  ├── index.html                                     │
│  ├── about.html  ◄── This file                      │
│  └── assets/                                        │
└─────────────────────────────────────────────────────┘
```

## Sync Flow

### Periodic Sync

```
┌──────────────┐     ┌─────────────────────────────────────┐
│   Syncer     │     │  Kubernetes API                     │
│              │     │                                     │
│  Timer: 5m   │────▶│  GET /apis/pages.kup6s.com/v1beta1/ │
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

## Resource Efficiency

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

## Security Model

- **Operator**: ClusterRole for watching StaticSites, but only creates resources in the system namespace
- **Syncer**: No cluster-wide secret access by default (opt-in via namespace Roles)
- **SSRF Protection**: Mandatory `--allowed-hosts` flag restricts which Git hosts can be accessed
- **Pod Security**: All pods run as non-root with read-only root filesystem

See [Security]({{< relref "/security" >}}) for detailed RBAC documentation.

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
├── ServiceAccounts + RBAC
│
│ Generated resources (all in system namespace):
├── IngressRoute: pages--customer-a-website (generated)
├── IngressRoute: pages--customer-b-docs (generated)
├── Middleware: pages--customer-a-website-prefix (generated)
├── Middleware: pages--customer-b-docs-prefix (generated)
├── Certificate: www-customer-a-com-tls (generated)
└── Certificate: docs-customer-b-com-tls (generated)

Namespace: pages (User Sites)
├── StaticSite: customer-a-website
├── StaticSite: customer-b-docs
└── Secret: git-credentials (optional)
```

Note: All generated Traefik and cert-manager resources are created in the system
namespace for improved security. Users can see resource references via `status.resources`.

## Limitations

1. **RWX Storage required**: The PVC must support ReadWriteMany (e.g., Longhorn, NFS, CephFS)
2. **No Build Pipeline**: Only serves static files (build in CI/CD)
3. **No Preview Deployments**: Each StaticSite is a fixed configuration
4. **Single Point of Sync**: The Syncer is a single Pod

## Future Extensions

- **Preview Deployments**: Automatic sites for Pull Requests
- **Build Integration**: Optional build container before sync
- **Metrics**: Prometheus metrics for sync status and errors
- **UI**: Web dashboard for site management
- **Multi-Cluster**: Sync to multiple clusters
