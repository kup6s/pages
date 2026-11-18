---
title: Installation
weight: 10
---

# Installation

## Via Helm (Recommended)

```bash
# Install from OCI registry
helm install pages oci://ghcr.io/kup6s/kup6s-pages --version 0.1.0

# Or with custom configuration
helm install pages oci://ghcr.io/kup6s/kup6s-pages \
  --set operator.pagesDomain=pages.example.com \
  --set operator.clusterIssuer=letsencrypt-prod \
  --set 'syncer.allowedHosts={github.com,gitlab.com}' \
  --set storage.storageClassName=longhorn \
  --set webhook.enabled=true \
  --set webhook.domain=webhook.pages.example.com
```

## Key Helm Values

| Value | Default | Description |
|-------|---------|-------------|
| `operator.pagesDomain` | `pages.kup6s.com` | Base domain for auto-generated URLs |
| `operator.clusterIssuer` | `letsencrypt-prod` | cert-manager ClusterIssuer |
| `syncer.allowedHosts` | **Required** | Allowed Git hosts (SSRF protection) |
| `storage.size` | `10Gi` | PVC size for sites |
| `storage.storageClassName` | (default) | StorageClass (must support RWX) |
| `nginx.replicas` | `2` | nginx replicas for HA |
| `webhook.enabled` | `false` | Enable webhook IngressRoute |
| `webhook.domain` | `webhook.pages.kup6s.com` | Webhook endpoint domain |

See [Helm Values Reference]({{< relref "/reference/helm-values" >}}) for all options.

## Verify Installation

```bash
# Check operator and syncer are running
kubectl get pods -n kup6s-pages

# Check CRD is registered
kubectl get crd staticsites.pages.kup6s.com
```

Expected output:

```
NAME                                      READY   STATUS    RESTARTS   AGE
kup6s-pages-operator-xxx                  1/1     Running   0          1m
kup6s-pages-syncer-xxx                    1/1     Running   0          1m
kup6s-pages-nginx-xxx                     1/1     Running   0          1m
kup6s-pages-nginx-yyy                     1/1     Running   0          1m
```

## Upgrading the CRD

Helm installs the CRD from the chart's `crds/` directory only on the first install; `helm upgrade` leaves it as it is.
Apply it yourself before upgrading the chart:

```bash
helm show crds oci://ghcr.io/kup6s/kup6s-pages --version <version> | kubectl apply --server-side -f -
```

With an older CRD the API server does not know new fields.
`kubectl apply` rejects them, but a client that does not ask for strict field validation gets them dropped with at most a warning.
A site set to `visibility: Private` that way stays public.

## Upgrading to per-namespace site paths (#158)

From 1.0.0-rc.7 on, each site is served from `/<namespace>/<name>` instead of `/<name>`, so sites of the same name in different namespaces no longer share a directory.

1. Apply the CRD of the new version first, see [Upgrading the CRD](#upgrading-the-crd).
   It adds `status.contentPath`.
   Without it the API server drops the field, every site stays on its legacy path, and nothing breaks, but nothing moves either.
2. Upgrade the chart.
   The syncer moves the existing clones at its start, without cloning again, and every site keeps serving while it moves.
   A site named like its own namespace is the exception: it answers `404` under its old path for the seconds until the operator has switched it.
   With 1.0.0-rc.7 such a site did not move at all and stopped syncing (#192); upgrading to 1.0.0-rc.8 moves it.
   See [Moving from the layout before 1.0.0-rc.7]({{< relref "/architecture#moving-from-the-layout-before-100-rc7" >}}).
3. Update proxies in front of private sites that use a fixed path instead of `status.internalURL`.
   The internal address changes from `http://<nginx>/<name>/` to `http://<nginx>/<namespace>/<name>/`.

To roll back, scale the syncer to 0 and roll back the chart.
The previous syncer cannot use `.clones` and clones every site again.

## Upgrading to the git CLI syncer (#153)

The syncer now runs the `git` command line with sparse, blobless clones instead of go-git.
Read these notes before upgrading.

### Repository URLs must not redirect

The syncer does not follow HTTP redirects, because a redirect would carry credentials to another host.
A StaticSite whose repository URL redirects (a renamed project, `http` to `https`, a server that redirects URLs without `.git`) goes to `Synced=False` with reason `CloneFailed`.
It keeps serving its last content.

Check every site before upgrading:

```bash
kubectl get staticsites -A -o jsonpath='{range .items[*]}{.metadata.namespace}/{.metadata.name} {.spec.repo}{"\n"}{end}'
```

Then, for each URL:

```bash
curl -s -o /dev/null -w '%{http_code}\n' "<repo>/info/refs?service=git-upload-pack"
```

A `3xx` answer means `spec.repo` must be changed to the final URL.
Private repositories may answer `401`, which is fine.

### The first sync re-clones every site

Existing clones carry no marker, so the first sync after the upgrade replaces each one.
Site directories with `path: /` become symlinks to the clone.
Expect a burst of clones, and check the free space of the sites volume.

### Rolling back

The previous syncer cannot use the new layout.
To roll back:

1. Scale the syncer to 0.
2. Delete `/sites/.repos` and the site symlinks on the volume.
3. Roll back the chart.

The old syncer clones every site again.

