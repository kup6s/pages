---
title: Architecture
weight: 50
---

# Architecture

Technical overview of the kup6s-pages architecture.

## Problem Statement

Existing solutions for static website hosting on Kubernetes are either inefficient or difficult to integrate:

**Kubero** and similar PaaS solutions start one Pod per website. With many small static sites, this leads to significant resource overhead.

**Codeberg pages-server** is efficient (one container for all sites) but handles TLS on its own. This conflicts with the standard Kubernetes pattern (Ingress Controller + cert-manager) and requires SSL passthrough, which prevents Layer-7 features like path routing.

**git-sync as sidecar** only synchronizes one repository per container. For many sites, you'd need many sidecars.

## Design Goals

1. **Resource efficiency**: One nginx Pod serves all sites
2. **Kubernetes-native**: Integration with Traefik IngressController and cert-manager
3. **Declarative**: Sites are defined as Custom Resources
4. **Git-based**: Automatic synchronization from Git repositories
5. **Custom Domains**: Each site can have its own domain
6. **Simple**: Minimal configuration for the end user

## Components

```
┌─────────────────────────────────────────────────────────────────────────┐
│                                                                         │
│  ┌──────────────┐      ┌──────────────┐      ┌──────────────┐          │
│  │ StaticSite   │      │ StaticSite   │      │ StaticSite   │   ...    │
│  │ CRD          │      │ CRD          │      │ CRD          │          │
│  └──────┬───────┘      └──────┬───────┘      └──────┬───────┘          │
│         │                     │                     │                   │
│         └─────────────────────┼─────────────────────┘                   │
│                               │                                         │
│                               ▼                                         │
│                      ┌────────────────┐                                 │
│                      │    Operator    │                                 │
│                      │                │                                 │
│                      │ • Watches CRDs │                                 │
│                      │ • Creates:     │                                 │
│                      │   - Ingress    │                                 │
│                      │   - Middleware │                                 │
│                      │   - Certific.  │                                 │
│                      └────────────────┘                                 │
│                                                                         │
│         ┌──────────────────────┼──────────────────────┐                 │
│         │                      │                      │                 │
│         ▼                      ▼                      ▼                 │
│  ┌────────────┐        ┌─────────────┐        ┌─────────────┐          │
│  │ Traefik    │        │ Traefik     │        │ cert-manager│          │
│  │ Ingress-   │        │ Middleware  │        │ Certificate │          │
│  │ Route      │        │ (addPrefix) │        │             │          │
│  └────────────┘        └─────────────┘        └─────────────┘          │
│                                                                         │
└─────────────────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────────────────┐
│                                                                         │
│                        ┌────────────────┐                               │
│                        │     Syncer     │                               │
│                        │                │                               │
│                        │ • Reads CRDs   │                               │
│                        │ • Git clone/   │                               │
│                        │   pull         │                               │
│                        │ • Webhook API  │                               │
│                        └───────┬────────┘                               │
│                                │                                        │
│                                ▼                                        │
│                     ┌─────────────────────┐                             │
│                     │   PVC: /sites       │                             │
│                     │   ├── site-a/       │                             │
│                     │   ├── site-b/       │                             │
│                     │   └── site-c/       │                             │
│                     └──────────┬──────────┘                             │
│                                │                                        │
│                                ▼                                        │
│                     ┌─────────────────────┐                             │
│                     │   nginx (1 Pod)     │                             │
│                     │   root /sites;      │                             │
│                     └─────────────────────┘                             │
│                                                                         │
└─────────────────────────────────────────────────────────────────────────┘
```

### Operator

Watches StaticSite resources across all namespaces and creates:
- **Traefik IngressRoute** for routing traffic
- **Traefik Middleware** with `addPrefix` for path-based routing
- **cert-manager Certificate** for TLS

All generated resources are created in the system namespace (`kup6s-pages`) for improved security.

### Syncer

