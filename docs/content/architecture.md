---
title: Architecture
weight: 50
---

# Architecture

Technical overview of the kup6s-pages architecture.

## Design Goals

1. **Resource efficiency**: One nginx Pod serves all sites
2. **Kubernetes-native**: Integration with Traefik IngressController and cert-manager
3. **Declarative**: Sites are defined as Custom Resources
4. **Git-based**: Automatic synchronization from Git repositories
5. **Simple**: Minimal configuration for the end user

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

## Resource Efficiency

| Approach | 100 Sites | 1000 Sites |
|----------|-----------|------------|
| Pod per Site | 100 Pods | 1000 Pods |
| kup6s-pages | 3 Pods | 3 Pods |

The three Pods are: Operator (1), Syncer (1), nginx (1-2 for HA).

## Security Model

- **Operator**: ClusterRole for watching StaticSites, but only creates resources in the system namespace
- **Syncer**: No cluster-wide secret access by default (opt-in via namespace Roles)
- **SSRF Protection**: Mandatory `--allowed-hosts` flag restricts which Git hosts can be accessed
- **Pod Security**: All pods run as non-root with read-only root filesystem

See [SECURITY.md](https://github.com/kup6s/pages/blob/main/docs/SECURITY.md) for detailed RBAC documentation.

## Limitations

1. **RWX Storage required**: The PVC must support ReadWriteMany
2. **No Build Pipeline**: Only serves static files (build in CI/CD)
3. **No Preview Deployments**: Each StaticSite is a fixed configuration
4. **Single Point of Sync**: The Syncer is a single Pod
