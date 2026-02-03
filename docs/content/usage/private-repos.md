---
title: Private Repositories
weight: 10
---

# Private Repositories

For private repositories, you need to:
1. Grant the syncer access to secrets in your namespace
2. Create a secret with your Git credentials
3. Reference the secret in your StaticSite

## Step 1: Grant Syncer Access (RBAC)

The syncer does not have cluster-wide secret access. You must grant it access to read secrets in your namespace:

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: pages-syncer-secrets
  namespace: pages           # Your namespace
rules:
  - apiGroups: [""]
    resources: ["secrets"]
    verbs: ["get"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: pages-syncer-secrets
  namespace: pages           # Your namespace
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: Role
  name: pages-syncer-secrets
subjects:
  - kind: ServiceAccount
    name: kup6s-pages-syncer # Syncer ServiceAccount (adjust if using custom release name)
    namespace: kup6s-pages   # Syncer namespace
```

## Step 2: Create a Secret

Create a Secret with your deploy token:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: my-repo-token
  namespace: pages
type: Opaque
stringData:
  password: "glpat-xxxxxxxxxxxx"  # Your Forgejo/GitLab/GitHub token
  username: "git"                 # Optional, defaults to "git"
```

## Step 3: Reference the Secret

Reference the Secret in your StaticSite:

```yaml
apiVersion: pages.kup6s.com/v1beta1
kind: StaticSite
metadata:
  name: internal-docs
  namespace: pages
spec:
  repo: https://forgejo.example.com/org/private-repo.git
  domain: docs.internal.example.com
  secretRef:
    name: my-repo-token
    key: password           # Optional, defaults to "password"
```

The Secret must be in the same namespace as the StaticSite. Without the RBAC setup, syncing will fail with "permission denied".

## Troubleshooting

**Authentication fails:**

1. Verify the RBAC is set up:
   ```bash
   kubectl get rolebinding pages-syncer-secrets -n pages
   ```

2. Verify the Secret exists and has the correct key:
   ```bash
   kubectl get secret my-repo-token -n pages -o yaml
   ```

3. Test the token manually:
   ```bash
   git clone https://git:YOUR_TOKEN@forgejo.example.com/org/repo.git
   ```
