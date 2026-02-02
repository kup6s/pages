---
title: Installation
weight: 10
---

# Installation

## Via Helm (Recommended)

```bash
# Install from OCI registry
helm install pages oci://ghcr.io/kup6s/kup6s-pages --version 0.1.0

# Or with custom configuration
helm install pages oci://ghcr.io/kup6s/kup6s-pages \
  --set operator.pagesDomain=pages.example.com \
  --set operator.clusterIssuer=letsencrypt-prod \
  --set 'syncer.allowedHosts={github.com,gitlab.com}' \
  --set storage.storageClassName=longhorn \
  --set webhook.enabled=true \
  --set webhook.domain=webhook.pages.example.com
```

## Key Helm Values

| Value | Default | Description |
|-------|---------|-------------|
| `operator.pagesDomain` | `pages.kup6s.com` | Base domain for auto-generated URLs |
| `operator.clusterIssuer` | `letsencrypt-prod` | cert-manager ClusterIssuer |
| `syncer.allowedHosts` | **Required** | Allowed Git hosts (SSRF protection) |
| `storage.size` | `10Gi` | PVC size for sites |
| `storage.storageClassName` | (default) | StorageClass (must support RWX) |
| `nginx.replicas` | `2` | nginx replicas for HA |
| `webhook.enabled` | `false` | Enable webhook IngressRoute |
| `webhook.domain` | `webhook.pages.kup6s.com` | Webhook endpoint domain |

See [Helm Values Reference]({{< relref "/reference/helm-values" >}}) for all options.

## Verify Installation

```bash
# Check operator and syncer are running
kubectl get pods -n kup6s-pages

# Check CRD is registered
kubectl get crd staticsites.pages.kup6s.com
```

Expected output:

```
NAME                                      READY   STATUS    RESTARTS   AGE
kup6s-pages-operator-xxx                  1/1     Running   0          1m
kup6s-pages-syncer-xxx                    1/1     Running   0          1m
kup6s-pages-nginx-xxx                     1/1     Running   0          1m
kup6s-pages-nginx-yyy                     1/1     Running   0          1m
```
