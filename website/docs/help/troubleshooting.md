---
title: Troubleshooting
description: Fixes for the problems people hit most often with VegaLoad.
sidebar_position: 1
---

# Troubleshooting

Find your symptom below. Each fix was tried on VegaLoad 0.9.0.

## macOS says the download cannot be opened

You see "Apple could not verify vegaload is free of malware", or `zsh: killed`.

The macOS binaries are not signed yet. A browser marks the download as blocked. Remove the mark once:

```
xattr -d com.apple.quarantine ./vegaload
./vegaload version
```

Files fetched with `gh release download`, `curl` or Homebrew do not have this mark.

## `bad CPU type in executable`

You downloaded the wrong macOS archive. Run `uname -m`.

- `arm64`: use the `darwin_arm64` archive.
- `x86_64`: use the `darwin_amd64` archive.

See [Install](../get-started/install.md).

## A run exits 0, but every call failed

A run with no threshold does not fail on errors. If the target is down, you still get exit code 0. Here is a run against a closed port:

```
fixed-vus summary
  total:    60869
  failed:   60869
  mean:     32.361µs
```

Look at `failed` in the summary. In CI, add a threshold so a bad run exits 3:

```
vegaload run -threshold "error_rate < 0.01" -threshold "failed < 5" scenario.vl.js
```

See [Gate a run on thresholds](../guides/thresholds.md).

## Almost all calls fail, and latency is near zero

Mean latency of a few microseconds with 100% failures means the calls never reached the server. Common causes:

- The target is down, or the host or port is wrong.
- You are running out of local ports. Every call opens a new connection, and the system keeps each closed port reserved for a while. A very high rate on one machine uses them all up. Lower the rate, or spread the load over more machines.

The sample app's [testing notes](https://github.com/vegaload/vegaload/blob/main/examples/sample-app/TESTING.md) describe this for handshake-only WebSocket runs.

## A gRPC run fails 100% with sub-millisecond latency

Calls that fail this fast were rejected before the server did any work. Check the method and the body.

- `-method` must be the full path, such as `/package.Service/Method`.
- The sample app has no reflection, so VegaLoad cannot look up the method for you.
- The JSON body must match the request message.

Here is a run against the sample app with a method that does not exist:

```
protocols
  FAIL  grpc  13958 calls, 13958 failed (100.00%)  p50 125µs  p95 224µs
```

See the [gRPC guide](../guides/grpc.md).

## VegaLoad refuses to start: "host not allowed"

```
Target "http://example.com" is not localhost or allowlisted. Continue? [y/N]
vegaload run: refusing to run against "http://example.com" without confirmation
(pass -yes, or add it with -allow-target)
```

The exit code is 2. VegaLoad only runs against localhost without asking. For any other host, do one of these:

- Add the host once with `-allow-target example.com`. You can repeat the flag.
- Pass `-yes` to skip the question for this run. Use this for CI and for any run with no one at the keyboard.

Only test systems you own or have permission to test. See [Safety](../ai-agents/safety.md).

## My agent does not see VegaLoad

Check what is set up in this project:

```
vegaload init -status
vegaload doctor
```

`init -status` lists each file as present or `missing`. If something is missing, repair it:

```
vegaload init -force
```

`vegaload doctor` also warns when an editor is installed but VegaLoad is not registered with it. Restart the editor after you change its config. Claude Desktop is not set up by `init`. Use `vegaload doctor -host claude-desktop -fix`.

## Files are left behind after an FTP or SFTP run

A failed upload can leave a partial file. A call that the end of the run cuts off can also leave its file, at most one per user. Point the run at a scratch directory, and delete it afterwards. See the [FTP guide](../guides/ftp.md) and [SFTP guide](../guides/sftp.md).

## Still stuck?

Run `vegaload doctor` and keep the output. Then [open an issue](https://github.com/vegaload/vegaload/issues) with the doctor output, the command you ran, and the summary it printed.
