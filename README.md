# kup6s-pages

Cloud native multi-tenant static web-hosting for Kubernetes.

## Overview

kup6s-pages deploys static websites from Git repositories to Kubernetes. A single nginx pod serves all sites efficiently, with Traefik handling routing via `addPrefix` middleware. The operator automatically manages IngressRoutes and TLS certificates.

**Key Features:**

- Single nginx pod for all sites (no per-site overhead)
- CRD-based declarative configuration
- Automatic TLS via cert-manager
- Traefik IngressRoute integration
- Git-based deployments with webhook support
- Private repository support via deploy tokens

## Quick Start

```bash
# Install
helm install pages oci://ghcr.io/kup6s/kup6s-pages \
  --set operator.pagesDomain=pages.example.com \
  --set 'syncer.allowedHosts={github.com}'

# Deploy a site
kubectl apply -f - <<EOF
apiVersion: pages.kup6s.com/v1beta1
kind: StaticSite
metadata:
  name: my-website
  namespace: pages
spec:
  repo: https://github.com/user/my-website.git
  domain: www.example.com
EOF

# Check status
kubectl get staticsites -n pages
```

## Documentation

Full documentation: https://pages-docs.kup6s.com

- [Installation](https://pages-docs.kup6s.com/installation/)
- [Usage Guide](https://pages-docs.kup6s.com/usage/)
- [Reference](https://pages-docs.kup6s.com/reference/)
- [Troubleshooting](https://pages-docs.kup6s.com/troubleshooting/)

## Development

```bash
make build      # Build binaries
make test       # Run tests (go)
make lint       # Run linter (go)
make helm-test  # Run tests (helm)
make helm-lint  # Run linter (helm)
make docs-serve # Preview documentation
```

See [Development Guide](https://pages-docs.kup6s.com/development/) for details.

## License

EUPL-1.2
