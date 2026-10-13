---
title: CI/CD
description: Run a load test from a pipeline and from a cluster.
sidebar_position: 1
---

# CI/CD

## Use in GitHub Actions

This repository is also a GitHub Action. It installs a released VegaLoad
(Linux and macOS runners) and runs it, and the step fails when `vegaload`
exits with a non-zero code. With `-threshold`, that means a broken
threshold fails the build:

```yaml
- uses: vegaload/vegaload@v0.9.0
  with:
    args: >-
      run -target https://staging.example.com/health -protocol http1
      -vus 20 -duration 1m -allow-target staging.example.com
      -threshold "p95 < 300ms" -threshold "error_rate < 1%"
```

Inside GitHub Actions, `vegaload run` also appends a summary to the job's
page: the key numbers, and the verdict of each threshold, check and
baseline gate. It does this on its own, because GitHub sets
`GITHUB_STEP_SUMMARY`. Pass `-no-step-summary` to turn it off. Add `-junit`
to write JUnit XML as well, which many CI systems show as test results:

```yaml
- uses: vegaload/vegaload@v0.9.0
  with:
    args: >-
      run -target https://staging.example.com/health -protocol http1
      -vus 20 -duration 1m -allow-target staging.example.com
      -threshold "p95 < 300ms" -junit vegaload-junit.xml
- uses: actions/upload-artifact@v4
  if: always()
  with:
    name: vegaload-junit
    path: vegaload-junit.xml
```

- `args` is what you would type after `vegaload`, with the same quoting.
  Leave it out to only install, then call `vegaload` in later steps.
- The action installs the version you pin after the `@`. Set `version:` to
  install a different one. A branch name such as `@main` installs the
  latest release.
- The archive is checked against the release's `checksums.txt` before it
  is used. Nothing is sent anywhere except the downloads from the GitHub
  release.
- The step's `version` output is the installed version.

## Use in Kubernetes

A Helm chart in [`charts/vegaload`](https://github.com/vegaload/vegaload/tree/main/charts/vegaload) runs a load test as a
Kubernetes Job, close to the service you are testing. It needs no CRD and no
cluster-wide permissions, only access to one namespace. It uses the published
image `ghcr.io/vegaload/vegaload` (from v0.5.0):

```
helm install smoke ./charts/vegaload --namespace perf \
  --set run.target=http://orders.shop.svc:8080/health --set run.protocol=http1 \
  --set run.vus=5 --set run.duration=30s --set 'run.thresholds={p95 < 300ms}' \
  --wait --wait-for-jobs
kubectl logs -n perf job/smoke-vegaload-1
```

From v0.7.0, you can install the chart without a
clone: `helm install smoke oci://ghcr.io/vegaload/charts/vegaload --version <version> ...`.

A scenario file works too: `--set-file scenario=./checkout.vl.js`. A broken
threshold fails the Job. See the chart's README. For plain `kubectl`, use
[`examples/k8s/job.yaml`](https://github.com/vegaload/vegaload/blob/main/examples/k8s/job.yaml).
