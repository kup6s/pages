---
title: StaticSite CRD
weight: 10
---

# StaticSite CRD

The `StaticSite` Custom Resource Definition is the primary API for deploying static websites.

**API Version:** `pages.kup6s.com/v1beta1`
**Kind:** `StaticSite`

## Spec Fields

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `repo` | string | Yes | - | Git repository URL (HTTPS) |
| `branch` | string | No | `main` | Git branch to track |
| `path` | string | No | `/` | Subpath in repo to serve |
| `pathPrefix` | string | No | - | URL path prefix (requires domain) |
| `domain` | string | No | `<name>.<pages-domain>` | Custom domain |
| `secretRef.name` | string | No | - | Secret name with Git credentials |
| `secretRef.key` | string | No | `password` | Key in Secret for the token |
| `syncInterval` | string | No | `5m` | How often to pull updates |

## Status Fields

| Field | Type | Description |
|-------|------|-------------|
| `phase` | string | Current phase: `Pending`, `Syncing`, `Ready`, or `Error` |
| `message` | string | Human-readable status message |
| `lastSync` | timestamp | Timestamp of last successful sync |
| `lastCommit` | string | Short SHA of the last synced commit |
| `url` | string | Full URL of the deployed site |
| `syncToken` | string | Auto-generated token for API authentication |
| `conditions` | []Condition | Standard Kubernetes conditions |
| `resources.ingressRoute` | string | Name of created IngressRoute |
| `resources.middleware` | string | Name of created Middleware |
| `resources.stripMiddleware` | string | Name of strip middleware (for pathPrefix) |
| `resources.certificate` | string | Name of created Certificate |

## Conditions

The status includes standard Kubernetes conditions:

| Type | Description |
|------|-------------|
| `Ready` | Site is deployed and accessible |
| `Synced` | Git repository is synced |
| `IngressReady` | IngressRoute is configured |
| `CertificateReady` | TLS certificate is issued |

## Example

```yaml
apiVersion: pages.kup6s.com/v1beta1
kind: StaticSite
metadata:
  name: my-website
  namespace: pages
spec:
  repo: https://github.com/user/my-website.git
  branch: main
  path: /dist
  domain: www.example.com
  syncInterval: 10m
status:
  phase: Ready
  message: "Site is live"
  lastSync: "2024-01-15T10:30:00Z"
  lastCommit: "abc1234"
  url: "https://www.example.com"
  syncToken: "xxxxxxxx"
  resources:
    ingressRoute: "pages--my-website"
    middleware: "pages--my-website-prefix"
    certificate: "www-example-com-tls"
```

## Short Names

The CRD registers the short name `ss` for convenience:

```bash
kubectl get ss -n pages
kubectl describe ss my-website -n pages
```
