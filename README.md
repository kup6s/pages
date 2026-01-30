# kup6s-pages

Kubernetes Operator for static website hosting à la GitHub Pages.

## Concept

```
┌──────────────────────────────────────────────────────────────────────────┐
│                           Request Flow                                   │
├──────────────────────────────────────────────────────────────────────────┤
│                                                                          │
│   https://www.customer.com/about.html                                    │
│            │                                                             │
│            ▼                                                             │
│   ┌─────────────────┐                                                    │
│   │     Traefik     │  Host(`www.customer.com`) matched                  │
│   │                 │  Middleware: addPrefix(/customer-website)          │
│   └────────┬────────┘                                                    │
│            │  /customer-website/about.html                               │
│            ▼                                                             │
│   ┌─────────────────┐                                                    │
│   │  nginx (1 Pod)  │  root /sites;                                      │
│   │                 │  serves /sites/customer-website/about.html         │
│   └────────┬────────┘                                                    │
│            │                                                             │
│            ▼                                                             │
│   ┌─────────────────────────────────────┐                                │
│   │  PVC: /sites                        │                                │
│   │  ├── customer-website/ ← from repo  │                                │
│   │  ├── user-blog/                     │                                │
│   │  └── docs-site/                     │                                │
│   └─────────────────────────────────────┘                                │
│                                                                          │
└──────────────────────────────────────────────────────────────────────────┘
```

## Features

- **1 Pod for all sites** - no Pod overhead per website
- **CRD-based** - declarative configuration
- **Traefik Integration** - IngressRoute + addPrefix Middleware
- **cert-manager Integration** - automatic TLS certificates
- **Git-based** - pull from Forgejo/GitLab/GitHub
- **Webhook Support** - instant updates on push

## Quick Start

```bash
# Deploy CRD + Operator + nginx
kubectl apply -f deploy/

# Create first site
kubectl apply -f - <<EOF
apiVersion: pages.kup6s.io/v1alpha1
kind: StaticSite
metadata:
  name: my-website
  namespace: pages
spec:
  repo: https://forgejo.kup6s.io/user/website.git
  domain: www.example.com
EOF

# Check status
kubectl get staticsites
```

## License

EUPL-1.2
