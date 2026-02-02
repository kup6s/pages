---
title: Webhooks
weight: 40
---

# Webhooks

For instant deployments on push, configure webhooks in your Git provider.

## Enable Webhook Ingress

Enable webhooks in your Helm values. **A webhook secret is required** for HMAC signature validation:

```yaml
webhook:
  enabled: true
  domain: "webhook.pages.example.com"
  clusterIssuer: "letsencrypt-prod"
  # Required: set a secret for HMAC validation
  secret: "your-webhook-secret-here"
  # Or reference an existing secret:
  # secretRef:
  #   name: "my-webhook-secret"
  #   key: "webhook-secret"
```

This creates the IngressRoute and Certificate automatically. The same secret must be configured in your Git provider's webhook settings.

## Webhook Endpoints

| Provider | URL |
|----------|-----|
| Forgejo/Gitea | `https://webhook.pages.example.com/webhook/forgejo` |
| GitHub | `https://webhook.pages.example.com/webhook/github` |
| Manual sync | `POST /sync/{namespace}/{name}` (requires `X-API-Key` header) |

## Configure in Forgejo/Gitea

1. Go to **Repository → Settings → Webhooks → Add Webhook**
2. **URL**: `https://webhook.pages.example.com/webhook/forgejo`
3. **Content Type**: `application/json`
4. **Secret**: Use the same secret configured in `webhook.secret`
5. **Events**: Push events

## Configure in GitHub

1. Go to **Repository → Settings → Webhooks → Add webhook**
2. **Payload URL**: `https://webhook.pages.example.com/webhook/github`
3. **Content type**: `application/json`
4. **Secret**: Use the same secret configured in `webhook.secret`
5. **Events**: Just the push event

## Manual Sync

Trigger a manual sync using the site's sync token:

```bash
# Get the sync token from the StaticSite status
TOKEN=$(kubectl get staticsite my-website -n pages -o jsonpath='{.status.syncToken}')

# Trigger sync with authentication
curl -H "X-API-Key: $TOKEN" -X POST https://webhook.pages.example.com/sync/pages/my-website
```

## Troubleshooting

**Webhook not triggering:**

1. Check the syncer logs:
   ```bash
   kubectl logs -n kup6s-pages -l app=pages-syncer
   ```

2. Verify the webhook secret matches in both Helm values and Git provider settings

3. Check the IngressRoute exists:
   ```bash
   kubectl get ingressroute -n kup6s-pages
   ```

4. Test manually with curl to verify connectivity
