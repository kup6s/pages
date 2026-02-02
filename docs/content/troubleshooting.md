---
title: Troubleshooting
weight: 40
---

# Troubleshooting

Common issues and solutions.

## Site Stuck in "Pending"

The site was created but never transitions to "Ready".

**Check the Syncer logs:**
```bash
kubectl logs -n kup6s-pages -l app=pages-syncer
```

**Common causes:**
- Git host not in `allowedHosts` (SSRF protection)
- Repository URL invalid or inaccessible
- Network issues reaching the Git host

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
