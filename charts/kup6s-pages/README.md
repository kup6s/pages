# kup6s-pages Helm Chart

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

## Prerequisites

- Kubernetes 1.24+
- Helm 3.0+
- Traefik ingress controller
- cert-manager (for TLS)
- RWX-capable StorageClass (for shared site storage)

## Installation

### Install from OCI Registry

```bash
helm install pages oci://ghcr.io/kup6s/kup6s-pages \
  --create-namespace \
  --namespace kup6s-pages \
  --set operator.pagesDomain=pages.example.com \
  --set 'syncer.allowedHosts={github.com}'
```

### Install from Source

```bash
git clone https://github.com/kup6s/pages.git
cd pages/charts/kup6s-pages
helm install pages . \
  --create-namespace \
  --namespace kup6s-pages \
  --set operator.pagesDomain=pages.example.com \
  --set 'syncer.allowedHosts={github.com}'
```

## Required Configuration

### Pages Domain

The `operator.pagesDomain` setting determines the base domain for hosted sites:

```bash
--set operator.pagesDomain=pages.example.com
```

Sites will be accessible at `<site-name>.pages.example.com`.

### Allowed Git Hosts (Security)

**IMPORTANT:** The `syncer.allowedHosts` setting is **required** to prevent SSRF attacks. It limits which Git hosts can be cloned:

```bash
--set 'syncer.allowedHosts={github.com,gitlab.com}'
```

For self-hosted Git servers:

```bash
--set 'syncer.allowedHosts={git.example.com,github.com}'
```

## Quick Start Example

After installation, deploy a static site:

```bash
kubectl apply -f - <<EOF
apiVersion: pages.kup6s.com/v1beta1
kind: StaticSite
metadata:
  name: my-website
  namespace: kup6s-pages
spec:
  repo: https://github.com/user/my-website.git
  domain: my-website.pages.example.com
EOF
```

Check the site status:

```bash
kubectl get staticsites -n kup6s-pages
```

The site will be accessible at `https://my-website.pages.example.com` once the operator creates the IngressRoute and TLS certificate.

## Common Configuration Examples

### Custom Domain with Wildcard TLS

For many sites on a single wildcard certificate:

```bash
helm install pages oci://ghcr.io/kup6s/kup6s-pages \
  --set operator.pagesDomain=pages.example.com \
  --set operator.pagesTlsMode=wildcard \
  --set operator.pagesWildcardSecret=wildcard-tls-cert \
  --set 'syncer.allowedHosts={github.com}'
```

Create the wildcard certificate separately:

```yaml
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: wildcard-tls-cert
  namespace: kup6s-pages
spec:
  secretName: wildcard-tls-cert
  dnsNames:
    - "*.pages.example.com"
  issuerRef:
    name: letsencrypt-prod
    kind: ClusterIssuer
```

### Custom Storage Class

```bash
helm install pages oci://ghcr.io/kup6s/kup6s-pages \
  --set storage.storageClass=nfs-client \
  --set storage.size=50Gi \
  --set operator.pagesDomain=pages.example.com \
  --set 'syncer.allowedHosts={github.com}'
```

### Private Repository Access

Store credentials as a Kubernetes secret:

```bash
kubectl create secret generic my-deploy-token \
  -n kup6s-pages \
  --from-literal=username=deploy-token \
  --from-literal=password=YOUR_TOKEN
```

Reference in StaticSite:

```yaml
apiVersion: pages.kup6s.com/v1beta1
kind: StaticSite
metadata:
  name: private-site
  namespace: kup6s-pages
spec:
  repo: https://github.com/user/private-repo.git
  domain: private.pages.example.com
  authSecretRef:
    name: my-deploy-token
```

## Configuration Options

See [values.yaml](values.yaml) for all available configuration options.

Key settings:

| Parameter | Description | Default |
|-----------|-------------|---------|
| `operator.pagesDomain` | Base domain for sites | `""` (required) |
| `operator.pagesTlsMode` | TLS mode: `individual` or `wildcard` | `individual` |
| `syncer.allowedHosts` | Allowed Git hosts (SECURITY) | `[]` (required) |
| `storage.storageClass` | StorageClass for site storage | `""` (cluster default) |
| `storage.size` | PVC size | `10Gi` |
| `operator.clusterIssuer` | cert-manager ClusterIssuer | `letsencrypt-prod` |

## Documentation

Full documentation: https://pages-docs.sites.kup6s.com

- [Installation Guide](https://pages-docs.sites.kup6s.com/installation/)
- [Usage Guide](https://pages-docs.sites.kup6s.com/usage/)
- [Configuration Reference](https://pages-docs.sites.kup6s.com/reference/)
- [Troubleshooting](https://pages-docs.sites.kup6s.com/troubleshooting/)

## Support

- Report issues: https://github.com/kup6s/pages/issues
- Source code: https://github.com/kup6s/pages

## License

EUPL-1.2
