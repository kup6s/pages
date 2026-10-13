---
title: Troubleshooting
weight: 40
---

# Troubleshooting

Common issues and solutions.

## Site Stuck in "Pending" or "Syncing"

`Pending` means the syncer has not reported a result yet.
`Syncing` means a clone is running.
Check the `Synced` condition:

```bash
kubectl get staticsite <name> -o jsonpath='{.status.conditions[?(@.type=="Synced")]}'
kubectl -n kup6s-pages logs -l app.kubernetes.io/component=syncer --tail=100
```

A site that stays `Syncing` for a long time while the syncer pod restarts was most likely killed during the clone.
Look for `OOMKilled` in `kubectl -n kup6s-pages describe pod -l app.kubernetes.io/component=syncer`.
See "Sync Failures" below.

## Sync Failures

The syncer sets `Synced=False` with one of these reasons.
The site keeps serving its last good content.

| Reason | Meaning | What to do |
|---|---|---|
| `CloneFailed` | `git clone` failed, for example because of an unknown branch, an unreachable host or a redirected URL | Check `spec.branch` and `spec.repo`. The message contains git's error. git does not follow HTTP redirects, so use the final URL of the repository, for example after a project rename or with the `.git` suffix some servers require |
| `FetchFailed` | Updating an existing clone failed | Usually network or server trouble. The syncer retries every interval |
| `AuthFailed` | The secret is missing or the server rejected the credentials | See "Private Repo Authentication Fails" |
| `SubpathMissing` | `spec.path` does not exist on the branch | Fix `spec.path` |
| `DiskFull` | The sites volume has less free space than `--min-free-percent` | Grow the PVC or remove unused sites |
| `Timeout` | A git operation took longer than `--git-timeout` | Raise `syncer.gitTimeout` for very large repositories |
| `InvalidSpec` | `repo` is not allowed by `--allowed-hosts`, or `path` is invalid | Fix the StaticSite |

Broken or interrupted clones need no manual cleanup.
The syncer detects them, including a lock file left by a fetch that timed out or was killed, deletes the leftovers, and clones again on the next sync.

The syncer emits a `SyncFailed` event when a site starts failing and a `SyncRecovered` event when it recovers:

```bash
kubectl get events --field-selector involvedObject.name=<name>
```

With `metrics.prometheusRule.enabled`, these alerts fire:

- `PagesSiteSyncFailing`: the site has not been synced for 15 minutes.
- `PagesSiteSyncStale`: no successful sync for `staleAfterSeconds`, for example because the syncer is down.
- `PagesVolumeLow`: the sites volume is running full.

### Syncer Runs Out of Memory

Sites with a `path` need little memory, because only that directory is downloaded.
Sites with `path: /` and repositories above about 1 GB can exceed the default limit of 256Mi.
Raise `syncer.resources.limits.memory`.

## Certificate Not Ready

TLS certificate is not being issued.

**Check cert-manager:**
```bash
kubectl get certificate -n kup6s-pages
kubectl describe certificate my-website-tls -n kup6s-pages
```

**Common causes:**
- DNS not propagated yet
- Let's Encrypt rate limits
- ClusterIssuer misconfigured
- HTTP-01 challenge failing (check Traefik ingress)

## 404 Errors

Site shows 404 for all pages.

**1. Verify the site directory exists:**
```bash
kubectl exec -n kup6s-pages deploy/pages-syncer -- ls -la /sites/
```

**2. Check if the repo was cloned successfully:**
```bash
kubectl get staticsite my-website -n pages -o yaml
```

**3. Check the StaticSite status:**
```bash
kubectl describe staticsite my-website -n pages
```

**Common causes:**
- Repo not yet cloned (check `status.phase`)
- Wrong `path` configured (subpath doesn't exist in repo)
- nginx not running or not mounting PVC

## Private Repo Authentication Fails

Syncing fails with "authentication required" or "permission denied".

**1. Verify the RBAC is set up:**
```bash
kubectl get rolebinding pages-syncer-secrets -n pages
```
If missing, see [Private Repositories]({{< relref "/usage/private-repos" >}}) for RBAC setup.

**2. Verify the Secret exists and has the correct key:**
```bash
kubectl get secret my-repo-token -n pages -o yaml
```

**3. Test the token manually:**
```bash
git clone https://git:YOUR_TOKEN@forgejo.example.com/org/repo.git
```

## Force Re-sync

Trigger a manual sync using the site's sync token:

```bash
# Get the sync token from the StaticSite status
TOKEN=$(kubectl get staticsite my-website -n pages -o jsonpath='{.status.syncToken}')

# Trigger sync with authentication
curl -H "X-API-Key: $TOKEN" -X POST https://webhook.pages.example.com/sync/pages/my-website
```

## Webhook Not Triggering

Pushes to the repository don't trigger updates.

**1. Check the syncer logs:**
```bash
kubectl logs -n kup6s-pages -l app=pages-syncer
```

**2. Verify webhook IngressRoute exists:**
```bash
kubectl get ingressroute -n kup6s-pages
```

**3. Verify webhook secret matches:**
The secret configured in Helm values must match the secret configured in your Git provider.

**4. Test manually:**
```bash
curl -v https://webhook.pages.example.com/health
```

## Pods Not Starting

Components fail to start or crash loop.

**Check pod status:**
```bash
kubectl get pods -n kup6s-pages
kubectl describe pod <pod-name> -n kup6s-pages
```

**Common causes:**
- Image pull errors (check `imagePullSecrets`)
- PVC not bound (check `storage.storageClassName`)
- Resource limits too low
- Security context issues (non-root user, read-only filesystem)

## View All Resources

List all resources created by kup6s-pages:

```bash
# StaticSites
kubectl get staticsites -A

# Generated Traefik resources
kubectl get ingressroute,middleware -n kup6s-pages

# Certificates
kubectl get certificate -n kup6s-pages

# Core components
kubectl get deploy,svc,pvc -n kup6s-pages
```
