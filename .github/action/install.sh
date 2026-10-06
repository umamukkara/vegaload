#!/usr/bin/env bash
# Installs a released vegaload binary for the GitHub Action in action.yml.
#
# It downloads the release archive for this runner's OS and CPU, checks it
# against the release's checksums.txt, and puts vegaload on PATH. It sends
# nothing anywhere: the only network calls are the downloads from the
# GitHub release itself.
#
# Inputs, as environment variables:
#   VEGALOAD_REPOSITORY  GitHub repository to download from (owner/name)
#   VEGALOAD_VERSION     a tag like v0.3.0, or "latest". Empty means: use
#                        VEGALOAD_ACTION_REF if it is a version tag, else latest.
#   VEGALOAD_ACTION_REF  the ref the action was called with (e.g. v0.3.0)
#   VEGALOAD_OS/ARCH     override the detected OS and CPU (for testing)
#   VEGALOAD_BASE_URL    override https://github.com (for testing)
#   VEGALOAD_INSTALL_DIR where to put the binary (default: $RUNNER_TEMP or /tmp)
# Outputs, to $GITHUB_PATH and $GITHUB_OUTPUT when they are set:
#   vegaload on PATH, and the installed version.
set -euo pipefail

repo="${VEGALOAD_REPOSITORY:-vegaload/vegaload}"
version="${VEGALOAD_VERSION:-}"
action_ref="${VEGALOAD_ACTION_REF:-}"

if [ -z "$version" ]; then
  if [[ "$action_ref" =~ ^v[0-9]+\.[0-9]+\.[0-9]+([-.][0-9A-Za-z.]+)?$ ]]; then
    version="$action_ref"
  else
    version="latest"
  fi
fi

# Detect the platform. Linux and macOS runners are supported.
os="${VEGALOAD_OS:-$(uname -s)}"
case "$os" in
  Linux|linux) os="linux" ;;
  Darwin|macOS|darwin) os="darwin" ;;
  *) echo "vegaload action: $os runners are not supported yet (Linux and macOS are)" >&2; exit 1 ;;
esac
arch="${VEGALOAD_ARCH:-$(uname -m)}"
case "$arch" in
  x86_64|amd64|X64) arch="amd64" ;;
  arm64|aarch64|ARM64) arch="arm64" ;;
  *) echo "vegaload action: unsupported CPU $arch" >&2; exit 1 ;;
esac

base="${VEGALOAD_BASE_URL:-https://github.com}/${repo}/releases"
if [ "$version" = "latest" ]; then
  # /releases/latest redirects to /releases/tag/<tag>.
  resolved="$(curl -fsSL -o /dev/null -w '%{url_effective}' "${base}/latest")"
  version="${resolved##*/}"
  if [[ ! "$version" =~ ^v[0-9] ]]; then
    echo "vegaload action: could not find the latest release of ${repo}" >&2
    exit 1
  fi
fi
number="${version#v}"

archive="vegaload_${number}_${os}_${arch}.tar.gz"
dest="${VEGALOAD_INSTALL_DIR:-${RUNNER_TEMP:-/tmp}}/vegaload-${number}"
rm -rf "$dest"
mkdir -p "$dest"

echo "vegaload action: installing ${version} (${os}/${arch}) from ${repo}"
if ! curl -fsSL --retry 3 -o "${dest}/${archive}" "${base}/download/${version}/${archive}"; then
  echo "vegaload action: could not download ${archive} from release ${version} of ${repo}" >&2
  echo "  check that the version exists: ${base}" >&2
  exit 1
fi
if ! curl -fsSL --retry 3 -o "${dest}/checksums.txt" "${base}/download/${version}/checksums.txt"; then
  echo "vegaload action: could not download checksums.txt from release ${version} of ${repo}" >&2
  exit 1
fi

# Check the archive against the release's checksums.
expected="$(awk -v f="$archive" '$2 == f { print $1 }' "${dest}/checksums.txt")"
if [ -z "$expected" ]; then
  echo "vegaload action: ${archive} is not listed in checksums.txt" >&2
  exit 1
fi
if command -v sha256sum >/dev/null 2>&1; then
  actual="$(sha256sum "${dest}/${archive}" | awk '{ print $1 }')"
else
  actual="$(shasum -a 256 "${dest}/${archive}" | awk '{ print $1 }')"
fi
if [ "$expected" != "$actual" ]; then
  echo "vegaload action: checksum mismatch for ${archive}" >&2
  echo "  expected ${expected}" >&2
  echo "  actual   ${actual}" >&2
  exit 1
fi

tar -xzf "${dest}/${archive}" -C "$dest" vegaload
chmod +x "${dest}/vegaload"

if [ -n "${GITHUB_PATH:-}" ]; then
  echo "$dest" >> "$GITHUB_PATH"
fi
if [ -n "${GITHUB_OUTPUT:-}" ]; then
  echo "version=${version}" >> "$GITHUB_OUTPUT"
  echo "path=${dest}/vegaload" >> "$GITHUB_OUTPUT"
fi
echo "vegaload action: installed $("${dest}/vegaload" version)"
