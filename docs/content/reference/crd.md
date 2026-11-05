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
| `visibility` | string | No | `Public` | `Public` or `Private`. A private site gets no IngressRoute, Middleware, Certificate or auto-generated domain; see [Private Sites]({{< relref "/usage/private-sites" >}}) |
| `secretRef.name` | string | No | - | Secret name with Git credentials |
| `secretRef.key` | string | No | `password` | Key in Secret for the token |
| `syncInterval` | string | No | `5m` | How often to pull updates |

The API server rejects a `Private` site that sets `domain` or `pathPrefix`.

## Status Fields

| Field | Type | Description |
|-------|------|-------------|
| `phase` | string | `Pending`, `Syncing`, `Ready` or `Error`; derived from the `Synced` condition and reconcile errors |
| `message` | string | Human-readable status message |
| `lastSync` | timestamp | Timestamp of last successful sync |
| `lastCommit` | string | Short SHA of the last synced commit |
| `url` | string | Full URL of the deployed site; empty for private sites |
| `internalURL` | string | In-cluster address of the site, served by nginx, e.g. `http://kup6s-pages-nginx.kup6s-pages.svc/my-website/` |
| `syncToken` | string | Auto-generated token for API authentication |
| `conditions` | []Condition | Standard Kubernetes conditions |
| `resources.ingressRoute` | string | Name of created IngressRoute |
| `resources.middleware` | string | Name of created Middleware |
| `resources.stripMiddleware` | string | Name of strip middleware (for pathPrefix) |
| `resources.certificate` | string | Name of created Certificate |

Private sites have no `resources`.

## Conditions

The status includes standard Kubernetes conditions:

| Type | Description |
|------|-------------|
| `Synced` | Git repository is synced (see below) |
| `CertificateReady` | TLS certificate is issued; mirrors the Certificate's `Ready` condition. Not set for private sites and for auto-generated domains in wildcard TLS mode |

### Synced

Only the syncer writes this condition.

| Status | Reason | Meaning |
|---|---|---|
| `True` | `Synced` | The configured branch and path are checked out. `lastCommit` names the commit |
| `Unknown` | `Syncing` | A clone is running |
| `False` | `CloneFailed`, `FetchFailed`, `AuthFailed`, `SubpathMissing`, `DiskFull`, `Timeout`, `InvalidSpec`, `SyncFailed` | The last sync failed. The message contains the error. The site keeps serving its last good content |

`kubectl get staticsites` shows the condition in the `Synced` column.

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
