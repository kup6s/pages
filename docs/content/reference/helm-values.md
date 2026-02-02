---
title: Helm Values
weight: 20
---

# Helm Values Reference

Complete reference for all Helm chart configuration options.

## Global

| Value | Default | Description |
|-------|---------|-------------|
| `nameOverride` | `""` | Override the chart name |
| `fullnameOverride` | `""` | Override the full release name |
| `createNamespace` | `true` | Create namespace (set to false if managed externally) |
| `namespace` | `kup6s-pages` | Target namespace for deployment |
| `imagePullSecrets` | `[]` | Global image pull secrets |

## CRD Configuration

| Value | Default | Description |
|-------|---------|-------------|
| `crds.install` | `false` | Install CRD with the chart (use `--skip-crds` to skip) |
| `crds.keep` | `true` | Keep CRD when chart is uninstalled |

## Operator

| Value | Default | Description |
|-------|---------|-------------|
| `operator.replicas` | `1` | Number of operator replicas |
| `operator.image.registry` | `ghcr.io` | Image registry |
| `operator.image.repository` | `kup6s/pages-operator` | Image repository |
| `operator.image.tag` | `""` | Image tag (defaults to Chart.appVersion) |
| `operator.image.pullPolicy` | `IfNotPresent` | Image pull policy |
| `operator.pagesDomain` | `pages.kup6s.com` | Pages domain for auto-generated site URLs |
| `operator.clusterIssuer` | `letsencrypt-prod` | cert-manager ClusterIssuer name |
| `operator.pagesTlsMode` | `individual` | TLS mode for auto-generated domains: `individual` (HTTP-01 per site) or `wildcard` (pre-existing wildcard cert) |
| `operator.pagesWildcardSecret` | `pages-wildcard-tls` | Secret name for wildcard certificate (only used when `pagesTlsMode=wildcard`) |
| `operator.metricsBindAddress` | `:8080` | Metrics bind address |
| `operator.healthProbeBindAddress` | `:8081` | Health probe bind address |
| `operator.extraArgs` | `[]` | Additional CLI arguments |
| `operator.resources.limits.cpu` | `200m` | CPU limit |
| `operator.resources.limits.memory` | `128Mi` | Memory limit |
| `operator.resources.requests.cpu` | `100m` | CPU request |
| `operator.resources.requests.memory` | `64Mi` | Memory request |
| `operator.nodeSelector` | `{}` | Node selector |
| `operator.tolerations` | `[]` | Tolerations |
| `operator.affinity` | `{}` | Affinity rules |
| `operator.serviceAccount.create` | `true` | Create service account |
| `operator.serviceAccount.name` | `""` | Service account name |
| `operator.serviceAccount.annotations` | `{}` | Service account annotations |

## Syncer

| Value | Default | Description |
|-------|---------|-------------|
| `syncer.replicas` | `1` | Number of syncer replicas |
| `syncer.image.registry` | `ghcr.io` | Image registry |
| `syncer.image.repository` | `kup6s/pages-syncer` | Image repository |
| `syncer.image.tag` | `""` | Image tag (defaults to Chart.appVersion) |
| `syncer.image.pullPolicy` | `IfNotPresent` | Image pull policy |
| `syncer.syncInterval` | `5m` | Default sync interval for git repositories |
| `syncer.webhookAddr` | `:8080` | Webhook server listen address |
| `syncer.sitesRoot` | `/sites` | Sites root directory |
| `syncer.allowedHosts` | `[]` | **Required.** Allowed Git hosts for SSRF protection |
| `syncer.extraArgs` | `[]` | Additional CLI arguments |
| `syncer.resources.limits.cpu` | `500m` | CPU limit |
| `syncer.resources.limits.memory` | `256Mi` | Memory limit |
| `syncer.resources.requests.cpu` | `100m` | CPU request |
| `syncer.resources.requests.memory` | `128Mi` | Memory request |
| `syncer.nodeSelector` | `{}` | Node selector |
| `syncer.tolerations` | `[]` | Tolerations |
| `syncer.affinity` | `{}` | Affinity rules |
| `syncer.serviceAccount.create` | `true` | Create service account |
| `syncer.serviceAccount.name` | `""` | Service account name |
| `syncer.service.type` | `ClusterIP` | Service type |
| `syncer.service.port` | `80` | Service port |

## nginx

| Value | Default | Description |
|-------|---------|-------------|
| `nginx.replicas` | `2` | Number of nginx replicas for HA |
| `nginx.image.registry` | `docker.io` | Image registry |
| `nginx.image.repository` | `library/nginx` | Image repository |
| `nginx.image.tag` | `1.25-alpine` | Image tag |
| `nginx.image.pullPolicy` | `IfNotPresent` | Image pull policy |
| `nginx.resources.limits.cpu` | `200m` | CPU limit |
| `nginx.resources.limits.memory` | `128Mi` | Memory limit |
| `nginx.resources.requests.cpu` | `50m` | CPU request |
| `nginx.resources.requests.memory` | `64Mi` | Memory request |
| `nginx.nodeSelector` | `{}` | Node selector |
| `nginx.tolerations` | `[]` | Tolerations |
| `nginx.affinity` | (pod anti-affinity) | Affinity rules |
| `nginx.service.type` | `ClusterIP` | Service type |
| `nginx.service.port` | `80` | Service port |
| `nginx.customConfig` | `""` | Custom nginx configuration |
| `nginx.pdb.enabled` | `true` | Enable PodDisruptionBudget |
| `nginx.pdb.minAvailable` | `1` | Minimum available pods |

## Storage

| Value | Default | Description |
|-------|---------|-------------|
| `storage.existingClaim` | `""` | Use existing PVC instead of creating one |
| `storage.storageClassName` | `""` | Storage class name (empty uses cluster default) |
| `storage.size` | `10Gi` | Storage size |
| `storage.accessModes` | `[ReadWriteMany]` | Access modes |
| `storage.annotations` | `{}` | PVC annotations |

## Webhook

| Value | Default | Description |
|-------|---------|-------------|
| `webhook.enabled` | `false` | Enable webhook IngressRoute and Certificate |
| `webhook.domain` | `webhook.pages.kup6s.com` | Webhook domain |
| `webhook.clusterIssuer` | `""` | ClusterIssuer (defaults to operator.clusterIssuer) |
| `webhook.entryPoints` | `[websecure]` | Traefik entrypoints |
| `webhook.annotations` | `{}` | Additional IngressRoute annotations |
| `webhook.secret` | `""` | Webhook secret for HMAC validation |
| `webhook.secretRef.name` | `""` | Reference to existing secret |
| `webhook.secretRef.key` | `webhook-secret` | Key in the secret |

## RBAC

| Value | Default | Description |
|-------|---------|-------------|
| `rbac.create` | `true` | Create ClusterRole and ClusterRoleBinding resources |
