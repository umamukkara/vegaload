---
title: Install
description: Install VegaLoad with Homebrew, a release archive, source, or Docker.
sidebar_position: 1
---

With Homebrew (macOS and Linux), after the first tagged GitHub release:

```
brew install vegaload/tap/vegaload
vegaload version
```

`brew tap vegaload/tap` followed by `brew install vegaload` does the same
thing. Upgrade with `brew upgrade vegaload`. Until a tag exists, build from
source below.

Or build from source (Go 1.22+):

```
git clone https://github.com/vegaload/vegaload
cd vegaload
go build -o vegaload ./cmd/vegaload
./vegaload version
```

`go install github.com/vegaload/vegaload/cmd/vegaload@latest` also works
once the repository is public. Release archives for Linux, macOS and
Windows are attached to each [GitHub release](https://github.com/vegaload/vegaload/releases).

On Windows 10 or later, download the zip for your CPU from the release page,
unpack it, and run `vegaload.exe`. Put its folder on your `PATH` to run
`vegaload` from anywhere. `run`, `validate`, `doctor`, `init` and `mcp serve`
all work. Python scenarios need Python 3 from python.org (VegaLoad finds
`python3`, `python` or the `py` launcher). Scoop and winget packages are set
up in [RELEASING.md](https://github.com/vegaload/vegaload/blob/main/RELEASING.md) and come after the first Windows release.

On macOS, pick the archive for your CPU. Run `uname -m`. If it prints
`arm64`, use the `darwin_arm64` archive. If it prints `x86_64`, use
`darwin_amd64`. The wrong one fails with `bad CPU type in executable`.

The macOS binaries are not signed yet. A file downloaded in a browser is
blocked: you see "Apple could not verify vegaload is free of malware", or
`zsh: killed`. Remove the download mark once, then run it:

```
xattr -d com.apple.quarantine ./vegaload
./vegaload version
```

Files fetched with `gh release download`, `curl` or Homebrew have no such
mark.

A scratch-based Docker image is published to `ghcr.io/vegaload/vegaload` with
each release from v0.9.0 on (`linux/amd64` and `linux/arm64`):

```
docker run --rm ghcr.io/vegaload/vegaload:0.9.0 version
```

You can also build it yourself from the included `Dockerfile`
(`docker build .`). The image carries nothing but the binary
and CA certificates, so Python-scripted scenarios (which shell out to a
local `python3`) need a different base image — see the `Dockerfile`'s
comment.
