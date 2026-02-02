# Security Documentation

This document describes the RBAC configuration and security considerations for kup6s-pages.

## RBAC Architecture

The operator uses a **ClusterRole** because it watches StaticSite resources across all
namespaces (multi-tenant design). This is the standard pattern for namespace-scoped
operators like cert-manager and ArgoCD.

### Resource Creation Pattern

All managed resources are created in the **StaticSite's namespace**, not a fixed system
namespace:

| Resource | Location | Ownership |
|----------|----------|-----------|
| IngressRoutes | StaticSite's namespace | ownerReferences (auto-cleanup) |
| Middlewares | StaticSite's namespace | ownerReferences (auto-cleanup) |
| Certificates | StaticSite's namespace | Labels (shared across sites) |
| Services | StaticSite's namespace | Labels (shared across sites) |

## Operator Permissions

### StaticSite CRD

```yaml
verbs: ["get", "list", "watch", "update", "patch"]
```

- **get/list/watch**: Core reconciliation loop
- **update/patch**: Adding finalizers for cleanup coordination
- **create/delete**: Not granted (users create/delete StaticSites)

### Traefik Resources (IngressRoutes, Middlewares)

```yaml
verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]
```

- **create**: Creates IngressRoutes and Middlewares for each StaticSite
- **delete**: Required for garbage collection via ownerReferences

### cert-manager Certificates

```yaml
verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]
```

- **create**: Issues TLS certificates for custom domains
- **delete**: Explicit cleanup when no sites use a domain (certificates are shared)

### Core Services

```yaml
verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]
```

- **create**: Creates ExternalName services for cross-namespace nginx routing
- **delete**: Explicit cleanup when last StaticSite in namespace is deleted

### Events

```yaml
verbs: ["create", "patch"]
```

Records reconciliation events on StaticSite resources.

### Coordination Leases

```yaml
verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]
```

Required for leader election in multi-replica deployments.

## Syncer Permissions

The syncer has minimal permissions - read-only access to StaticSites plus status updates:

```yaml
# StaticSites: read-only + status
verbs: ["get", "list", "watch"]  # for staticsites
verbs: ["get", "update", "patch"]  # for staticsites/status
```

Secrets access is **not granted by default**. Users must create namespace-scoped Roles
to grant the syncer access to Git credentials. See the README for configuration details.

## Security Considerations

### Principle of Least Privilege

- The operator cannot create or delete StaticSite resources
- The syncer has no secrets access by default
- All resources are scoped to user namespaces

### Shared Resource Management

Some resources are shared across multiple StaticSites:

- **Certificates**: Shared by sites with the same domain
- **Services**: Shared by all sites in a namespace

The operator uses labels to track ownership and performs explicit orphan checks before
deletion to prevent accidental removal of resources still in use.

### Cross-Namespace Access

The syncer can be granted secrets access in user namespaces via RoleBindings:

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: pages-syncer-secrets
  namespace: customer-namespace
rules:
  - apiGroups: [""]
    resources: ["secrets"]
    verbs: ["get"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: pages-syncer-secrets
  namespace: customer-namespace
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: Role
  name: pages-syncer-secrets
subjects:
  - kind: ServiceAccount
    name: kup6s-pages-syncer
    namespace: kup6s-pages
```

This grants read-only access to secrets in the customer namespace only.

## Additional Hardening

For environments requiring additional restrictions:

1. **OPA/Gatekeeper policies**: Restrict which namespaces can contain StaticSites
2. **Network policies**: Limit operator/syncer egress to required endpoints
3. **Pod security standards**: Both operator and syncer run as non-root with read-only
   root filesystem
