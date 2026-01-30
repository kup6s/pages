# kup6s-pages

Kubernetes Operator für statisches Website-Hosting à la GitHub Pages.

## Konzept

```
┌──────────────────────────────────────────────────────────────────────────┐
│                           Request Flow                                    │
├──────────────────────────────────────────────────────────────────────────┤
│                                                                          │
│   https://www.kunde.at/about.html                                        │
│            │                                                             │
│            ▼                                                             │
│   ┌─────────────────┐                                                    │
│   │     Traefik     │  Host(`www.kunde.at`) matched                      │
│   │                 │  Middleware: addPrefix(/kunde-website)             │
│   └────────┬────────┘                                                    │
│            │  /kunde-website/about.html                                  │
│            ▼                                                             │
│   ┌─────────────────┐                                                    │
│   │  nginx (1 Pod)  │  root /sites;                                      │
│   │                 │  serves /sites/kunde-website/about.html            │
│   └────────┬────────┘                                                    │
│            │                                                             │
│            ▼                                                             │
│   ┌─────────────────────────────────────┐                                │
│   │  PVC: /sites                        │                                │
│   │  ├── kunde-website/   ← aus repo    │                                │
│   │  ├── user-blog/                     │                                │
│   │  └── docs-site/                     │                                │
│   └─────────────────────────────────────┘                                │
│                                                                          │
└──────────────────────────────────────────────────────────────────────────┘
```

## Features

- **1 Pod für alle Sites** - kein Pod-Overhead pro Website
- **CRD-basiert** - deklarative Konfiguration
- **Traefik Integration** - IngressRoute + addPrefix Middleware
- **cert-manager Integration** - automatische TLS Zertifikate
- **Git-basiert** - Pull aus Forgejo/GitLab/GitHub
- **Webhook Support** - Instant Updates bei Push

## Quick Start

```bash
# CRD + Operator + nginx deployen
kubectl apply -f deploy/

# Erste Site anlegen
kubectl apply -f - <<EOF
apiVersion: pages.kup6s.io/v1alpha1
kind: StaticSite
metadata:
  name: meine-website
  namespace: pages
spec:
  repo: https://forgejo.kup6s.io/user/website.git
  domain: www.example.com
EOF

# Status checken
kubectl get staticsites
```

## Lizenz

EUPL-1.2
