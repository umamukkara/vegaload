---
title: "Test an SFTP server"
description: "`-target` is `sftp://host` (port 22)."
sidebar_position: 10
---

# Test an SFTP server

```
SFTP_PASSWORD=secret vegaload run -vus 4 -duration 10s -protocol sftp \
  -target sftp://localhost -opt mode=roundtrip -opt path=upload/vl-{id}.bin \
  -opt size=1MiB -opt allow_writes=true -opt username=vegaload \
  -opt password_env=SFTP_PASSWORD -opt host_key=SHA256:your-fingerprint
```

`-target` is `sftp://host` (port 22). `ssh://` is refused. A port in the URL always wins. Do not put a password in the URL, and do not put a query string or a path there. Put the remote path in `-opt path`. The path may be relative. A username is required. Set it in the URL or with `-opt username`. When both are set they must be the same. There is no anonymous login.

Set `password_env` or `key_file`, or both. With both, the key is offered first and then the password. `key_file` is an OpenSSH or PEM private key (RSA, ECDSA or Ed25519). It is read once. Protect that file (mode 600). `key_passphrase_env` is the passphrase for an encrypted key, and it needs `key_file`. There is no agent and no keyboard-interactive login.

The host key is checked by SHA256 fingerprint. `host_key` is one fingerprint, or several separated by commas, each `SHA256:` and 43 base64 characters. Without it, the client refuses the key and the error shows the type and the fingerprint. `-insecure` skips that check for a test server. `host_key` together with `-insecure` is an error. A fingerprint that does not match does not suggest `-insecure`. A server can have more than one key type. The client offers Ed25519 first, then ECDSA, then RSA, and the server answers with the first one it has, as OpenSSH does. If the error shows a type you did not pin, list the fingerprint of every host key (`ssh-keyscan host` shows them).

`-opt` keys:

| Key | Default | Meaning |
| --- | --- | --- |
| `mode` | `download` | `connect`, `download`, `upload`, `list`, `stat`, `delete`, `roundtrip` |
| `path` | | The file or directory. `{id}` is replaced in the path and in a body. A list path may start with `-`. |
| `size` | | Upload this many bytes (`1KiB`, `1MiB`, up to 4GiB) instead of a body. `size=0` is an empty file. |
| `fill` | `random` | `random`, `zero` or `text`, used with `size`. |
| `expect_size` | | Download must be exactly this many bytes. |
| `expect_sha256` | | Download must hash to this SHA-256 (64 hex characters). |
| `expect` | | Text the download must contain, searched in the first 1MiB, or a name the listing must contain. |
| `limit` | `1000` | How many listing entries the reply keeps (1 to 100000). |
| `hidden` | false | Keep names that start with `.`. The server returns them, and the client drops them unless this is true. |
| `username` | | Login name. Required when the URL has no user. |
| `password_env` | | Environment variable that holds the password. |
| `key_file` | | Private key file. |
| `key_passphrase_env` | | Environment variable that holds the key passphrase. Needs `key_file`. |
| `host_key` | | SHA256 fingerprints of host keys that are trusted. |
| `connections` | `1` | How many SSH connections this run may hold (1 to 256). |
| `connection` | `shared` | `per_call` dials a new connection and drops it. |
| `allow_writes` | false | Required for `upload`, `roundtrip` and `delete`. |
| `allow_admin` | false | Also required for `delete`, and for an `upload` whose path has no `{id}`. Needs `allow_writes` as well. |
| `concurrent_writes` | false | Send several writes at once. On OpenSSH this is about 3 times faster. Turn it on after a test against the target server. Some servers handle out-of-order writes badly. |

`ca_file`, `cert_file`, `tls_server_name` and the other TLS options are unknown options here. SFTP is not FTP with TLS.

- `connect` logs in and asks for the working directory. `download` reads one file. A directory is an error. `upload` writes one file. `list` lists one directory. The default path is `.`. `stat` asks for the size, time and type. A directory can be stated. `delete` removes one file, and refuses a directory. `roundtrip` uploads, downloads, checks the bytes and deletes that file. It needs `{id}` in the path, so it never removes a file that was already there. It uses one connection for all three steps.
- `{id}` in a path or a body becomes a new value on every call. It is not written into the bytes that `size` generates.
- `connect`, `download`, `list` and `stat` are always allowed. `upload` with `{id}` needs `allow_writes`. `upload` without `{id}` needs `allow_writes` and `allow_admin`. `roundtrip` needs `allow_writes` and `{id}`. `delete` needs both flags. The check happens before any network call.
- The account is the real lock. `allow_writes` and `allow_admin` only stop VegaLoad from sending the command. Give the account a chroot, a read-only directory, or write access only to a scratch directory.
- One SSH connection carries many calls. A timeout closes that connection, so the other calls on it fail too. `connections` greater than 1 limits how many calls share one connection. The call timeout should be longer than the slowest transfer. An SSH connection uses one CPU core for encryption, so more connections can raise throughput.
- `loginMs` is the whole SSH handshake, the login and the SFTP start. It is not split. A reused connection reports `connectMs` and `loginMs` as 0.
- A listing does not report bytes received. Names that start with `.` are dropped unless `hidden=true`. The reply is sorted by name. `total` counts after that filter.
- A failed upload can leave a partial file. `concurrent_writes` does not change that. `roundtrip` still checks the bytes and tries to delete its file, for up to one second. If that delete fails, the call can still pass and the text names the file that is left. A call that the end of the run cuts off can also leave its file, at most one per user. Use a scratch directory.
- The timeout covers the whole call, including the dial and the transfer. A call that takes too long says `timed out after <duration>`. Throughput in the report is `bytesReceived` or `bytesSent` divided by the time the call took.
- Servers limit how many logins may start at once (MaxStartups) and how many connections one user may hold. A large `connections` value can be refused.
- There is no agent, no keyboard-interactive login, no certificates, no `known_hosts` file, no SCP, no port forwarding and no remote command. A very old key exchange such as `diffie-hellman-group1-sha1` will not connect. This build still accepts an `ssh-rsa` host key.
- A script's `sftp` reply has the same fields as `ftp`: `entries` (each `name`, `type`, `size` and `time`), `size`, `total`, `timing` (`connectMs`, `loginMs`, `transferMs`, `uploadMs`, `downloadMs`, `deleteMs`) and `text`. `time` is UTC. `type` is `file`, `dir`, `link` or `other`. Give the password as `password: env.SFTP_PASSWORD` and a key passphrase as `key_passphrase: env.SFTP_PP`. `insecure: true` skips the host key check.
- The host allowlist and caps apply as for HTTP targets.
