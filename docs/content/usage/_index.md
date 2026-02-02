---
title: Usage
weight: 20
bookCollapseSection: true
---

# Usage

This section covers how to deploy static websites using kup6s-pages.

## Basic Example

Create a `StaticSite` resource to deploy a website from a Git repository:

```yaml
apiVersion: pages.kup6s.com/v1alpha1
kind: StaticSite
metadata:
  name: my-website
  namespace: pages
spec:
  repo: https://github.com/user/my-website.git
  domain: www.example.com
```

The operator will:
1. Create a Traefik Middleware (`my-website-prefix`) with `addPrefix: /my-website`
2. Create a Traefik IngressRoute for `Host(`www.example.com`)`
3. Create a cert-manager Certificate for the domain
4. The Syncer clones the repo to `/sites/my-website/`

## Check Status

```bash
# List all sites
kubectl get staticsites -A

# Detailed status
kubectl describe staticsite my-website -n pages

# Short form
kubectl get ss -n pages
```

## Auto-Generated Domain

If no `domain` is specified, the site gets a subdomain of the configured pages domain:

```yaml
apiVersion: pages.kup6s.com/v1alpha1
kind: StaticSite
metadata:
  name: my-project
  namespace: pages
spec:
  repo: https://github.com/user/my-project.git
  # No domain specified → https://my-project.pages.kup6s.com
```

## Build Output Subpath

For sites with build tools (Vite, Hugo, Sphinx, etc.) where the output is in a subdirectory:

```yaml
apiVersion: pages.kup6s.com/v1alpha1
kind: StaticSite
metadata:
  name: docs
  namespace: pages
spec:
  repo: https://github.com/user/docs.git
  branch: main
  path: /dist          # Serve only the /dist directory
  domain: docs.example.com
```

The Syncer clones to `/sites/.repos/docs/` and creates a symlink `/sites/docs/` → `/sites/.repos/docs/dist/`.
