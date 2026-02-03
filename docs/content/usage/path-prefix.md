---
title: Path Prefix
weight: 30
---

# Path Prefix

Serve multiple repositories under different paths of the same domain.

## Use Case

You want to serve archived content or versioned documentation under paths like:
- `https://www.example.com/2019/`
- `https://www.example.com/2020/`
- `https://www.example.com/v1/docs/`

## Configuration

```yaml
apiVersion: pages.kup6s.com/v1beta1
kind: StaticSite
metadata:
  name: archive-2019
  namespace: pages
spec:
  repo: https://github.com/org/archive-2019.git
  domain: www.example.com
  pathPrefix: /2019
---
apiVersion: pages.kup6s.com/v1beta1
kind: StaticSite
metadata:
  name: archive-2020
  namespace: pages
spec:
  repo: https://github.com/org/archive-2020.git
  domain: www.example.com
  pathPrefix: /2020
```

## How It Works

- Both sites share the same TLS certificate (one per domain)
- Traefik routes based on path prefix before the host match
- Requests to `https://www.example.com/2019/page.html` serve from `archive-2019` repo
- Requests to `https://www.example.com/2020/page.html` serve from `archive-2020` repo

## Requirements

- `pathPrefix` requires a custom `domain` to be set
- Path prefixes must be unique per domain
- The prefix is stripped before serving (so `/2019/index.html` serves `index.html` from the repo)

## Root Path

To serve a "main" site at the root while having prefixed sub-sites, create a site without `pathPrefix`:

```yaml
apiVersion: pages.kup6s.com/v1beta1
kind: StaticSite
metadata:
  name: main-site
  namespace: pages
spec:
  repo: https://github.com/org/main-website.git
  domain: www.example.com
  # No pathPrefix → serves at root
```

Traefik will route `/2019/*` and `/2020/*` to their respective sites, and everything else to the main site.
