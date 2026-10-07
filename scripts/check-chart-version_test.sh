#!/usr/bin/env bash
# Tests check-chart-version.sh. Run it from anywhere: ./scripts/check-chart-version_test.sh
set -euo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
check="$here/check-chart-version.sh"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
fail=0
ok() { echo "ok   $1"; }
bad() { echo "FAIL $1"; fail=1; }

printf 'apiVersion: v2\nappVersion: "0.5.1"\n' > "$tmp/quoted.yaml"
printf 'apiVersion: v2\nappVersion: 0.5.1\n' > "$tmp/bare.yaml"
printf 'apiVersion: v2\nname: x\n' > "$tmp/none.yaml"

passes() { local name=$1; shift; if "$check" "$@" >/dev/null 2>&1; then ok "$name"; else bad "$name"; fi; }
fails()  { local name=$1; shift; if "$check" "$@" >/dev/null 2>&1; then bad "$name"; else ok "$name"; fi; }

passes "matching tag, quoted value"      v0.5.1 "$tmp/quoted.yaml"
passes "matching tag, bare value"        v0.5.1 "$tmp/bare.yaml"
fails  "tag is newer than the chart"     v0.5.2 "$tmp/quoted.yaml"
fails  "tag is older than the chart"     v0.5.0 "$tmp/quoted.yaml"
passes "a pre-release tag is skipped"    v0.5.2-rc.1 "$tmp/quoted.yaml"
fails  "no appVersion is an error"       v0.5.1 "$tmp/none.yaml"
msg=$("$check" v0.5.2 "$tmp/quoted.yaml" 2>&1 || true)
if grep -q 'appVersion is 0.5.1, but the tag is v0.5.2' <<<"$msg"; then ok "the message names both versions"; else bad "the message names both versions"; fi
# The real chart: its own appVersion must pass for the tag it names.
app=$(sed -n 's/^appVersion:[[:space:]]*"\([^"]*\)".*/\1/p' "$here/../charts/vegaload/Chart.yaml")
passes "the real chart matches its own version" "v$app"

exit $fail
