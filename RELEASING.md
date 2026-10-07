# Releasing VegaLoad

A release is a version tag. Pushing the tag runs
`.github/workflows/release.yml`, which uses GoReleaser to:

1. build binaries for Linux, macOS and Windows (amd64 and arm64),
2. create the GitHub release with archives and `checksums.txt`,
3. update the Homebrew formula in `vegaload/homebrew-tap`,
4. publish the container image to `ghcr.io/vegaload/vegaload` (a second job,
   `image`, in the same workflow).

## Before you tag

Set `appVersion` in `charts/vegaload/Chart.yaml` to the new version without the
`v` (for a tag `v0.5.2`, write `"0.5.2"`), and the image in
`examples/k8s/job.yaml` too. Merge that first. The Release run checks it with
`scripts/check-chart-version.sh` and stops before it publishes anything if the
two do not match. A pre-release tag such as `v0.5.2-rc.1` is not checked.

## One-time setup

1. Create a public repo `vegaload/homebrew-tap` with a `README.md` and no
   other files. GoReleaser writes `Formula/vegaload.rb` there.
2. Create a fine-grained personal access token with **Contents: read and
   write** on `vegaload/homebrew-tap` only.
3. In `vegaload/vegaload`, add it as the Actions secret
   `HOMEBREW_TAP_TOKEN`.

The default `GITHUB_TOKEN` cannot push to another repo, so the extra token is
required.

## Container image

The `image` job builds the `Dockerfile` for `linux/amd64` and `linux/arm64`
and pushes it to `ghcr.io/vegaload/vegaload`. The tag `v0.5.0` gives the image
tags `0.5.0` and `latest`. A pre-release tag such as `v0.5.0-rc.1` gives only
`0.5.0-rc.1`, and does not move `latest`. The Helm chart's default image tag is the version
without the `v`, so the two match. It needs no extra secret: the job uses the
workflow's own `GITHUB_TOKEN` with `packages: write`.

Do this once, after the first release that publishes an image:

1. Open the package at `https://github.com/orgs/vegaload/packages/container/package/vegaload`.
2. In **Package settings**, set the visibility to **Public**. A new package is
   private, and a cluster cannot pull a private image without a pull secret.
3. Check that the package is linked to the `vegaload/vegaload` repository (the
   image carries the label that links it).

Check the image:

```
docker run --rm ghcr.io/vegaload/vegaload:0.5.0 version
```

CI builds both architectures on every pull request (the `docker-image` job),
so a broken `Dockerfile` fails in the pull request.

## Windows packages

Every release already carries a Windows zip (`vegaload_<version>_windows_amd64.zip`
and `_arm64.zip`). Two more install paths are set up by hand, once.

**Scoop.**

1. Create a public repo `vegaload/scoop-bucket` with a `README.md`.
2. Give the token in `HOMEBREW_TAP_TOKEN` **Contents: read and write** on that
   repo as well as on `vegaload/homebrew-tap`.
3. In `.goreleaser.yaml`, remove the `#` at the start of each line of the
   `scoops:` block. Do this only after steps 1 and 2, or the release job fails
   at its last step.

Users then run:

```
scoop bucket add vegaload https://github.com/vegaload/scoop-bucket
scoop install vegaload
```

**winget.** Winget takes a pull request to `microsoft/winget-pkgs` for each
version. After a release, run
`wingetcreate update VegaLoad.VegaLoad --version X.Y.Z --urls <the two zip URLs> --submit`
(install `wingetcreate` with `winget install wingetcreate`). The first
version is created with `wingetcreate new <zip URL>`. Users then run
`winget install VegaLoad.VegaLoad`.

## Cutting a release

```
git checkout main && git pull
git tag -a v0.1.0 -m "v0.1.0"
git push origin v0.1.0
```

The workflow only runs for tags matching `v[0-9]*` (for example `v0.1.0`),
not a stray `vfoo` tag.

Watch the Release run in the Actions tab. When it is green:

```
brew update
brew install vegaload/tap/vegaload
vegaload version        # prints: vegaload 0.1.0
```

## Testing the release config locally

```
HOMEBREW_TAP_TOKEN=x goreleaser release --snapshot --clean --skip=publish
cat dist/homebrew/Formula/vegaload.rb
```

CI runs the same command on every pull request.

## Notes

- GoReleaser is pinned to v2. Its `brews` block is deprecated and will be
  removed in v3. We keep it because Homebrew casks are macOS-only and we
  want Linux users covered too.
- A later goal is `homebrew-core`, so that plain `brew install vegaload`
  works with no tap. It needs a stable, notable project and a source build
  formula, so it comes after the first few tagged releases.