Synchronizes Git repositories to the shared PVC:
- Periodically polls all StaticSite CRDs
- Keeps one clone per StaticSite under `/sites/.clones/<namespace>/<name>`, using the git CLI
- Clones are shallow partial clones (`--depth 1 --filter=blob:none`).
  When `spec.path` is set, only that directory is checked out (sparse checkout).
  Ten sites that each serve one directory of a large repository download about the repository's size once, not ten times.
- `/sites/<namespace>/<name>` is always a symlink to the served directory of the clone.
  Paths are unique across namespaces, so sites of the same name in different namespaces never share a checkout.
- Supports private repos via Secrets
- Serves webhooks for instant syncs
- Reports results in the `Synced` condition and as events, and exposes Prometheus metrics on a separate port

### nginx

A single nginx Deployment serves all sites with a static configuration:

```nginx
server {
    listen 80;
    root /sites;
    absolute_redirect off;
    location / {
        try_files $uri $uri/ $uri/index.html =404;
    }
}
```

No dynamic configuration needed - routing is handled by Traefik via addPrefix.

A directory requested without a trailing slash (`/2019`, `/docs/guide`) is redirected to the same public URL with a slash.
nginx's built-in redirect would expose the internal `/<namespace>/<name>` path that Traefik added, so the configuration builds the target itself: the prefix that Traefik's `stripPrefix` reported in `X-Forwarded-Prefix`, followed by the request path without the `/<namespace>/<name>` segments.
Only plain path segments are accepted from that header, so a client cannot turn the redirect into one to another host.
The redirect is relative, so the browser keeps the public scheme and host.

nginx reads its configuration only at start.
The nginx pods carry a checksum of it (`checksum/config`), so a chart upgrade that changes the configuration, or `nginx.customConfig`, rolls them.

The nginx replicas run on different nodes, so that losing one node does not take every site down.
A topology spread constraint (`nginx.topologySpreadConstraints`) lets the number of replicas per node differ by at most one, and the scheduler refuses any node that would break this.
The constraint counts only the pods of one revision (`matchLabelKeys: pod-template-hash`), so a rolling update spreads the new pods among themselves.
Counted together with the old pods, replicas that share a node move to the other node together on every rollout, and a preferred pod anti-affinity alone behaves the same way.
Nodes the pod cannot run on are left out of the count (`nodeTaintsPolicy: Honor`): cordoned nodes, nodes that are not ready, and nodes with a taint the pod does not tolerate.
On a single-node cluster, or while only one node is schedulable, the replicas therefore share that node instead of one of them staying `Pending`.

The scheduler applies the constraint only when it places a pod, and Kubernetes does not move running pods.
After a node drain or outage the replicas share the remaining node and stay there until the next rollout.
If the cluster always has two or more nodes for nginx, add `minDomains: 2` to the constraint (Kubernetes 1.28 or later).
A replica that lost its node then stays `Pending` until a second node is schedulable, so the replicas are spread again after every drain or reboot, while the other replica keeps serving.
Do not set it on a single-node cluster, where the second replica would never start.

A terminating nginx pod keeps serving for a few seconds before nginx stops (`nginx.preStopSleepSeconds`, default 5).
When Kubernetes deletes a pod, for a rollout, a drain, or an eviction, it removes the pod from the Service endpoints and starts the termination at the same time.
The ingress controller learns about the removed endpoint a moment later.
Without the pause nginx would already be gone, and a request routed to the old address would hang until the controller's dial timeout and end in `504 Gateway Timeout`.
The pause runs `sleep` in the nginx image; nginx then shuts down gracefully on the image's stop signal `SIGQUIT` and finishes the requests it has accepted.
Pause and shutdown share the termination grace period of 30 seconds, so keep the pause well below it.

## Request Flow

