# Releasing VegaLoad

A release is a version tag. Pushing the tag runs
`.github/workflows/release.yml`, which uses GoReleaser to:

1. build binaries for Linux, macOS and Windows (amd64 and arm64),
2. create the GitHub release with archives and `checksums.txt`,
3. update the Homebrew formula in `vegaload/homebrew-tap`.

## One-time setup

1. Create a public repo `vegaload/homebrew-tap` with a `README.md` and no
   other files. GoReleaser writes `Formula/vegaload.rb` there.
2. Create a fine-grained personal access token with **Contents: read and
   write** on `vegaload/homebrew-tap` only.
3. In `vegaload/vegaload`, add it as the Actions secret
   `HOMEBREW_TAP_TOKEN`.

The default `GITHUB_TOKEN` cannot push to another repo, so the extra token is
required.

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
