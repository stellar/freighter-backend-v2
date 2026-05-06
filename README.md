# freighter-backend-v2
Freighter's next generation of backend system written in Go

## Releases

Releases follow a two-step prerelease → promote flow. Both steps run in GitHub Actions; you trigger each with `gh workflow run`.

### 1. Cut a prerelease

```bash
gh workflow run publish-prerelease.yml \
  -f version=v1.2.3-rc.1 \
  -f target_ref=main
```

This builds a Docker image from the chosen ref and pushes it to:

```
746476062914.dkr.ecr.us-east-1.amazonaws.com/stg/freighter-backend-v2:v1.2.3-rc.1
```

It also creates a GitHub prerelease at the same commit.

### 2. Test the prerelease in staging

Deploy `stg/freighter-backend-v2:v1.2.3-rc.1` to your staging cluster and validate.

### 3. Promote to a release

```bash
gh workflow run promote-release.yml \
  -f prerelease_tag=v1.2.3-rc.1 \
  -f release_version=v1.2.3
```

This re-tags the existing staging image into the production ECR namespace (preserving the image digest — no rebuild) and creates a non-prerelease GitHub release:

```
746476062914.dkr.ecr.us-east-1.amazonaws.com/prd/freighter-backend-v2:v1.2.3
```

### Tag format

- Releases: `vMAJOR.MINOR.PATCH` (e.g., `v1.2.3`)
- Prereleases: `vMAJOR.MINOR.PATCH-rc.N` (e.g., `v1.2.3-rc.1`)

The workflows enforce these formats — anything else is rejected before any side effect.

### Hot-fixes / cherry-picked releases

Out of scope for the current setup. All releases are cut from `main`. If you need to release a fix without including newer `main` changes, revert the unwanted commits on `main` first and cut from there.
