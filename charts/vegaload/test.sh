#!/usr/bin/env bash
# Renders the chart with different values and checks the result. It needs
# only `helm`. Run it from anywhere: ./charts/vegaload/test.sh
set -euo pipefail
chart="$(cd "$(dirname "$0")" && pwd)"
fail=0
# The chart's own appVersion, so a release bump does not break the test.
app_version=$(sed -n 's/^appVersion:[[:space:]]*"\{0,1\}\([^"[:space:]]*\)"\{0,1\}.*/\1/p' "$chart/Chart.yaml")
if [ -z "$app_version" ]; then echo "FAIL no appVersion in Chart.yaml"; exit 1; fi

render() { helm template t "$chart" --namespace perf "$@"; }
ok() { echo "ok   $1"; }
bad() { echo "FAIL $1"; fail=1; }

# expect_has NAME PATTERN -- VALUES...: the render must contain PATTERN.
expect_has() {
  local name=$1 pat=$2; shift 3
  if render "$@" 2>&1 | grep -Fq -- "$pat"; then ok "$name"; else bad "$name (missing: $pat)"; fi
}
# expect_lacks NAME PATTERN -- VALUES...: the render must not contain PATTERN.
expect_lacks() {
  local name=$1 pat=$2; shift 3
  if render "$@" 2>&1 | grep -Fq -- "$pat"; then bad "$name (found: $pat)"; else ok "$name"; fi
}
# expect_error NAME MESSAGE -- VALUES...: the render must fail with MESSAGE.
expect_error() {
  local name=$1 msg=$2; shift 3
  if out=$(render "$@" 2>&1); then bad "$name (rendered, but should fail)"
  elif grep -Fq -- "$msg" <<<"$out"; then ok "$name"; else bad "$name (wrong error: $out)"; fi
}

direct=(--set run.target=http://orders.shop.svc:8080/health --set run.protocol=http1)

expect_has   "direct mode passes the target"        '"http://orders.shop.svc:8080/health"' -- "${direct[@]}"
expect_has   "the target host is allowed"           '"orders.shop.svc"' -- "${direct[@]}"
expect_has   "a bare host:port target is allowed too" '"orders.shop.svc"' -- --set run.target=orders.shop.svc:9090 --set run.protocol=grpc
expect_has   "thresholds become flags"              '"p95 < 300ms"' -- "${direct[@]}" --set 'run.thresholds={p95 < 300ms}'
expect_has   "vus and duration"                     '"30s"' -- "${direct[@]}" --set run.vus=5 --set run.duration=30s
expect_has   "extra allowed hosts"                  '"db.shop.svc"' -- "${direct[@]}" --set 'run.allowTargets={db.shop.svc}'
expect_has   "the job name has the revision"        'name: t-vegaload-1' -- "${direct[@]}"
expect_has   "the job runs once"                    'backoffLimit: 0' -- "${direct[@]}"
expect_has   "runs as non-root"                     'runAsNonRoot: true' -- "${direct[@]}"
expect_has   "read-only root file system"           'readOnlyRootFilesystem: true' -- "${direct[@]}"
expect_has   "image tag defaults to appVersion"     "vegaload:$app_version" -- "${direct[@]}"
expect_has   "image tag can be set"                 'vegaload:9.9.9' -- "${direct[@]}" --set image.tag=9.9.9
expect_has   "pod fsGroup makes /tmp writable"      'fsGroup: 65532' -- "${direct[@]}"
expect_has   "the job is kept on upgrade"           'helm.sh/resource-policy": keep' -- "${direct[@]}"
expect_has   "the scenario ConfigMap has the revision" 'name: t-vegaload-scenario-1' -- --set scenario='x'
expect_lacks "direct mode has no ConfigMap"         'kind: ConfigMap' -- "${direct[@]}"
expect_has   "scenario mode makes a ConfigMap"      'kind: ConfigMap' -- --set scenario='export default function () {}'
expect_has   "scenario mode runs the mounted file"  '"/scenario/scenario.vl.js"' -- --set scenario='export default function () {}'
expect_has   "scenario file name can be set"        '"/scenario/checkout.vl.js"' -- --set scenario='x' --set scenarioFileName=checkout.vl.js
expect_lacks "scenario mode has no -target"         '- -target' -- --set scenario='x'

for kind in ClusterRole Role RoleBinding CustomResourceDefinition ServiceAccount Deployment; do
  expect_lacks "no $kind is created" "kind: $kind" -- "${direct[@]}"
done

expect_error "nothing to run is an error"           'Nothing to run' --
expect_error "target without protocol is an error"  'Nothing to run' -- --set run.target=http://x
expect_error "scenario and target together"         'not both' -- "${direct[@]}" --set scenario=x

exit $fail