```
HTTPS Request: www.customer.com/about.html
         │
         │ 1. TLS Termination (Traefik)
         ▼
┌─────────────────────────────────────────────────────┐
│  Traefik                                            │
│                                                     │
│  Route Match: Host(`www.customer.com`)              │
│  Middleware:  addPrefix(/customer/website)          │
│                                                     │
│  Internal Request: /customer/website/about.html     │
└────────────────────────┬────────────────────────────┘
                         │
                         │ 2. HTTP to nginx Service
                         ▼
┌─────────────────────────────────────────────────────┐
│  nginx                                              │
│                                                     │
│  root /sites;                                       │
│  Request: /customer/website/about.html              │
│  Served:  /sites/customer/website/about.html        │
└────────────────────────┬────────────────────────────┘
                         │
                         │ 3. File from PVC
                         ▼
┌─────────────────────────────────────────────────────┐
│  PVC: /sites                                        │
│                                                     │
│  /sites/customer/website/                           │
│  ├── index.html                                     │
│  ├── about.html  ◄── This file                      │
│  └── assets/                                        │
└─────────────────────────────────────────────────────┘
```

## Sync Flow

### Periodic Sync

For every StaticSite, at each interval:

1. Validate `repo` and `path`, and read the credentials from `secretRef`.
2. Skip the site with `Synced=False/DiskFull` if the sites volume has less than `--min-free-percent` free.
3. If `/sites/.clones/<namespace>/<name>` is a complete clone of the configured repo, branch and path, run `git fetch --depth 1` and `git reset --hard`.
   A failed fetch keeps the current content online.
   A failed reset triggers a fresh clone.
4. Otherwise, clone into `/sites/.clones/<namespace>/.tmp-<name>-<random>`, swap it into place, and point `/sites/<namespace>/<name>` at it with an atomic rename.
   An interrupted clone never replaces a working site.
5. Replace the clone with a fresh one when `.git` grows beyond twice the checkout plus 50 MiB.
   Git does not prune old objects of partial clones.
6. Write `Synced`, `lastSync`, `lastCommit` and `contentPath`.

### Site paths

Traefik's `addPrefix` middleware puts `/<namespace>/<name>` in front of every request, and `status.internalURL` ends in the same path.
The operator takes this path from `status.contentPath`, which the syncer writes once the site is linked there.
While `contentPath` is empty, and for any value other than the site's own path, the operator keeps the path `/<name>` of the layout before 1.0.0-rc.7.

### Moving from the layout before 1.0.0-rc.7

Earlier versions kept every site under `/sites/<name>` and `/sites/.repos/<name>`, so two sites of the same name in different namespaces shared one directory.
At its start, before the first sync, the syncer moves each legacy clone to `.clones/<namespace>/<name>`:

1. It moves the clone only to the site whose repository, branch and path it was made for.
   Another site of the same name gets a fresh clone from the next sync.
2. The legacy link `/sites/<name>` is pointed into the moved clone, so the site keeps serving under its old path.
3. The syncer links `/sites/<namespace>/<name>` and writes `status.contentPath`.
4. The operator switches the site's `addPrefix` and `internalURL` within seconds.
5. Cleanup removes `/sites/<name>` and `/sites/.repos/<name>` once every site of that name has its `contentPath`, and `.repos` once it is empty.

No site is cloned again or taken offline.
While a site still serves under `/<name>`, nginx already strips two segments for the redirect of a directory requested without a trailing slash, so such a redirect lands on the site root.
This lasts from the nginx rollout until the syncer has moved the site, seconds to minutes.

A site named like a namespace keeps its legacy link `/sites/<namespace>` until it has moved.
Sites in that namespace report `Synced=False` with reason `LegacyPathInUse` until cleanup has removed the link, usually within two sync intervals.

### Webhook Sync (Instant)

