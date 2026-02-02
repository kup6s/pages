---
title: CLI Flags
weight: 30
---

# CLI Flags

Command-line flags for the operator and syncer components.

## Operator Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--pages-domain` | `pages.kup6s.com` | Base domain for auto-generated subdomains |
| `--cluster-issuer` | `letsencrypt-prod` | cert-manager ClusterIssuer name |
| `--nginx-namespace` | `kup6s-pages` | Namespace where nginx service runs |
| `--nginx-service-name` | `kup6s-pages-nginx` | Name of the nginx service |
| `--metrics-bind-address` | `:8080` | Metrics endpoint |
| `--health-probe-bind-address` | `:8081` | Health probe endpoint |

### Example

```bash
go run ./cmd/operator \
  --pages-domain=pages.example.com \
  --cluster-issuer=letsencrypt-prod \
  --nginx-namespace=kup6s-pages \
  --nginx-service-name=kup6s-pages-nginx
```

## Syncer Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--sites-root` | `/sites` | Directory where sites are stored |
| `--sync-interval` | `5m` | Default interval for polling repos |
| `--webhook-addr` | `:8080` | Webhook HTTP server address |
| `--allowed-hosts` | **Required** | Comma-separated allowlist of Git hosts |
| `--webhook-secret` | `""` | Secret for webhook HMAC validation |

### Example

```bash
go run ./cmd/syncer \
  --sites-root=/sites \
  --sync-interval=5m \
  --webhook-addr=:8080 \
  --allowed-hosts=github.com,gitlab.com,forgejo.example.com \
  --webhook-secret=your-secret-here
```

## Allowed Hosts

The `--allowed-hosts` flag provides SSRF (Server-Side Request Forgery) protection. The syncer will only clone repositories from these hosts.

**Common values:**
- `github.com`
- `gitlab.com`
- `bitbucket.org`
- `codeberg.org`

**Wildcards** are supported for self-hosted instances:
- `*.gitlab.example.com`
- `git.internal.company.com`

**Example:**
```bash
--allowed-hosts=github.com,gitlab.com,*.gitlab.internal.example.com
```
