---
title: Custom Domains
weight: 20
---

# Custom Domains

Each StaticSite can have its own custom domain with automatic TLS certificates.

## Basic Custom Domain

```yaml
apiVersion: pages.kup6s.com/v1alpha1
kind: StaticSite
metadata:
  name: company-site
  namespace: pages
spec:
  repo: https://github.com/company/website.git
  domain: www.company.com
```

The operator automatically:
- Creates a Traefik IngressRoute for `Host(`www.company.com`)`
- Creates a cert-manager Certificate
- Configures TLS termination

## DNS Configuration

Point your domain to the cluster's Traefik load balancer IP:

```
www.company.com.  A    203.0.113.10
```

Or use a CNAME if your load balancer has a hostname:

```
www.company.com.  CNAME  lb.cluster.example.com.
```

## Certificate Status

Check if the certificate is ready:

```bash
kubectl get certificate -n kup6s-pages
kubectl describe certificate www-company-com-tls -n kup6s-pages
```

Common issues:
- DNS not propagated yet
- Rate limits from Let's Encrypt
- ClusterIssuer misconfigured

## Multiple Domains

Each StaticSite supports one domain. For multiple domains pointing to the same content, create multiple StaticSite resources:

```yaml
apiVersion: pages.kup6s.com/v1alpha1
kind: StaticSite
metadata:
  name: site-www
  namespace: pages
spec:
  repo: https://github.com/company/website.git
  domain: www.company.com
---
apiVersion: pages.kup6s.com/v1alpha1
kind: StaticSite
metadata:
  name: site-apex
  namespace: pages
spec:
  repo: https://github.com/company/website.git
  domain: company.com
```

Both sites share the same repo but get separate certificates.
