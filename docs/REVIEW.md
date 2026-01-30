# Critical Review: kup6s-pages Concept

## Concept Strengths

### 1. Architecture is Clever
The addPrefix pattern with Traefik Middleware is an elegant solution - no dynamic nginx configuration needed.

### 2. Resource Efficiency
One nginx Pod for all sites is a major advantage over Pod-per-site solutions.

| Approach | 100 Sites | 1000 Sites |
|----------|-----------|------------|
| Pod per Site | 100 Pods | 1000 Pods |
| kup6s-pages | 3 Pods | 3 Pods |

### 3. Kubernetes-native Integration
- CRDs with status subresource
- cert-manager for automatic TLS certificates
- Traefik IngressRoutes for routing
- Owner References for automatic cleanup

### 4. Separation of Concerns
Operator and Syncer are cleanly separated - Operator manages Kubernetes resources, Syncer manages Git sync.

---

## Critical Issues

### 1. `spec.path` is not implemented

In `pkg/syncer/git.go:169-175` `setupSubpath` is called, but the function does nothing:

```go
func (s *Syncer) setupSubpath(repoDir, subpath string) error {
    return nil  // <- Does nothing!
}
```

**Problem**: If someone has a repo with build output in `/dist`, serving won't work. The addPrefix only adds the site name, not the subpath.

**Solution needed**: Either create symlinks or nginx location rewrite.

### 2. Import Path Mismatch

- `go.mod`: `module github.com/kup6s/pages`
- Code imports: `github.com/kleinundpartner/kup6s-pages/...`

This will fail immediately at build time.

### 3. DeepCopy Generation Missing

The kubebuilder markers are there (`+k8s:deepcopy-gen`), but there's no `zz_generated.deepcopy.go`. Without this, scheme registration will fail - controller-runtime expects DeepCopy methods.

### 4. Patch Type is Wrong

In `pkg/syncer/git.go:226`:
```go
Patch(ctx, site.Name, "application/merge-patch+json", ...)
```

Should be `types.MergePatchType` (a `types.PatchType`, not a string).

### 5. No Cleanup Logic for Deleted Sites

In `pkg/controller/staticsite.go:311-312` there's a TODO comment:
```go
// Here we could trigger the Syncer to delete /sites/<n>/
```

The files on the PVC are never deleted - storage leak over time.

### 6. `syncInterval` per Site is Ignored

`spec.syncInterval` is defined in the CRD, but the Syncer only uses the global `DefaultInterval`. All sites are synced with the same interval.

### 7. Wildcard Certificate Assumption

For sites without a custom domain, `pkg/controller/staticsite.go:191` references `pages-wildcard-tls`, but it's not created anywhere. The concept doesn't explicitly mention this as a prerequisite.

---

## Security Vulnerabilities

### SSRF Risk
The Syncer accepts arbitrary repo URLs without validation:
- `file://` URLs possible
- Internal network IPs reachable
- No allowlist for Git hosts

### Secret Access Too Broad
Syncer needs cluster-wide secret read permissions since it must read secrets from user namespaces. This is security-critical.

### Webhook Secret Not Validated
`WebhookSecret` is defined in the code but never validated - anyone can trigger webhooks and cause DoS.

---

## Minor Issues

- No health probes in Operator deployment (only in Syncer)
- `error_page 404 /404.html` in nginx references a file that would need to be in the `/sites` root, not per site
- `runAsUser: 1000` in Syncer - git operations need write permissions on PVC
- Shallow Clone + Pull: `Depth: 1` on pull can cause problems with larger branch divergences

---

## Conclusion

The concept is **architecturally solid** and the addPrefix pattern is clever.

---

## Completed Fixes

The following issues have been fixed:

- [x] Import paths corrected (`github.com/kup6s/pages`)
- [x] DeepCopy methods generated (`zz_generated.deepcopy.go`)
- [x] Patch type corrected (`types.MergePatchType`)
- [x] `spec.path` implemented (symlink approach: repos in `.repos/<name>`, symlink to `<name>`)
- [x] Cleanup logic for deleted sites (periodic + DELETE endpoint)
- [x] Webhook secret validation (HMAC-SHA256 for GitHub/Forgejo)
- [x] SSRF protection (Git host allowlist with `--allowed-hosts` flag)
- [x] controller-runtime v0.18 API compatibility

### Remaining Issues

- `syncInterval` per site is not yet individually considered
- Health probes in Operator deployment
- Secret scoping (Syncer currently needs cluster-wide secret read permissions)
