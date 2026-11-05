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
pkg/apis/v1beta1/    # StaticSite CRD types
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
go run ./cmd/syncer --sites-root=/tmp/sites

# Install via Helm
helm install pages oci://ghcr.io/kup6s/kup6s-pages
```

## Key APIs

- CRD: `staticsites.pages.kup6s.com/v1beta1`
- Namespace: `kup6s-pages`
- Service: `<release>-nginx` (HTTP), `<release>-syncer` (webhooks)
- The CRD in `charts/kup6s-pages/crds/` is maintained by hand. Do not regenerate it with
  controller-gen: its output differs (validation patterns, short names, columns).
  Change the YAML and the types together; `pkg/apis/v1beta1/crd_test.go` checks they match.
- The operator always runs with leader election, so it does not start outside a cluster
  (`go run ./cmd/operator` fails with "unable to find leader election namespace").

## Dependencies

- Go, controller-runtime, git CLI in the syncer image (see go.mod and Dockerfile.syncer)
- Requires: Traefik, cert-manager, RWX StorageClass

# Issues/ Commits

- Commits, PRs, issues and comments written with AI assistance end with an
  `Assisted-by: <model>` line, e.g. `Assisted-by: Claude Opus 5.5`.
  No `Co-Authored-By`, no noreply address.
- Work TDD
- Always run `make lint` before committing.
- Always pull main first, create a branch with a worktree under `./.worktrees` in the project, and open a PR when ready.
- Clean up after merge.
