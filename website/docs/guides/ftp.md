---
title: "Test an FTP server"
description: "`-target` is `ftp://host` (port 21, or 990 with `-opt tls=implicit`) or `ftps://host` (implicit TLS, port 990)."
sidebar_position: 9
---

# Test an FTP server

```
FTP_PASSWORD=secret vegaload run -vus 4 -duration 10s -protocol ftp \
  -target ftp://localhost -opt mode=roundtrip -opt path=/upload/vl-{id}.bin \
  -opt size=1MiB -opt allow_writes=true -opt username=vegaload \
  -opt password_env=FTP_PASSWORD
```

`-target` is `ftp://host` (port 21, or 990 with `-opt tls=implicit`) or `ftps://host` (implicit TLS, port 990). A port in the URL always wins. `vegaload doctor` takes no `-opt`, so it reads only the URL: it checks port 21 for `ftp://host`. To check port 990, give it `ftps://host` or `ftp://host:990`.
Put the password in an environment variable. Do not put it in the URL.
With no username and no `password_env`, the client logs in as `anonymous`
with the password `vegaload@`. A username other than `anonymous` needs
`password_env`. `tls=explicit` on `ftp://` is AUTH TLS. `tls=none` is not
allowed with `ftps://`. `-insecure` skips certificate checks for a test
server.

`-opt` keys:

| Key | Default | Meaning |
| --- | --- | --- |
| `mode` | `download` | `connect`, `download`, `upload`, `list`, `stat`, `delete`, `roundtrip` |
| `path` | | The file or directory. `{id}` is replaced in the path and in a body. |
| `size` | | Upload this many bytes (`1KiB`, `1MiB`, up to 4GiB) instead of a body. `size=0` is an empty file. |
| `fill` | `random` | `random`, `zero` or `text`, used with `size`. |
| `expect_size` | | Download must be exactly this many bytes. |
| `expect_sha256` | | Download must hash to this SHA-256 (64 hex characters). |
| `expect` | | Text the download must contain, searched in the first 1MiB, or a name the listing must contain. |
| `limit` | `1000` | How many listing entries the reply keeps (1 to 100000). |
| `hidden` | false | For `list` only: send `LIST -a`. MLSD already returns names that start with `.`. |
| `username` | `anonymous` | Login name. |
| `password_env` | | Environment variable that holds the password. |
| `tls` | from the URL | `none`, `explicit` or `implicit`. With `implicit` and no port in the URL, the port is 990. |
| `tls_verify` | true | Check the server certificate. `-insecure` turns this off. |
| `tls_max` | `1.2` | Highest TLS version, `1.2` or `1.3`. It is `1.2` because vsftpd 3.0.5 fails large uploads over TLS 1.3 (`426 Failure reading network stream`). Use `1.3` for a server that needs it. |
| `ca_file`, `cert_file`, `key_file`, `tls_server_name` | | See [TLS with your own CA](/guides/tls). |
| `sessions` | `10` | How many control connections this run may hold. Match it to `-vus`. Extra users wait. |
| `connection` | `shared` | `per_call` dials a new session and drops it. It still takes one of the `sessions` slots, so the number in flight stays bounded. |
| `data_host` | `control` | `control` dials the data connection to the host you named. `announced` uses the PASV address, which must be an IP. |
| `epsv` | `auto` | `off` uses PASV only. |
| `allow_writes` | false | Required for `upload`, `roundtrip` and `delete`. |
| `allow_admin` | false | Also required for `delete`, and for an `upload` whose path has no `{id}`. Needs `allow_writes` as well. |

- Transfers are passive and binary. There is no active mode, no ASCII mode,
  no resume and no recursion. One server per run.
- `connect` logs in and does nothing else. `download` reads one file.
  `upload` writes one file. `list` lists one directory. `stat` asks for
  the size. `delete` removes one file. `roundtrip` uploads, downloads,
  checks the bytes and deletes that file. It needs `{id}` in the path, so
  it never removes a file that was already there.
- `sessions` is how many control connections the run may hold at once.
  Set it to the same number as `-vus`, or extra users wait for a free
  session. A session that the server closes is not reused. The next call
  dials again. There is no hidden retry.
- `{id}` in a path or a body becomes a new value on every call. It is not
  written into the bytes that `size` generates.
- An upload without `size` and without a body is an error. A failed upload
  can leave a partial file. `roundtrip` still tries to delete its file. If
  that delete fails, the call can still pass and the text names the file
  that is left.
  A call that the end of the run cuts off can also leave its file, at most
  one per user. Use a scratch directory.
- The account is the real lock. `allow_writes` and `allow_admin` only stop
  VegaLoad from sending the command. Give the account a scratch directory.
- A listing skips lines the client cannot parse. `limit` trims what the
  reply returns. The client still reads the whole listing first, so a huge
  directory uses memory for every name. Many servers expand a wildcard in
  a list path. `hidden` changes `LIST` only.
- `data_host=control` ignores the address in the PASV reply and dials the
  host from `-target`. Use that when the server is behind NAT and
  announces an address the client cannot reach. `data_host=announced`
  dials the announced IP. A firewall must allow the passive port range.
- The timeout covers the whole call, including the transfer. A call that
  takes too long says `timed out after <duration>`. Throughput in the
  report is the bytes of the call divided by the time the call took.
- A script's `ftp` reply has `entries` (each `name`, `type`, `size` and
  `time`), `size`, `total`, `timing` (`connectMs`, `loginMs`,
  `transferMs`, `uploadMs`, `downloadMs`, `deleteMs`) and `text`. `time`
  is UTC, such as `2020-01-02T15:04:05Z`, or `""` when the server gave
  none. Give the password as `password: env.FTP_PASSWORD`.
- The host allowlist and caps apply as for HTTP targets.
