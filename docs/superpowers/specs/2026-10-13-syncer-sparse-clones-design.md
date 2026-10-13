# Syncer: sparse partial clones, recovery, status ownership, observability

Issue: #153. Follow-up: #157 (atomic content swap).

## Goal

The syncer must serve many path-scoped StaticSites from large repositories without being OOM-killed, without filling the shared sites PVC, without crash-looping on interrupted clones, and it must make persistent sync failures visible.

Success criteria:

- Ten StaticSites that each serve one year directory of a 1.1 GB repository sync within the default `256Mi` memory limit, and use about the repository's size on disk in total, not ten times it.
- An interrupted clone never blocks later syncs and never stays on the PVC.
- A failing or stale site shows up in `kubectl get staticsites`, as a Kubernetes event, and as a Prometheus alert, even when the syncer is killed before it can report.
- One site cannot fill the shared volume.

## Current state

- `pkg/syncer/git.go` clones each StaticSite with go-git (`PlainClone`, depth 1) into `/sites/.repos/<name>` (with `path`) or `/sites/<name>` (without). Every site with the same repository downloads the full repository.
- Clones are written directly to their final directory. A killed clone leaves a `.git` without the branch ref. The next sync takes the pull path and fails with `git reset failed: reference not found` forever.
- The syncer writes `phase: Error`, but the controller's `updateFinalStatus` sets `phase: Ready` and `message: Site configured, waiting for sync` on every reconcile and overwrites it.
- The syncer has no metrics and emits no events. The chart ships no PodMonitor or PrometheusRule.
- nginx does not deny `.git`. Sites without `path` are cloned into the served directory, so `/.git/` is reachable.
- `SyncOne` (webhook) and `SyncAll` (loop) can work on the same directory concurrently.

## Spike results

Synthetic repository: 18 year directories, 1.1 GB pack, 4152 files. Client under `MemoryMax=256M` without swap, server outside the cgroup (`git daemon`).

| Variant | Result | Max RSS | Disk |
|---|---|---|---|
| go-git, depth 1 (current) | killed, 1.2 GB partial clone left behind | 285 MB unlimited | 1.2 GB |
| git CLI, depth 1, full | ok, 21 s | 235 MB | 2.1 GB |
| git CLI, partial + sparse `2018` | ok, 0.4 s | 32 MB | 48 MB |
| git CLI, partial + sparse `2005` | ok, 0.9 s | 87 MB | 155 MB |

Updates (`fetch --depth 1` + `reset --hard`) after a normal commit, after a force-push, and after a sparse path change were correct and stayed under 90 MB RSS. The clone persists `remote.origin.partialclonefilter=blob:none`, so later fetches stay partial.

Every update keeps the previous blobs: ten updates of 10 MB grew `.git` from 24 MB to 120 MB. `git gc --prune=now`, also after `reflog expire`, reclaims nothing, because objects in promisor packs are never pruned. `gc` itself peaked at 233 MB RSS. Growth is therefore handled by re-cloning, not by `gc`.

## Decisions

