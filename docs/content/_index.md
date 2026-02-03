---
title: kup6s-pages
type: docs
---

# kup6s-pages

Cloud native multi-tenant static web-hosting for Kubernetes.

## Overview

kup6s-pages deploys static websites from Git repositories to Kubernetes. A single nginx pod serves all sites efficiently, with Traefik handling routing via `addPrefix` middleware. The operator automatically manages IngressRoutes and TLS certificates.

**Key Features:**

- Single nginx pod for all sites (no per-site overhead)
- CRD-based declarative configuration
- Automatic TLS via cert-manager
- Traefik IngressRoute integration
- Git-based deployments (Forgejo, GitLab, GitHub)
- Webhook support for instant updates on push
- Private repository support via deploy tokens
- Subpath support for build outputs (e.g., `/dist`)
- Path prefix support for multiple repos on same domain

## Quick Start

**1. Install via Helm**

```bash
helm install pages oci://ghcr.io/kup6s/kup6s-pages \
  --set operator.pagesDomain=pages.example.com \
  --set operator.clusterIssuer=letsencrypt-prod \
  --set 'syncer.allowedHosts={github.com}'
```

**2. Create a StaticSite**

```yaml
apiVersion: pages.kup6s.com/v1beta1
kind: StaticSite
metadata:
  name: my-website
  namespace: pages
spec:
  repo: https://github.com/user/my-website.git
  domain: www.example.com
```

**3. Check status**

```bash
kubectl get staticsites -n pages
```

Your site is live at `https://www.example.com` once the status shows `Ready`.

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