```
┌──────────────┐     ┌──────────────┐     ┌──────────────┐
│   Forgejo    │     │   Traefik    │     │   Syncer     │
│              │     │              │     │              │
│  git push    │────▶│  webhook.    │────▶│  POST        │
│              │     │  pages.      │     │  /webhook/   │
│              │     │  kup6s.io    │     │  forgejo     │
└──────────────┘     └──────────────┘     └──────┬───────┘
                                                 │
       ┌─────────────────────────────────────────┘
       │
       ▼
┌──────────────────────────────────────────────────────────┐
│                                                          │
│  1. Parse Webhook Payload (repo URL, branch)             │
│  2. Find all StaticSites with this repo URL              │
│  3. Sync each matching site (fetch + reset or re-clone)  │
│  4. Status Update                                        │
│                                                          │
└──────────────────────────────────────────────────────────┘
```

## Resource Efficiency

| Approach | 100 Sites | 1000 Sites |
|----------|-----------|------------|
| Pod per Site | 100 Pods | 1000 Pods |
| kup6s-pages | 3 Pods | 3 Pods |

The three Pods are: Operator (1), Syncer (1), nginx (1-2 for HA).

### No Dynamic nginx Configuration

The addPrefix pattern eliminates the need to reconfigure nginx for each new site:

- No ConfigMap updates
- No nginx reload
- No race conditions

## Security Model

- **Operator**: ClusterRole for watching StaticSites, but only creates resources in the system namespace
- **Syncer**: No cluster-wide secret access by default (opt-in via namespace Roles)
- **SSRF Protection**: Mandatory `--allowed-hosts` flag restricts which Git hosts can be accessed
- **Pod Security**: All pods run as non-root with read-only root filesystem

See [Security]({{< relref "/security" >}}) for detailed RBAC documentation.

## Deployment Overview

```
Namespace: kup6s-pages (System)
├── Deployment: pages-operator
│   └── Pod: operator
├── Deployment: pages-syncer
│   └── Pod: syncer
├── Deployment: static-sites-nginx
│   └── Pod: nginx (replicas: 2)
├── Service: static-sites-nginx
├── Service: pages-syncer (for webhooks)
├── PVC: static-sites-data
├── ConfigMap: nginx-config
├── ServiceAccounts + RBAC
│
│ Generated resources (all in system namespace):
├── IngressRoute: pages--customer-a-website (generated)
├── IngressRoute: pages--customer-b-docs (generated)
├── Middleware: pages--customer-a-website-prefix (generated)
├── Middleware: pages--customer-b-docs-prefix (generated)
├── Certificate: www-customer-a-com-tls (generated)
└── Certificate: docs-customer-b-com-tls (generated)

Namespace: pages (User Sites)
├── StaticSite: customer-a-website
├── StaticSite: customer-b-docs
└── Secret: git-credentials (optional)
```

Note: All generated Traefik and cert-manager resources are created in the system
namespace for improved security. Users can see resource references via `status.resources`.

## Limitations

1. **RWX Storage required**: The PVC must support ReadWriteMany (e.g., Longhorn, NFS, CephFS)
2. **No Build Pipeline**: Only serves static files (build in CI/CD)
3. **No Preview Deployments**: Each StaticSite is a fixed configuration
4. **Single Point of Sync**: The Syncer is a single Pod
5. **Reset in place**: Updates reset the served checkout in place.
   During the reset, nginx can serve a mix of old and new files (#157).
6. **Name collisions**: Site directories are keyed by name only.
   Two StaticSites with the same name in different namespaces collide (#158).
7. **No partial-clone support**: Git servers without partial-clone support receive `--filter` and ignore it.
   The syncer then downloads the full repository.
   If such a site serves a small `path` of a very large repository, the clone can exceed the growth threshold right after cloning.
   The syncer then clones the site fresh on every new commit.

## Future Extensions

- **Preview Deployments**: Automatic sites for Pull Requests
- **Build Integration**: Optional build container before sync
- **UI**: Web dashboard for site management
- **Multi-Cluster**: Sync to multiple clusters
