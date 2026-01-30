# CLAUDE.md - kup6s-pages

Go-based Kubernetes Operator for multi-tenant static website hosting.

## Architecture

Two components sharing a PVC:
- **Operator** (`cmd/operator/`) - Watches `StaticSite` CRDs, creates Traefik IngressRoutes + Certificates
- **Syncer** (`cmd/syncer/`) - Clones/pulls Git repos, serves webhooks

Traffic: Traefik → addPrefix middleware → nginx → `/sites/<name>/`

## Project Structure

```
cmd/operator/      # Operator entrypoint
cmd/syncer/        # Syncer entrypoint
pkg/apis/v1alpha1/ # StaticSite CRD types
pkg/controller/    # Reconciliation logic
pkg/syncer/        # Git sync + webhook server
deploy/            # K8s manifests (crd, rbac, operator, nginx)
```

## Common Commands

```bash
make build          # Build both binaries
make test           # Run all tests
make run-operator   # Local dev (requires kubeconfig)
make run-syncer     # Local dev with tmp dir

make docker-build   # Build container images
make deploy         # Apply all manifests
```

## Key APIs

- CRD: `staticsites.pages.kup6s.io/v1alpha1`
- Namespace: `kup6s-pages`
- Service: `static-sites-nginx` (HTTP), `pages-syncer` (webhooks)

## Dependencies

- Go 1.22
- controller-runtime v0.18
- go-git v5.12
- Requires: Traefik, cert-manager, RWX StorageClass
