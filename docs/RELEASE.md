# Release Process

This document describes how to release kup6s-pages.

## Version Scheme

We follow [Semantic Versioning](https://semver.org/) with pre-release identifiers:

- **Stable**: `1.0.0`, `1.1.0`, `2.0.0`
- **Alpha**: `1.0.0-alpha.1`, `1.0.0-alpha.2` (early testing, API may change)
- **Beta**: `1.0.0-beta.1`, `1.0.0-beta.2` (feature complete, stabilizing)
- **Release Candidate**: `1.0.0-rc.1` (final testing before stable)

## What Gets Released

Each release publishes:

| Artifact | Registry | Example Tag |
|----------|----------|-------------|
| Operator image | `ghcr.io/kup6s/pages-operator` | `v1.0.0-alpha.1`, `1.0.0-alpha.1` |
| Syncer image | `ghcr.io/kup6s/pages-syncer` | `v1.0.0-alpha.1`, `1.0.0-alpha.1` |
| Helm chart | `oci://ghcr.io/kup6s/kup6s-pages` | `1.0.0-alpha.1` |

## Pre-Release Checklist

Before releasing:

1. **All CI checks pass on main**
   ```bash
   # Verify locally
   go test -race ./...
   go build ./...
   helm lint charts/kup6s-pages
   helm unittest charts/kup6s-pages
   ```

2. **Update CHANGELOG.md** (if it exists) with release notes

3. **Verify Chart.yaml** has placeholder version (workflow updates it automatically)
   ```yaml
   version: 0.1.0      # Will be overwritten by release workflow
   appVersion: "0.1.0" # Will be overwritten by release workflow
   ```

## Creating a Release

### Option 1: GitHub CLI (Recommended)

```bash
# For alpha release
gh release create v1.0.0-alpha.1 \
  --title "v1.0.0-alpha.1" \
  --notes "First alpha release of kup6s-pages" \
  --prerelease

# For stable release
gh release create v1.0.0 \
  --title "v1.0.0" \
  --notes "First stable release"
```

### Option 2: GitHub Web UI

1. Go to **Releases** → **Draft a new release**
2. Click **Choose a tag** → type `v1.0.0-alpha.1` → **Create new tag**
3. Set **Release title**: `v1.0.0-alpha.1`
4. Write release notes describing changes
5. Check **Set as a pre-release** for alpha/beta/rc versions
6. Click **Publish release**

## After Release

The [release workflow](.github/workflows/release.yaml) automatically:

1. Runs all tests
2. Builds Docker images with version tags
3. Pushes images to ghcr.io
4. Updates Chart.yaml with release version
5. Packages and pushes Helm chart to OCI registry

Monitor the workflow at: `https://github.com/kup6s/pages/actions`

## Installing a Release

```bash
# Helm chart
helm install pages oci://ghcr.io/kup6s/kup6s-pages --version 1.0.0-alpha.1

# Direct image reference
ghcr.io/kup6s/pages-operator:v1.0.0-alpha.1
ghcr.io/kup6s/pages-syncer:v1.0.0-alpha.1
```

## Troubleshooting

### Release workflow failed

1. Check workflow logs at GitHub Actions
2. Common issues:
   - Tests failing → fix and create new release
   - Registry auth failed → check `GITHUB_TOKEN` permissions
   - Helm push failed → ensure chart name matches repository

### Wrong version released

1. Delete the GitHub release
2. Delete the git tag: `git push --delete origin v1.0.0-alpha.1`
3. Create a new release with correct version

### Yanking a release

To remove a broken release from use:

1. Mark GitHub release as **pre-release** to warn users
2. Add warning to release notes
3. Release a patch version with the fix
