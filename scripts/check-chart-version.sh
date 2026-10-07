#!/usr/bin/env bash
# Checks that the Helm chart's appVersion matches a release tag, so the chart's
# default image is the one this release publishes.
#
#   scripts/check-chart-version.sh v0.5.1 [path/to/Chart.yaml]
#
# A pre-release tag (v0.5.1-rc.1) is skipped: its image is not the chart's.
set -euo pipefail

tag=${1:?usage: check-chart-version.sh TAG [Chart.yaml]}
root="$(cd "$(dirname "$0")/.." && pwd)"
chart=${2:-$root/charts/vegaload/Chart.yaml}
want=${tag#v}

case "$want" in
  *-*) echo "pre-release tag $tag: not checking the chart version"; exit 0 ;;
esac

have=$(sed -n 's/^appVersion:[[:space:]]*"\{0,1\}\([^"[:space:]]*\)"\{0,1\}.*/\1/p' "$chart")
if [ -z "$have" ]; then
  echo "no appVersion found in $chart" >&2
  exit 1
fi
if [ "$have" != "$want" ]; then
  echo "the chart's appVersion is $have, but the tag is $tag." >&2
  echo "Set appVersion to \"$want\" in charts/vegaload/Chart.yaml (and the image in" >&2
  echo "examples/k8s/job.yaml), merge that, and tag again." >&2
  exit 1
fi
echo "chart appVersion $have matches $tag"
