---
title: Development
weight: 60
---

# Development

Guide for contributors and developers.

## Project Structure

```
pages/
├── cmd/
│   ├── operator/         # Operator entrypoint
│   └── syncer/           # Syncer entrypoint
├── pkg/
│   ├── apis/v1alpha1/    # CRD types
│   ├── controller/       # Reconciliation logic
│   └── syncer/           # Git sync and webhook server
└── charts/kup6s-pages/   # Helm chart
    ├── Chart.yaml
    ├── values.yaml
    ├── templates/
    ├── crds/
    └── tests/            # Helm unit tests
```

## Prerequisites

- Go 1.21+
- Helm 3
- Docker (for building images)
- Access to a Kubernetes cluster (for testing)

## Build

```bash
# Build both binaries
make build

# Build individually
make build-operator
make build-syncer
```

## Test

```bash
# Go tests
make test

# Helm chart tests
make helm-lint
make helm-test
```

## Run Locally

```bash
# Operator (requires kubeconfig)
make run-operator
# Or with custom flags:
go run ./cmd/operator --pages-domain=pages.local --cluster-issuer=selfsigned

# Syncer
make run-syncer
# Or with custom flags:
go run ./cmd/syncer --sites-root=./tmp/sites --sync-interval=30s --allowed-hosts=github.com
```

## Code Quality

```bash
# Run linter
make lint

# Format code
make fmt

# Vet
make vet

# Tidy dependencies
make tidy
```

## Docker Images

```bash
# Build images
make docker-build

# Push images
make docker-push
```

## Helm Development

```bash
# Lint chart
make helm-lint

# Run unit tests
make helm-test

# Template chart (preview output)
make helm-template

# Install locally
make deploy

# Uninstall
make undeploy
```

## Key Packages

| Package | Description |
|---------|-------------|
| `pkg/apis/v1alpha1` | StaticSite CRD types and scheme registration |
| `pkg/controller` | Operator reconciliation logic |
| `pkg/syncer` | Git synchronization and webhook handlers |

## Contributing

1. Fork the repository
2. Create a feature branch
3. Make your changes
4. Run `make lint` and `make test`
5. Submit a pull request

## Documentation

Documentation is built with [Hugo](https://gohugo.io/) using the [Book theme](https://github.com/alex-shpak/hugo-book).

```bash
# Build docs
make docs-build

# Serve locally
make docs-serve
```

## Related Documentation

- [CONCEPT.md](https://github.com/kup6s/pages/blob/main/docs/CONCEPT.md) - Detailed architecture and design rationale
- [SECURITY.md](https://github.com/kup6s/pages/blob/main/docs/SECURITY.md) - RBAC and security model
- [RELEASE.md](https://github.com/kup6s/pages/blob/main/docs/RELEASE.md) - Release process
