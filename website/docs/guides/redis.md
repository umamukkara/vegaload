---
title: "Test a Redis or Valkey server"
description: "The `redis` driver runs one command for each iteration, read from `-body`, or several commands, one per line."
sidebar_position: 7
---

# Test a Redis or Valkey server

The `redis` driver runs one command for each iteration, read from `-body`,
or several commands, one per line. Two or more lines are a pipeline. They
are not atomic. `-opt transaction=true` runs them inside `MULTI`/`EXEC`,
even when there is only one. It uses a pure Go client, so there is nothing
to install. It is **read-only by default**. Redis has no session read-only
switch, so VegaLoad checks every command before it sends anything. If one
command in a pipeline is refused, nothing is sent. Add
`-opt allow_writes=true` to allow writes. Admin commands such as `FLUSHALL`
also need `-opt allow_admin=true`, and `allow_admin` without `allow_writes`
is an error. A read-only user on the server is still the real lock.

The target is `redis://host[:port][/database]` or `rediss://` for TLS, with
port 6379 by default. `-protocol` stays `redis`. The URL takes no query
string, and a password in the URL is refused. It is tested with Redis 6 and
7 and with Valkey 7 and 8. KeyDB, Dragonfly and Garnet often work too.

```
# One read, 20 users, for 30 seconds. The password is in $REDIS_PASSWORD.
vegaload run -target redis://127.0.0.1:6379 -protocol redis \
  -body 'GET user:42' \
  -opt password_env=REDIS_PASSWORD -opt pool=20 \
  -vus 20 -duration 30s

# A placeholder is one whole argument, even when the value has spaces.
vegaload run -target redis://127.0.0.1:6379 -protocol redis \
  -body 'GET ?' -opt 'args=["user:42"]'

# A pipeline that writes. Both lines need allow_writes.
vegaload run -target redis://127.0.0.1:6379 -protocol redis \
  -body $'INCR hits:42\nEXPIRE hits:42 60' \
  -opt allow_writes=true
```

The options are `username`, `password_env`, `database`, `tls`
(`false`, `true`, `skip-verify`), `pool`, `protocol` (`2` or `3`),
`allow_writes`, `allow_admin`, `transaction`, `args`, `min_rows`, `expect`
and `max_rows`. `ca_file`, `cert_file`, `key_file` and `tls_server_name`
are in [TLS with your own CA](/guides/tls).

- The connections are kept in a pool that all users share. `pool` is the
  most connections to open (default 10). Waiting for a free connection
  counts as part of the measured time.
- Put the password in an environment variable and pass its name with
  `-opt password_env=NAME`. A password does not need a user: the default
  user is fine. An ACL user can be `username` or the user in the URL.
- `database` is a number from 0 to 255. It can also be the path of the URL,
  as `redis://host:6379/2`. Give it in one place.
- `tls=false` is the default for `redis://`. `rediss://` means TLS.
  `true` checks the certificate and the host name. With `-insecure`, `true`
  does not check the certificate. `skip-verify` uses TLS and does not check
  the certificate. `tls=false` with `rediss://` is an error. A connection
  reset on `redis://` may mean the port wants `rediss://`, and a failed TLS
  handshake on `rediss://` may mean the port is plain.
- `protocol=2` (the default) speaks RESP2. `protocol=3` speaks RESP3.
- A command is written the way redis-cli writes it. Spaces and tabs split
  arguments. `"double quotes"` and `'single quotes'` keep spaces. Inside
  double quotes, `\n`, `\r`, `\t`, `\b`, `\a`, `\\`, `\"` and `\xHH` are
  escapes. Inside single quotes, only `\'` is an escape. A line that is
  blank or starts with `#` is skipped. A quoted string cannot cross a line.
- An argument that is exactly `?`, and is not quoted, is replaced by the
  next value from `args`. The value is one argument. It is never pasted
  into the command text, so a value that contains spaces, quotes or
  newlines cannot change the command. `"?"` is a literal question mark.
  The command name cannot be a `?`, and neither can a subcommand such as
  the word after `CONFIG`. Every `?` needs a value, and every value must
  be used. A boolean or null is refused: pass a string or a number.
- One line is one round trip. Two or more lines are a pipeline. A pipeline
  is limited to 10000 commands. `transaction=true` wraps them in
  `MULTI`/`EXEC`.
