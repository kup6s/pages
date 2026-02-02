# CLAUDE.md - kup6s-pages

Go-based Kubernetes Operator for multi-tenant static website hosting.

## Architecture

Two components sharing a PVC:
- **Operator** (`cmd/operator/`) - Watches `StaticSite` CRDs, creates Traefik IngressRoutes + Certificates
- **Syncer** (`cmd/syncer/`) - Clones/pulls Git repos, serves webhooks

Traffic: Traefik → addPrefix middleware → nginx → `/sites/<name>/`

## Project Structure

```
cmd/operator/        # Operator entrypoint
cmd/syncer/          # Syncer entrypoint
pkg/apis/v1alpha1/   # StaticSite CRD types
pkg/controller/      # Reconciliation logic
pkg/syncer/          # Git sync + webhook server
charts/kup6s-pages/  # Helm chart
```

## Common Commands

```bash
# Build
go build ./...

# Test
go test ./...
helm unittest charts/kup6s-pages

# Local development
go run ./cmd/operator --pages-domain=pages.local
go run ./cmd/syncer --sites-root=/tmp/sites

# Install via Helm
helm install pages oci://ghcr.io/kup6s/kup6s-pages
```

## Key APIs

- CRD: `staticsites.pages.kup6s.com/v1alpha1`
- Namespace: `kup6s-pages`
- Service: `static-sites-nginx` (HTTP), `pages-syncer` (webhooks)

## Dependencies

- Go, controller-runtime, go-git (see go.mod for versions)
- Requires: Traefik, cert-manager, RWX StorageClass

# Issues/ Commits

- Do not and NEVER mention AI/Claude.
- Work TDD
- Always run `make lint` before committing.
- Always pull main firscreate a branch with worktree under `./.worktrees` in the project and PR if ready.
- cleanup after merge
