# VegaLoad Helm chart

Runs one VegaLoad load test as a Kubernetes Job. The chart creates a Job (and a
ConfigMap when you give a scenario file). It creates no CRD, no Role and no
cluster-wide object, so a user with access to one namespace can install it.

**Image.** The chart pulls `ghcr.io/vegaload/vegaload`, tagged with the
chart's `appVersion` (the VegaLoad version, without the `v`). The image is
published from release v0.5.0 on, for `linux/amd64` and `linux/arm64`. If your
cluster cannot reach ghcr.io, or you want an earlier version, build the image
from the repository's `Dockerfile`, push it to your own registry, and add
`--set image.repository=<your registry>/vegaload --set image.tag=<tag>`.

```
# Protocol-direct: just a target
helm install smoke ./charts/vegaload -n perf \
  --set run.target=http://orders.shop.svc:8080/health --set run.protocol=http1 \
  --set run.vus=5 --set run.duration=30s \
  --set 'run.thresholds={p95 < 300ms,error_rate < 1%}' \
  --wait --wait-for-jobs --timeout 15m

# Or a scenario file
helm install checkout ./charts/vegaload -n perf \
  --set-file scenario=./checkout.vl.js \
  --set 'run.allowTargets={orders.shop.svc}' --set run.vus=10 --set run.duration=1m

kubectl logs -n perf job/smoke-vegaload-1
```

`--wait --wait-for-jobs` makes `helm install` wait for the run and return an
error when it fails. Without it, `helm install` returns at once and you watch
the Job.

## Run it again

`helm upgrade` with the same or new values starts a new Job (the Job name ends
with the revision number). Each Job is marked `helm.sh/resource-policy: keep`,
so Helm does not delete the old one. The old Job and its logs stay until
`ttlSecondsAfterFinished` ends (24 hours by default). Each revision also gets
its own scenario ConfigMap, so an upgrade does not change the scenario of an
earlier run.

`helm uninstall` does not delete the Jobs either. They are removed by the same
24-hour limit once they finish, and by `activeDeadlineSeconds` if one never
finishes. To remove one at once, run `kubectl delete job <name>`.

## Pass and fail

A broken threshold makes `vegaload run` exit with code 3. The Job then fails.
The summary, with the failed thresholds, is in the pod log.

## Values

See `values.yaml`; every value has a comment. The main ones:

| Value | Meaning |
|---|---|
| `run.target`, `run.protocol` | What to load test (protocol-direct mode). |
| `scenario` (use `--set-file`) | A scenario file, instead of a target. Set one or the other. |
| `run.vus`, `run.duration`, `run.executor`, `run.stages`, `run.rate`, `run.maxVUs` | Load shape. They are the flags of `vegaload run`. |
| `run.thresholds` | Pass or fail rules. |
| `run.allowTargets` | Extra hosts the run may call. The target's own host is allowed for you. |
| `image.repository`, `image.tag` | The VegaLoad image. The tag defaults to the chart's `appVersion`. |
| `resources`, `nodeSelector`, `tolerations`, `affinity` | Where and how the pod runs. |

## Safety

Writing `run.target` is the permission to load test that host, so it is passed
as `-allow-target`. Any other host a scenario calls must be in
`run.allowTargets`. The pod runs as a non-root user, with a read-only root file
system and all capabilities dropped. A pod `fsGroup` makes the `/tmp` volume
writable for that user, which is where the audit log goes.

Python scenarios need Python in the image, and the default image has none. Use
your own image with `image.repository`.

## Without Helm

[`examples/k8s/job.yaml`](../../examples/k8s/job.yaml) is the same Job as plain
YAML for `kubectl apply`.

## Tests

`./charts/vegaload/test.sh` renders the chart with different values and checks
the result. It needs only `helm`. CI runs it.