- These commands are always allowed: `GET`, `MGET`, `STRLEN`, `GETRANGE`,
  `EXISTS`, `TYPE`, `TTL`, `PTTL`, `EXPIRETIME`, `PEXPIRETIME`, `RANDOMKEY`,
  `DBSIZE`, `SCAN`, `KEYS`, `DUMP`, `LCS`, `GETBIT`, `BITCOUNT`, `BITPOS`,
  `BITFIELD_RO`, `SORT_RO`, `HGET`, `HMGET`, `HGETALL`, `HEXISTS`, `HLEN`,
  `HKEYS`, `HVALS`, `HSTRLEN`, `HRANDFIELD`, `HSCAN`, `LINDEX`, `LLEN`,
  `LRANGE`, `LPOS`, `SISMEMBER`, `SMISMEMBER`, `SCARD`, `SMEMBERS`,
  `SRANDMEMBER`, `SSCAN`, `SINTER`, `SUNION`, `SDIFF`, `SINTERCARD`,
  `ZSCORE`, `ZMSCORE`, `ZCARD`, `ZCOUNT`, `ZLEXCOUNT`, `ZRANGE`,
  `ZRANGEBYSCORE`, `ZRANGEBYLEX`, `ZREVRANGE`, `ZREVRANGEBYSCORE`,
  `ZREVRANGEBYLEX`, `ZRANK`, `ZREVRANK`, `ZSCAN`, `ZRANDMEMBER`, `ZDIFF`,
  `ZINTER`, `ZUNION`, `ZINTERCARD`, `GEOPOS`, `GEODIST`, `GEOHASH`,
  `GEOSEARCH`, `GEORADIUS_RO`, `GEORADIUSBYMEMBER_RO`, `PFCOUNT`, `XLEN`,
  `XRANGE`, `XREVRANGE`, `XREAD`, `EVAL_RO`, `EVALSHA_RO`, `FCALL_RO`,
  `PING`, `ECHO`, `TIME`, `INFO`, `ROLE`, `LOLWUT`. These subcommands are
  reads too: `OBJECT ENCODING|FREQ|IDLETIME|REFCOUNT`, `MEMORY USAGE`,
  `COMMAND` with no subcommand, `COMMAND COUNT|INFO|DOCS|LIST|GETKEYS`, `SLOWLOG GET|LEN`,
  `PUBSUB CHANNELS|NUMSUB|NUMPAT|SHARDCHANNELS|SHARDNUMSUB`,
  `XINFO STREAM|GROUPS|CONSUMERS`, `ACL WHOAMI`.
- Anything else is a write and needs `allow_writes=true`. That includes
  `SET`, `DEL`, `EXPIRE`, `INCR`, `GETEX`, `GETDEL`, `TOUCH`, `PUBLISH`,
  `XADD`, `XREADGROUP`, `XACK`, `EVAL`, `EVALSHA`, `FCALL`, `WAIT` and
  blocking commands such as `BLPOP`. A command this list does not name is
  a write as well.
- These need `allow_writes` and `allow_admin`: `FLUSHALL`, `FLUSHDB`,
  `SWAPDB`, `SHUTDOWN`, `DEBUG`, `SAVE`, `BGSAVE`, `BGREWRITEAOF`,
  `REPLICAOF`, `SLAVEOF`, `FAILOVER`, `CLUSTER`, `MIGRATE`, `MODULE`,
  `ACL` (every subcommand except `WHOAMI`), `CONFIG GET|SET|REWRITE|RESETSTAT`,
  `SCRIPT FLUSH|KILL`, `FUNCTION FLUSH|DELETE|RESTORE|KILL`.
- These are refused even with both flags, because they change the
  connection or they are not available: `SELECT` (use `database`),
  `AUTH` (use `password_env`), `HELLO` (use `protocol`), `MULTI`, `EXEC`
  and `DISCARD` (use `transaction=true`), `WATCH`, `UNWATCH`, `SUBSCRIBE`
  and the other subscribe commands, `MONITOR`, `QUIT`, `CLIENT`, `RESET`,
  `READONLY`, `READWRITE`, `SYNC`, `PSYNC`, `REPLCONF`.
- The reply of a script has `value` (the last command), `values` (one entry
  per command, in order) and `rowCount` (how many elements the last reply
  has). A missing key is a successful `null`, and its `rowCount` is 0.
  `min_rows` counts every element. `expect` looks only at the elements a
  script keeps, which is `max_rows` of them (default 1000). A server error
  keeps its code word, such as `WRONGTYPE` or `NOPERM`. In a pipeline the
  error names the command by its first word only.
- One endpoint only. There is no Cluster and no Sentinel. A `MOVED` or
  `ASK` reply is a failed call. `SUBSCRIBE` is not available yet.
  `WATCH` is not available: use a Lua script with `allow_writes`, or a
  transaction. A whole reply is read into memory, so `KEYS *` or a huge
  `LRANGE` can use a lot of it. `SCAN` is the safe way to walk keys.
  Binary values are not exact in a script: a byte that is not valid text
  is replaced. A blocking command such as `BLPOP` needs `allow_writes`,
  and the call timeout has to be longer than the block time.
- The host allowlist and caps apply as for HTTP targets.
