---
title: Private Sites
weight: 35
---

# Private Sites

A private site is synced and served like any other site, but gets no public route.
Use it when the site should only be reachable through your own proxy, for example a VPN or a second ingress.

```yaml
apiVersion: pages.kup6s.com/v1beta1
kind: StaticSite
metadata:
  name: cluster-docs
  namespace: pages
spec:
  repo: https://github.com/example/cluster-docs.git
  visibility: Private
```

The operator creates no IngressRoute, no Middleware and no Certificate, and the site gets no auto-generated domain.
The syncer keeps the content up to date as usual.

A private site cannot set `domain` or `pathPrefix`; the API server rejects it.

## Reaching the site

The status shows the in-cluster address:

```bash
kubectl get staticsite cluster-docs -n pages -o jsonpath='{.status.internalURL}'
# http://kup6s-pages-nginx.kup6s-pages.svc/cluster-docs/
```

`status.url` stays empty.

Point the root of your proxy at `internalURL`, so `/` of the proxy maps to `/cluster-docs/` on nginx, the way Traefik does it for public sites.
Redirects from nginx (a directory requested without a trailing slash) are relative to the site root.
If your proxy serves the site under a sub-path, send that sub-path in an `X-Forwarded-Prefix` header and nginx puts it in front of its redirects.

How you expose the site (Tailscale, another ingress class, `kubectl port-forward`) is up to you; the operator does not create anything for it.

## Who can read a private site

Private only means "no route at the public ingress".
Any pod that can reach the nginx Service can read the site.
See [Security]({{< relref "/security#private-sites" >}}) for how to restrict that.

## Switching

Setting an existing site to `Private` removes its IngressRoute and Middlewares, and its Certificate unless another site still uses that domain.
Remove `domain` and `pathPrefix` in the same change:

```bash
kubectl patch staticsite my-site -n pages --type merge \
  -p '{"spec":{"visibility":"Private","domain":null,"pathPrefix":null}}'
```

The synced content stays.
Setting it back to `Public` creates the routing objects again.