1. **No shared clones.** Each StaticSite keeps its own clone. Sharing an object store (shared clone or git worktrees) couples sites and tenants: a private repository's content could be served to a tenant without credentials, and one broken store breaks every dependent site. Sparse partial clones remove the disk cost that motivated sharing.
2. **git CLI instead of go-git.** The syncer image already contains `git` (Alpine 3.21, git 2.47). go-git is removed from the syncer and its tests.
3. **Partial clone for every site, sparse checkout when `path` is not `/`.** One code path for all sites.
4. **Re-clone instead of gc** when `.git` grows, when `path`, `repo` or `branch` change, and when the local repository is broken.
5. **Old content stays served on failure.** A failed fetch never removes a working site.
6. **The controller is the single writer of `phase` and `message`.** The syncer owns the `Synced` condition, `lastSync` and `lastCommit`.
7. **Per-site state metrics come from the operator,** not the syncer, so they survive syncer crashes.
8. **Atomic content swap is out of scope** (#157). Updates still reset the served checkout in place, as today.
9. **Name collisions across namespaces are out of scope.** `/sites/<name>` stays keyed by name; fixing it changes routing and gets its own issue.

## Design

### 1. git layer (`pkg/syncer/gitcli.go`)

A thin wrapper around the `git` binary. The syncer does no git work itself.

| Operation | Commands |
|---|---|
| `clone(ctx, repo, branch, path, dest, auth)` | `git clone --depth 1 --single-branch --no-tags --filter=blob:none --branch <branch> [--sparse] <repo> <dest>`, then `git -C <dest> sparse-checkout set <path>` when `path` is not `/` |
| `update(ctx, dir, branch, auth)` | `git -C <dir> fetch --depth 1 --no-tags origin +refs/heads/<branch>:refs/remotes/origin/<branch>`, then `git -C <dir> reset --hard refs/remotes/origin/<branch>` |
| `head(ctx, dir)` | `git -C <dir> rev-parse --short=8 HEAD` |

Every invocation runs with a fixed environment:

- `GIT_TERMINAL_PROMPT=0`
- `GIT_CONFIG_NOSYSTEM=1`, `GIT_CONFIG_GLOBAL=/dev/null`
- `GIT_ALLOW_PROTOCOL=http:https`, in addition to `validateRepoURL`
- With credentials: `GIT_CONFIG_COUNT=1`, `GIT_CONFIG_KEY_0=http.<scheme>://<host>/.extraHeader`, `GIT_CONFIG_VALUE_0=Authorization: Basic <base64(user:password)>`

Credentials never appear in argv, `.git/config` or logs, and the URL-scoped key keeps git from sending them to another host. They are passed to every invocation, because `reset` and `sparse-checkout set` fetch missing blobs lazily.

Each invocation gets a context timeout from the new flag `--git-timeout` (default `10m`). Errors include git's stderr, truncated to 2 KiB.

`path` is cleaned and its leading `/` removed. It is rejected when the result starts with `..`. An empty result means the repository root (no sparse checkout).

### 2. Sync flow (`pkg/syncer/git.go`)

Layout, identical for every site:

```
/sites/.repos/<name>/                    partial (and sparse) clone
/sites/<name> -> /sites/.repos/<name>/<path>   symlink, also for path "/"
/sites/.repos/.tmp-<name>-<random>/      clone in progress
/sites/.repos/.old-<name>-<random>/      previous clone during a swap
```

`syncSite` per site:

1. Validate `repo` and `path`.
2. Take the site's mutex (keyed `namespace/name`). Webhook and loop syncs of the same site run one after the other; different sites do not block each other.
3. Check free space (section 4).
4. Decide:
   - **Clone** when any of these is true: `.repos/<name>` is missing; `git rev-parse HEAD` fails; `remote.origin.url` differs from `spec.repo`; the branch in `remote.origin.fetch` differs from `spec.branch`; the sparse path (`git sparse-checkout list`, or none when `core.sparseCheckout` is unset) differs from `spec.path`; `remote.origin.promisor` is not `true` (a clone from the go-git era).
   - **Update** otherwise.
5. Update: run `update`.
   - `fetch` fails: return the error. The existing checkout stays served.
   - `reset` fails after a successful fetch: the local repository is broken; clone in the same run.
   - HEAD changed: compare the size of `.git` with the size of the checkout. When `.git` exceeds twice the checkout plus 50 MiB, clone in the same run.
6. Clone:
   1. Set `Synced=Unknown`, reason `Syncing`.
   2. Run `clone` into `.tmp-<name>-<random>`, guarded by the free-space watchdog. On any error, remove the temp directory and return the error.
   3. Rename `.repos/<name>` to `.old-<name>-<random>` if present, rename the temp directory to `.repos/<name>`.
   4. Point `/sites/<name>` at the new target: create the symlink under a temporary name and `rename` it over the old symlink. If `/sites/<name>` is a directory (layout before this change), remove it first; this one-time migration has a short gap.
   5. Remove `.old-<name>-<random>`.
7. On success set `Synced=True`, `lastSync`, `lastCommit`, and emit `SyncRecovered` if the site was failing before.
8. On failure set `Synced=False` with a reason and the error message, and emit `SyncFailed` if the site was not failing before.

Failure reasons: `CloneFailed`, `FetchFailed`, `AuthFailed` (secret missing or HTTP 401/403), `SubpathMissing`, `DiskFull`, `Timeout`, `InvalidSpec`.

At startup and in every cleanup run, all `.tmp-*` and `.old-*` entries under `.repos` are removed. Cleanup keeps removing `.repos/<name>` and `/sites/<name>` of deleted sites as today.

Migration from the current layout needs no manual step: go-era clones fail the promisor check and are re-cloned on the first sync; broken partial clones are re-cloned; `path: /` directories become symlinks.

### 3. Status ownership (`pkg/controller/staticsite.go`, `pkg/apis/v1beta1`)

New condition type `ConditionSynced = "Synced"` in `pkg/apis/v1beta1/types.go`.

The syncer writes only `conditions[type=Synced]`, `lastSync` and `lastCommit`. `lastSync` becomes the time of the last successful sync. The syncer writes through get, modify, `UpdateStatus` with retry on conflict; a merge patch would replace the whole `conditions` list and drop the controller's conditions.

The controller stops setting `phase: Ready` unconditionally. `updateFinalStatus` derives `phase` and `message`:

| `Synced` condition | `phase` | `message` |
|---|---|---|
| absent | `Pending` | `Waiting for first sync` |
| `Unknown`, reason `Syncing` | `Syncing` | `Syncing since <lastTransitionTime>` |
| `True` | `Ready` | `Synced <lastCommit>` |
| `False` | `Error` | `Sync failed: <reason>: <message>` |

Reconcile errors of the controller keep precedence and set `Error` as today. A status update by the syncer triggers a reconcile, which recomputes `phase`.

New printer column `Synced` (`.status.conditions[?(@.type=="Synced")].status`).

Syncer RBAC: `get` and `update` on `staticsites/status`, `create` and `patch` on `events` (`events.k8s.io`).

### 4. Free-space guard

- Before each clone and update, `statfs` on the sites root. Below `--min-free-percent` (default `10`) the syncer skips the site and sets `Synced=False`, reason `DiskFull`. Cleanup and deletion always run.
- During a clone, a watchdog checks free space every 5 seconds and cancels the clone when it drops below the threshold. The temp directory is removed.
- No per-site size limit; sparse clones bound each site by its subpath.

### 5. Observability

Three layers.

**Status:** the `Synced` condition with reason, message and `lastTransitionTime`. `Syncing` with an old `lastTransitionTime` reveals a sync that died mid-clone.

**Events:** the syncer emits `Warning SyncFailed` when a site starts failing and `Normal SyncRecovered` when it recovers. No event per interval.

**Metrics:**

| Metric | Source | Labels |
|---|---|---|
| `pages_site_synced` (1 when `Synced=True`, 0 otherwise, including absent and `Unknown`) | operator | `namespace`, `name` |
| `pages_site_last_sync_timestamp_seconds` | operator | `namespace`, `name` |
| `pages_syncer_sync_total` | syncer | `result`, `reason` |
| `pages_syncer_git_duration_seconds` (histogram) | syncer | `op` (`clone`, `update`) |
| `pages_syncer_volume_free_bytes` | syncer | none |

The operator computes per-site metrics at scrape time from its informer cache with a custom collector on the controller-runtime registry, so deleted sites leave no stale series. The syncer serves `/metrics` on a new port (`--metrics-addr`, default `:9090`), separate from the webhook port, which is publicly routed.

Chart, both disabled by default:

- `metrics.podMonitor.enabled`: PodMonitors for operator and syncer.
- `metrics.prometheusRule.enabled`: alerts
  - `PagesSiteSyncFailing`: `pages_site_synced == 0` for `metrics.prometheusRule.failingFor` (default 15 minutes). Covers failing sites, sites that never synced, and sites stuck in `Syncing`.
  - `PagesSiteSyncStale`: `time() - pages_site_last_sync_timestamp_seconds > 3 × sync interval` for 15 minutes. Covers sites whose status still says `True` while the syncer no longer runs.
  - `PagesVolumeLow`: free space below `metrics.prometheusRule.volumeLowPercent` (default `15`) for 10 minutes.

A crash-looping syncer is covered by `KubePodCrashLooping` from kube-prometheus-stack.

### 6. nginx, chart, docs

- nginx: `location ~ (^|/)\.git(/|$) { return 404; }`. Other dotfiles such as `.well-known` stay reachable.
- Chart values: `syncer.gitTimeout`, `syncer.minFreePercent`, `syncer.metricsPort`, `metrics.podMonitor.*`, `metrics.prometheusRule.*`. The syncer memory limit stays `256Mi`; the values documentation states that sites with `path: /` and repositories above about 1 GB need more.
- Docs:
  - `architecture.md`: sync flow and directory layout.
  - `troubleshooting.md`: `Synced` reasons, automatic recovery, alerts.
  - `reference/crd.md`: `Synced` condition, new meaning of `lastSync`, printer column.
  - `reference/cli-flags.md`, `reference/helm-values.md`: new flags and values.
  - `usage/path-prefix.md`: sparse clones per site; git servers without partial-clone support fall back to a full clone.
  - `security.md`: credential handling, `.git` denial.

## Testing

- `gitcli`: integration tests against local `file://` repositories with `uploadpack.allowFilter=true`: clone with and without sparse path, update after commit and after force-push, sparse path change, credentials present in the environment and absent from `.git/config` and argv, timeout cancellation.
- Sync flow: clone into temp and swap; interrupted clone leaves no `.tmp-*` and is re-cloned; broken repository is re-cloned; repo, branch and path changes trigger a re-clone; growth threshold triggers a re-clone; fetch failure keeps the old checkout; migration from a go-git clone and from a `path: /` directory; leftover cleanup; per-site mutex.
- Status: controller phase derivation for every row of the table; syncer condition writes keep the controller's conditions; events only on transitions.
- Free-space guard: threshold before clone and update; watchdog cancels a clone.
- Metrics: operator collector output for synced, failing and unknown sites; syncer counters and gauge.
- Chart: helm-unittest for PodMonitors, PrometheusRule and new values; kubeconform as in CI.
- nginx: `.git` path returns 404, `.well-known` does not.

## Risks

- **Git servers without partial clone** ignore `--filter` with a warning and send the full repository. Behaviour stays correct; disk and memory use grow to the full-clone numbers. Documented.
- **Short gap at migration** of `path: /` sites, once per site.
- **Status write volume:** every successful sync writes `lastSync`, as today. Each clone adds one write for `Syncing`.
- **Rollback:** the old syncer image cannot use the new layout for `path: /` sites (symlink instead of directory) and would fail its pull on sparse clones. A rollback requires deleting `/sites/.repos` and the site symlinks; the old syncer then clones everything again.

## Out of scope

- Atomic content swap (#157).
- Name collisions across namespaces (separate issue).
- Enabling PodMonitors and rules on kup6s (dp-infra, after the release).
