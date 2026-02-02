# Security Documentation

This document describes the RBAC configuration and security considerations for kup6s-pages.

## RBAC Architecture

The operator uses a split RBAC model for improved security:

1. **ClusterRole**: Read-only access to StaticSites across all namespaces
2. **Role**: Full CRUD on generated resources in the system namespace only

This design minimizes the blast radius - a compromised operator can only affect
resources in the system namespace, not user namespaces.

### Resource Creation Pattern

All managed resources are created in the **system namespace** (where the operator runs),
not in user namespaces:

| Resource | Location | Naming Pattern | Ownership |
|----------|----------|----------------|-----------|
| IngressRoutes | System namespace | `{site-ns}--{site-name}` | Labels (explicit cleanup) |
| Middlewares | System namespace | `{site-ns}--{site-name}-prefix` | Labels (explicit cleanup) |
| Certificates | System namespace | `{domain}-tls` | Labels (shared across sites) |

Users can see which resources were created via the StaticSite status:

```yaml
status:
  resources:
    ingressRoute: kup6s-pages/customer-ns--my-site
    middleware: kup6s-pages/customer-ns--my-site-prefix
    certificate: kup6s-pages/www-example-com-tls
```

## Operator Permissions

### ClusterRole (cluster-wide, read-heavy)

```yaml
# StaticSite CRD
verbs: ["get", "list", "watch", "update", "patch"]  # update/patch for finalizers
# StaticSite status
verbs: ["get", "update", "patch"]
# StaticSite finalizers
verbs: ["update"]
# Events
verbs: ["create", "patch"]
```

- **get/list/watch**: Core reconciliation loop
- **update/patch**: Adding finalizers for cleanup coordination
- **create/delete**: Not granted (users create/delete StaticSites)

### Role (system namespace only)

```yaml
# Traefik IngressRoutes and Middlewares
verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]
# cert-manager Certificates
verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]
# Coordination Leases (leader election)
verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]
```

- **create**: Creates IngressRoutes, Middlewares, Certificates for each StaticSite
- **delete**: Required for explicit cleanup via finalizers

## Syncer Permissions

The syncer has minimal permissions - read-only access to StaticSites plus status updates:

```yaml
# StaticSites: read-only + status
verbs: ["get", "list", "watch"]  # for staticsites
verbs: ["get", "update", "patch"]  # for staticsites/status
```

Secrets access is **not granted by default**. Users must create namespace-scoped Roles
to grant the syncer access to Git credentials. See the README for configuration details.

## Security Benefits

### Reduced Blast Radius

| Aspect | Before | After |
|--------|--------|-------|
| Operator ClusterRole | CRUD on Services, IngressRoutes, Certificates in ALL namespaces | Read-only StaticSites + events |
| Where resources are created | User namespaces | System namespace only |
| Compromise impact | Cluster-wide networking changes | System namespace only |

### Simplified Auditing

All generated resources are in one namespace, making it easier to:
- Audit what the operator has created
- Apply network policies
- Monitor for unexpected changes

### User Namespace Isolation

The operator never creates or modifies resources in user namespaces. Users only need to:
- Create StaticSite CRDs
- Optionally grant syncer access to secrets (if using private repos)

## Shared Resource Management

Some resources are shared across multiple StaticSites:

- **Certificates**: Shared by sites with the same domain

The operator uses labels to track ownership and performs explicit orphan checks before
deletion to prevent accidental removal of resources still in use.

## Cross-Namespace Secret Access (Syncer)

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
4. **Traefik namespace filtering**: Configure Traefik to only watch IngressRoutes
   in the system namespace for additional isolation
