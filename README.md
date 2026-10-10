# VegaLoad

VegaLoad is a thin, open-source load testing tool: a single static binary
with a scriptable core engine, protocol drivers (HTTP/1.1, HTTP/2, gRPC,
WebSocket, MQTT, Kafka, PostgreSQL, and raw TCP and UDP), and a self-contained HTML report — no server, no account,
no telemetry.

It's also agent-native: `vegaload init` registers VegaLoad as an MCP server
for Claude Code, Cursor, or any other MCP-capable agent host, so an agent
can run tests, read results, and explain failures through the same CLI
commands a human would type. It is never agent-*mandatory* — every feature
works from a plain terminal with nothing else installed. See `AGENTS.md` for
the design principle behind that split.

## Status

Phase 0 (core engine, CLI, protocols, scripting) and Phase 1 (the HTML/JSON
report) are done. Phase 2 (the MCP server, skill bundles, and a versioned
eval suite for the MCP tools) is also done — this README's walkthrough
covers all three. Since then, v0.3.0 and v0.4.0 added thresholds, checks,
`vegaload validate`, a baseline gate (`run -baseline`), JUnit output and a CI
job summary, and data files and env vars (`-data`, `-env`, `-secret-env`).
The main branch, which becomes v0.5.0, adds MCP over HTTP and SSE
(`mcp serve -http`), a second eval suite (`mcp eval -suite v2`), Windows
support, a Helm chart that runs a test as a Kubernetes Job with no CRD, and a
container image published to `ghcr.io/vegaload/vegaload`.
A Harness RT bridge (`--move-to-harness`) is intentionally out of scope for now.

## Install

With Homebrew (macOS and Linux), after the first tagged GitHub release:

```
brew install vegaload/tap/vegaload
vegaload version
```

`brew tap vegaload/tap` followed by `brew install vegaload` does the same
thing. Upgrade with `brew upgrade vegaload`. Until a tag exists, build from
source below.

Or build from source (Go 1.22+):

```
git clone https://github.com/vegaload/vegaload
cd vegaload
go build -o vegaload ./cmd/vegaload
./vegaload version
```

`go install github.com/vegaload/vegaload/cmd/vegaload@latest` also works
once the repository is public. Release archives for Linux, macOS and
Windows are attached to each [GitHub release](https://github.com/vegaload/vegaload/releases).

On Windows 10 or later, download the zip for your CPU from the release page,
unpack it, and run `vegaload.exe`. Put its folder on your `PATH` to run
`vegaload` from anywhere. `run`, `validate`, `doctor`, `init` and `mcp serve`
all work. Python scenarios need Python 3 from python.org (VegaLoad finds
`python3`, `python` or the `py` launcher). Scoop and winget packages are set
up in [RELEASING.md](./RELEASING.md) and come after the first Windows release.
A scratch-based Docker image is published to `ghcr.io/vegaload/vegaload` with
each release from v0.5.0 on (`linux/amd64` and `linux/arm64`):

```
docker run --rm ghcr.io/vegaload/vegaload:0.5.0 version
```

You can also build it yourself from the included `Dockerfile`
(`docker build .`). The image carries nothing but the binary
and CA certificates, so Python-scripted scenarios (which shell out to a
local `python3`) need a different base image — see the `Dockerfile`'s
comment.

## Getting started

For short recipes by task (ramp, CI gate, baseline, steps, data, and more),
see the [cookbook](./examples/cookbook).

Everything below uses [`examples/sample-app`](./examples/sample-app), a
small widgets service with injected latency and a ~3% failure rate on
creates, served over HTTP/1.1, HTTP/2, WebSocket, and gRPC, built
specifically so this walkthrough has something real to point `vegaload` at.
Start it in its own terminal first:

```
cd examples/sample-app
go run .
```

Leave it running, and do the rest from a second terminal at the repository
root.

### 1. Run a load test, no scenario file needed

Protocol-direct mode drives a target straight from CLI flags — useful for a
quick check, or for an agent that just got a URL and doesn't need to write a
script first:

```
./vegaload run -target http://127.0.0.1:8080/widgets -protocol http1 -vus 10 -duration 30s
```

This runs 10 virtual users against the sample app for 30 seconds, prints a
summary, and writes (and opens) a self-contained HTML report — one file,
with the full latency distribution and a requests/errors-over-time chart,
nothing else to host.

### Test a raw TCP or UDP service

For a service that speaks its own protocol, use the `tcp` or `udp` driver.
Each iteration opens a connection, sends the body, optionally checks the
reply, and closes. Driver settings go in repeatable `-opt key=value` flags.

```
# Does the service answer PING with PONG? Read until the reply contains PONG.
./vegaload run -target tcp://127.0.0.1:6379 -protocol tcp \
  -body 'PING\r\n' -opt expect=PONG -vus 10 -duration 20s

# Read exactly 8 bytes back, or read until a delimiter, or only connect.
./vegaload run -target tcp://127.0.0.1:7000 -protocol tcp -body 'hello\n' -opt read=8
./vegaload run -target tcp://127.0.0.1:25 -protocol tcp -opt until='\r\n' -opt expect=220
./vegaload run -target tcp://127.0.0.1:7000 -protocol tcp

# Send one UDP datagram. Add -opt reply=true or -opt expect=... to wait for an answer.
./vegaload run -target udp://127.0.0.1:514 -protocol udp -body '<13>test message\n'
```

The `tcp` options are `read`, `until`, `expect`, `max`, `tls` and `escape`.
The `udp` options are `reply`, `expect` and `escape`. In `-body`, `expect`
and `until`, the text may use `\n`, `\r`, `\t`, `\0`, `\\` and `\xNN`. Pass
`-opt escape=false` to send backslashes as they are. Use `-opt tls=true` for
a TLS service, with `-insecure` if its certificate is not trusted. A
misspelled option is an error, not ignored. The same host allowlist and
caps apply as for HTTP targets. Each iteration opens a new connection, so a
very high rate on one machine can run out of local ports. Keep the rate
moderate, or spread the load over more machines.

### Test an MQTT broker

The `mqtt` driver does one job per iteration: connect, do the job, close. The
job is set with `-opt mode=...`. The body is the message payload.

```
# Publish a message and wait for the broker's ack (the default mode).
./vegaload run -target mqtt://127.0.0.1:1883 -protocol mqtt -body 'hello' \
  -opt topic=load/test -opt qos=1 -vus 20 -duration 30s

# Publish, and wait until the same message comes back. This measures
# delivery through the broker. {id} is a unique client id for each iteration.
./vegaload run -target mqtt://127.0.0.1:1883 -protocol mqtt -body 'ping {id}' \
  -opt mode=roundtrip -opt topic='load/{id}' -opt qos=1

# Subscribe and wait for 5 messages that contain "ok".
./vegaload run -target mqtt://127.0.0.1:1883 -protocol mqtt \
  -opt mode=subscribe -opt topic='sensors/#' -opt count=5 -opt expect=ok
```

The options are `mode` (`publish`, `subscribe`, `roundtrip`), `topic`,
`qos`, `retain`, `username`, `password_env`, `client_id`, `count`, `expect`
and `keepalive`. Put the password in an environment variable and pass its
name with `-opt password_env=NAME` (it needs `username`), so it is not on
the command line. In `roundtrip` mode the body must contain `{id}`, so each
user can tell its own message from the messages of other users. Use `mqtts://` for TLS, with `-insecure` if the certificate is not
trusted. Every iteration uses a new client id, because a broker closes an
older connection that has the same id. The same host allowlist and caps
apply as for HTTP targets.

### Test a Kafka cluster

The `kafka` driver uses a pure Go client, so there is nothing to install.
The target is `kafka://host:9092`, or `kafkas://host:9093` for TLS. The job
of each iteration is set with `-opt mode=...`:

```
# Produce: send records and wait for the broker's ack (the default mode).
./vegaload run -target kafka://127.0.0.1:9092 -protocol kafka -body '{"order":1}' \
  -opt topic=orders -opt key='user-{id}' -opt acks=all -vus 20 -duration 30s

# Roundtrip: produce, then read exactly those records back.
./vegaload run -target kafka://127.0.0.1:9092 -protocol kafka -body 'ping {id}' \
  -opt mode=roundtrip -opt topic=orders -vus 10 -duration 30s

# Consume: read 5 records from the start of the topic and check their text.
./vegaload run -target kafka://127.0.0.1:9092 -protocol kafka \
  -opt mode=consume -opt topic=orders -opt count=5 -opt expect=order

# Admin: create and delete a topic, over and over. {id} makes each name new.
./vegaload run -target kafka://127.0.0.1:9092 -protocol kafka \
  -opt mode=admin -opt action=topic_lifecycle -opt topic='load-{id}' -opt partitions=3
```

The options are `mode` (`produce`, `consume`, `roundtrip`, `admin`), `topic`,
`key`, `acks` (`all`, `leader`, `none`), `compression` (`none`, `gzip`,
`snappy`, `lz4`, `zstd`), `count`, `expect`, `from` (`start`, `end`), `action`
(`list_topics`, `create_topic`, `delete_topic`, `topic_lifecycle`,
`list_groups`, `describe_cluster`), `partitions`, `replication`, `sasl`
(`plain`, `scram-sha-256`, `scram-sha-512`), `username`, `password_env` and
`client_id`. Put the SASL password in an environment variable and pass its
name with `-opt password_env=NAME`, so it is not on the command line. An
option that the chosen mode does not use is an error.

Some points to know:

- Produce, roundtrip and admin share one client between all users, like a
  real producer. Consume, and the reading half of roundtrip, open a new
  connection for each iteration, so that time is part of the result.
- Roundtrip reads back the exact partition and offset it wrote. It does not
  use consumer groups, so it does not measure group rebalancing. Consumer
  groups are not part of this driver yet.
- The first broker is the target you give. The client then connects to the
  broker addresses that the cluster announces. The host allowlist checks only
  the first address, so make sure the announced brokers are ones you may test.
- The topic must exist, unless the broker creates topics on its own. Use the
  admin `create_topic` action first.

### Test a PostgreSQL database

The `postgres` driver runs one SQL text for each iteration, read from `-body`,
and reads every row it returns. It uses a pure Go client, so there is nothing
to install. It is **read-only by default**: every transaction is read-only,
so a load test cannot change data by mistake. Add `-opt allow_writes=true`
to let the SQL write. The target is `postgres://host[:port][/database]` (or
`postgresql://`), with port 5432 by default. The URL may end with
`?sslmode=...` and `?application_name=...`, as in `psql`. Any other setting
after the `?` is refused, and a password there is refused too. It also works with servers that
speak the PostgreSQL protocol, such as CockroachDB, YugabyteDB and Aurora.

```
# One query, 20 users, for 30 seconds. The password is in $DB_PASSWORD.
./vegaload run -target postgres://127.0.0.1:5432/app -protocol postgres \
  -body 'select id, total from orders where customer_id = 42' \
  -opt username=app -opt password_env=DB_PASSWORD -opt pool=20 -opt min_rows=1 \
  -vus 20 -duration 30s

# A parameterised, prepared statement: $1 and $2 come from -opt args.
./vegaload run -target postgres://127.0.0.1:5432/app -protocol postgres \
  -body 'select * from orders where customer_id = $1 and status = $2' \
  -opt 'args=[42, "paid"]' -opt query_mode=extended -opt username=app

# A transaction that writes. Several statements in one text work in the
# default mode, and writes need allow_writes.
./vegaload run -target postgres://127.0.0.1:5432/app -protocol postgres \
  -body 'begin; update accounts set n = n + 1 where id = 1; select n from accounts where id = 1; commit' \
  -opt username=app -opt allow_writes=true -opt expect=1
```

The options are `username`, `password_env`, `database`, `sslmode`
(`disable`, `prefer`, `require`, `verify-full`), `application_name`, `pool`,
`allow_writes`, `query_mode`, `args`, `min_rows`, `expect` and `max_rows`.

- The connections are kept in a pool that all users share, like an
  application's pool. `pool` is the most connections to open (default 10).
  Waiting for a free connection counts as part of the measured time, so set
  `pool` to at least the number of users if you want to measure the server and
  not the queue.
- Put the password in an environment variable and pass its name with
  `-opt password_env=NAME` (it needs a user), so it is not on the command line.
  A password in the URL is refused. The standard `PGPASSWORD` variable and a
  `~/.pgpass` file work too, as they do for `psql`.
- `query_mode=simple` (the default) sends the SQL as one text, so it can hold
  several statements, such as a transaction. `query_mode=extended` prepares
  the statement once for each connection and reuses it, as most applications
  do. It runs one statement. With `args` in simple mode, VegaLoad puts the
  values into the text as quoted values (a value is never read as SQL), so
  several statements and `args` work together:
  `begin; update t set a = $2 where id = $1; select a from t where id = $1; commit`.
  The result is the last statement that returned rows. Each `$n` must have
  a value, and each value must be used.
- Read-only is the default. VegaLoad asks the server to make every transaction
  read-only, so an `insert`, `update`, `delete` or DDL statement fails with
  SQLSTATE 25006, and the error says how to allow it. `allow_writes=true`
  lets the SQL write. This is a safety net, not a lock: the SQL can still ask
  for a read-write transaction itself (`begin read write`), or turn the
  setting off and write in the same text (`set default_transaction_read_only
  = off; ...`). Each call puts the setting back before its SQL runs, so one
  call cannot leave writes on for the next. Use a read-only database role
  when you need a real lock.
- An iteration passes when the SQL ran without a server error and the result
  has at least `min_rows` rows. With `expect`, some value in the result must also contain the
  text (only the first `max_rows` rows are checked). A server error fails the
  iteration, and its message and SQLSTATE code are in the report.
- If a transaction fails halfway, the connection is closed and replaced, never
  reused. A timeout does the same.
- The host allowlist and caps apply as for HTTP targets. Load tests change
  data and use real server resources, so run them against a test database.

### Test a MySQL or MariaDB database

The `mysql` driver runs one SQL text for each iteration, read from `-body`,
and reads every row it returns. It uses a pure Go client, so there is nothing
to install. It is **read-only by default**: every new connection runs
`SET SESSION TRANSACTION READ ONLY`, so a load test cannot change data by
mistake. Add `-opt allow_writes=true` to let the SQL write. The target is
`mysql://host[:port][/database]` (or `mariadb://`), with port 3306 by
default. `-protocol` stays `mysql`. The URL may end with `?tls=...`. Any
other setting after the `?` is refused, and a password there is refused too.
It also works with servers that speak this protocol, such as MariaDB, TiDB,
Aurora MySQL and Percona Server.

```
# One query, 20 users, for 30 seconds. The password is in $DB_PASSWORD.
./vegaload run -target mysql://127.0.0.1:3306/app -protocol mysql \
  -body 'select id, total from orders where customer_id = 42' \
  -opt username=app -opt password_env=DB_PASSWORD -opt pool=20 -opt min_rows=1 \
  -vus 20 -duration 30s

# A parameterised statement: ? comes from -opt args.
./vegaload run -target mysql://127.0.0.1:3306/app -protocol mysql \
  -body 'select * from orders where customer_id = ? and status = ?' \
  -opt 'args=[42, "paid"]' -opt username=app

# A transaction that writes. Several statements in one text work in the
# default mode, and writes need allow_writes.
./vegaload run -target mysql://127.0.0.1:3306/app -protocol mysql \
  -body 'begin; update accounts set n = n + 1 where id = 1; select n from accounts where id = 1; commit' \
  -opt username=app -opt allow_writes=true -opt expect=1
```

The options are `username`, `password_env`, `database`, `tls`
(`preferred`, `false`, `true`, `skip-verify`), `pool`, `allow_writes`,
`query_mode`, `args`, `min_rows`, `expect` and `max_rows`.

- The connections are kept in a pool that all users share, like an
  application's pool. `pool` is the most connections to open (default 10).
  Waiting for a free connection counts as part of the measured time, so set
  `pool` to at least the number of users if you want to measure the server and
  not the queue.
- Put the password in an environment variable and pass its name with
  `-opt password_env=NAME` (it needs a user), so it is not on the command line.
  A password in the URL is refused.
- `tls=preferred` (the default) uses TLS when the server offers it, and plain
  text when it does not. `true` checks the certificate and the host name.
  With `-insecure`, `true` does not check the certificate. `false` stays on
  plain text. `skip-verify` uses TLS and does not check the certificate.
  MySQL 8's default login works with `tls=false`: the client asks the server
  for its public key. The password itself is only sent inside TLS when
  `tls=true` or `tls=skip-verify`.
- `query_mode=simple` (the default) sends the SQL as one text, so it can hold
  several statements, such as a transaction. `query_mode=prepared` prepares
  the statement once and reuses it, as most applications do. It runs one
  statement, and it does not keep a connection for the call. `set`, `use`,
  `lock`, `xa`, `unlock`, `begin` and `start` are refused in that mode.
  With `args` in simple mode, VegaLoad puts the values into the text as
  quoted values (a value is never read as SQL), so several statements and
  `args` work together:
  `begin; update t set a = ? where id = ?; select a from t where id = ?; commit`.
  The result is the last statement that returned rows. Each `?` must have a
  value, and each value must be used. A text of several statements reports
  `rowsAffected` as 0: that count is not available then. A single insert or
  update does report `rowsAffected`, and a single insert reports `lastInsertId`.
- Read-only is the default. An `insert`, `update`, `delete` or DDL statement
  fails, and the error says how to allow it. `allow_writes=true` lets the SQL
  write. This is a safety net, not a lock: the SQL can still ask for a
  read-write transaction itself (`START TRANSACTION READ WRITE`).
  `set session transaction read write; insert ...` in one text works too.
  The next call is blocked, because SET discards the connection. Use a
  read-only database role when you need a real lock.
- An iteration passes when the SQL ran without a server error and the result
  has at least `min_rows` rows. With `expect`, some value in the result must
  also contain the text (only the first `max_rows` rows are checked). A
  server error fails the iteration, and its message, error number and
  SQLSTATE are in the report.
- If a statement fails, the connection is closed and replaced, never reused.
  A timeout does the same. `LOAD DATA LOCAL INFILE` stays off.
- The host allowlist and caps apply as for HTTP targets. Load tests change
  data and use real server resources, so run them against a test database.

### Test a Redis or Valkey server

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
./vegaload run -target redis://127.0.0.1:6379 -protocol redis \
  -body 'GET user:42' \
  -opt password_env=REDIS_PASSWORD -opt pool=20 \
  -vus 20 -duration 30s

# A placeholder is one whole argument, even when the value has spaces.
./vegaload run -target redis://127.0.0.1:6379 -protocol redis \
  -body 'GET ?' -opt 'args=["user:42"]'

# A pipeline that writes. Both lines need allow_writes.
./vegaload run -target redis://127.0.0.1:6379 -protocol redis \
  -body $'INCR hits:42\nEXPIRE hits:42 60' \
  -opt allow_writes=true
```

The options are `username`, `password_env`, `database`, `tls`
(`false`, `true`, `skip-verify`), `pool`, `protocol` (`2` or `3`),
`allow_writes`, `allow_admin`, `transaction`, `args`, `min_rows`, `expect`
and `max_rows`.

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
  the certificate. `tls=false` with `rediss://` is an error.
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

### 2. Or write a scenario file

```
./vegaload new my-scenario
```

scaffolds `my-scenario.vl.js` (JavaScript by default; `-python` for a
`.py` scenario shelling out to a local `python3`). A scenario's `http` and
`ws` globals make real HTTP and WebSocket calls, carrying a value from one
into the next — a script can create something over HTTP, then open a
WebSocket and read frames until it's done, the way
[`examples/scenarios/http-ws-chain.vl.js`](./examples/scenarios/http-ws-chain.vl.js)
does against the sample app (its Python twin,
[`http_ws_chain.py`](./examples/scenarios/http_ws_chain.py), does the same
thing):

```
./vegaload run -vus 5 -duration 10s examples/scenarios/http-ws-chain.vl.js
```

(flags before the scenario file — `vegaload`'s flag parser stops at the
first non-flag argument; run `vegaload run -h` for the full flag list.) A
host outside localhost needs `-allow-target` or `-yes` — the same FR-CLI-06
allowlist protocol-direct mode's `-target` uses, just enforced per call
since a script's own targets aren't known until it runs. A scenario with no
network calls at all is still useful for pure per-iteration logic against
VegaLoad's VU pool and executor shapes (fixed-VU, ramp, step,
constant-arrival-rate) —
[`examples/scenarios/smoke.vl.js`](./examples/scenarios/smoke.vl.js) is a
minimal one:

```
./vegaload run -vus 5 -duration 5s examples/scenarios/smoke.vl.js
```

To load test an actual HTTP target without writing a script at all, use
protocol-direct mode (step 1) instead — or generate a runbook of
protocol-direct commands from an OpenAPI spec:

```
./vegaload new -from-openapi examples/sample-app/openapi.json sample-app
```

writes two files. `sample-app.vl.js` is a runnable scenario that calls every
operation of the spec once per iteration, each as a named step (add `-python`
for a Python scenario; flags go before the name). It sends the example bodies
and required query parameters from the spec, reads a credential from `env`
(give it with `-secret-env`), and uses the id that a create returns in the calls
under it. A spec describes each endpoint on its own, so the order (creates,
reads, updates, deletes) is a guess: the file says so, and marks the ids and
other values it could not fill. Check it with `vegaload validate` and edit it to
fit your real flow. `sample-app.vegaload-plan.md` is a runbook with one
ready-to-run `vegaload run` command per endpoint, for loading one endpoint at a
time.

### Call TCP, UDP, MQTT, Kafka, gRPC, PostgreSQL and MySQL from a scenario

A JavaScript scenario can also use the `tcp`, `udp`, `mqtt`, `kafka`, `grpc`, `postgres`, `mysql` and `redis` globals. Each
function is one call that does one job and returns what it read:

```js
export default function () {
  const r = tcp.send("tcp://localhost:6379", { body: "PING\r\n", until: "\r\n" });
  check(r, { "got PONG": (x) => x.ok && x.body === "+PONG\r\n" });

  mqtt.publish("mqtt://localhost", { topic: "sensors/1", body: "21.5", qos: 1 });
  const m = mqtt.subscribe("mqtt://localhost", { topic: "sensors/#", count: 2, timeout: "5s" });
  console.log(m.messages.map((x) => x.topic + "=" + x.body).join(", "));
}
```

- `tcp.send(url, options)` and `udp.send(url, options)`: the options are the
  same as the `-opt` keys in the TCP and UDP sections above (`until`, `read`,
  `expect`, `tls`, `reply`, and so on), plus `body`. The reply is `r.body`.
  A script's text is sent as written: backslash escapes are off unless you
  pass `escape: true`.
- `mqtt.publish`, `mqtt.subscribe` and `mqtt.roundtrip` take `topic`, `body`
  and the other MQTT `-opt` keys (not `mode`: the function sets it). The
  messages that were read are in `r.messages`, each with `topic` and `body`.
  Give a password as `password: env.MQTT_PASSWORD` and run with
  `-secret-env MQTT_PASSWORD`, so it is removed from every output.
- `kafka.produce`, `kafka.consume`, `kafka.roundtrip` and `kafka.admin` take
  the Kafka `-opt` keys (`topic`, `key`, `count`, `expect`, `from`, `action`,
  `sasl`, `username`, `acks`, and so on; not `mode`) plus `value` (the record
  value, `body` means the same). The records are in `r.records`, each with
  `topic`, `partition`, `offset`, `key` and `value`. For `produce`, the
  partition and offset say where the broker stored the record. An admin
  answer is in `r.text`. A Kafka client is made on the first call and kept
  for the rest of that virtual user's run, so `produce` does not connect again
  for every call. `consume` and `roundtrip` read with their own connection,
  as in the load-test mode. A SASL password is `password: env.NAME`.
  The safety allowlist checks the first broker you name, not the other
  brokers the cluster announces. The kept client stops at the longest timeout
  it was built with (the first call's `timeout`, and at least one minute), so a
  single Kafka call cannot run longer than that.
- `grpc.call(url, options)` makes one unary gRPC call. The options are
  `method` (required, such as `"/package.Service/Method"`), `body` (an object,
  or JSON text; leave it out for an empty message) and `headers` (an object,
  sent as gRPC metadata). The reply has `status` (the gRPC code, 0 when it
  worked), `statusName` (such as `"NotFound"`), `body` (the reply as JSON
  text) and `json` (the same, already parsed). The script needs no `.proto`
  file: the first call to a method asks the server for its message types
  (server reflection), and the answer is kept. A JSON body needs the server to
  offer reflection. Sent to a server without it, the call throws. Call such a
  server with `encoding: "base64"`: `body` and the reply are then the encoded
  message in base64. A server's error status is a reply with `ok` false, not
  an exception. Only unary methods work for now. `url` is `host:port`,
  `grpc://host:port` or `grpcs://host:port` for TLS. One connection is made on
  the first call and kept for the rest of that virtual user's run.
- `postgres.query(url, options)` runs one SQL text, given as `body`, and takes
  the other PostgreSQL `-opt` keys (`username`, `database`, `sslmode`, `args`,
  `min_rows`, `expect`, `max_rows`, `query_mode`, `allow_writes`, `pool`). `args`
  is a list: `args: [42, "paid"]`. The reply has `rows` (a list of objects, one
  per row, keyed by column name, up to `max_rows`), `columns` (the names, in
  order), `rowCount` (all the rows, also those past `max_rows`),
  `rowsAffected` and `commandTag` (such as `"SELECT 2"`). Numbers, text,
  booleans and NULL (`null`) keep their type, and JSON columns are parsed.
  Times are text in UTC (RFC 3339), and `numeric`, `uuid` and byte columns are
  text, so no precision is lost. If two columns have the same name, the last
  one wins: give them different names with `as`. Give the password as
  `password: env.DB_PASSWORD` and run with `-secret-env DB_PASSWORD`. A pool
  with one connection is made on the first call and kept for the rest of that
  virtual user's run, so each call does not connect again. A server error
  (a bad SQL, a failed constraint) is a reply with `ok` false and the
  SQLSTATE code in `error`, not an exception. Scripts are read-only like the
  command line: a script that writes passes `allow_writes: true`.
- `mysql.query(url, options)` runs one SQL text, given as `body`, and takes
  the other MySQL `-opt` keys (`username`, `database`, `tls`, `args`,
  `min_rows`, `expect`, `max_rows`, `query_mode`, `allow_writes`, `pool`).
  Placeholders are `?`, and `args` is a list: `args: [42, "paid"]`. The reply
  has `rows`, `columns`, `rowCount` and `rowsAffected`, the same as
  PostgreSQL, plus `lastInsertId` for a single insert. It has no
  `commandTag`. A text of several statements reports `rowsAffected` as 0.
  Give the password as `password: env.DB_PASSWORD` and run with
  `-secret-env DB_PASSWORD`. A pool with one connection is made on the first
  call and kept for the rest of that virtual user's run. A server error is a
  reply with `ok` false, and the error number and SQLSTATE are in `error`.
  Scripts are read-only like the command line: a script that writes passes
  `allow_writes: true`. `url` may be `mysql://` or `mariadb://`.
- `redis.command(url, options)` runs one Redis command, given as `body`, or
  several, one per line. It takes the other Redis `-opt` keys (`username`,
  `database`, `tls`, `pool`, `protocol`, `allow_writes`, `allow_admin`,
  `transaction`, `args`, `min_rows`, `expect`, `max_rows`). `args` is a
  list: `args: ["user:42", "ann"]`. A `?` is one whole argument. The reply
  has `value` (the last command), `values` (one entry per command) and
  `rowCount`. In Python, `r.values` is that list (and `r["values"]` is the
  same). It has no `rows`, `columns`, `rowsAffected`, `commandTag` or
  `lastInsertId`. Give the password as `password: env.REDIS_PASSWORD` and
  run with `-secret-env REDIS_PASSWORD`. A client with one connection is
  made on the first call and kept for the rest of that virtual user's run.
  A server error, and a command the read-only check refuses, is a reply
  with `ok` false, not an exception. A script that writes passes
  `allow_writes: true` in the connection options. Admin commands also pass
  `allow_admin: true`. `url` may be `redis://` or `rediss://`.
- Every function also accepts `insecure` (skip TLS checks) and `timeout`
  (milliseconds, or text such as `"2s"`).
- Every call returns `{ok, error, bytesSent, bytesReceived, body, messages}`. Kafka calls also return `records` and `text`, gRPC calls return `status`, `statusName` and `json`, PostgreSQL calls return `rows`, `columns`, `rowCount`, `rowsAffected` and `commandTag`, MySQL calls return `rows`, `columns`, `rowCount`, `rowsAffected` and `lastInsertId`, and Redis calls return `value`, `values` and `rowCount`.
  A network failure does not throw: `ok` is false and `error` says why. A
  call that is set up wrongly (an unknown option, a missing topic) does throw.
- The safety allowlist is checked for the host of every call, like `http`.
- Each call opens its own connection and closes it, like the load-test mode.
  For `mqtt.subscribe`, the message must arrive during the call, so run it
  when something else publishes.

Python scenarios have the same globals, with the same options and the
same reply. The options are keyword arguments, the reply works as
`r.ok` and as `r["ok"]`, and a word that Python reserves gets a trailing
underscore (`from_="end"`):

```python
def iteration():
    r = tcp.send("tcp://localhost:6379", body="PING\r\n", until="\r\n")
    check(r, {"got PONG": lambda x: x.ok and x.body == "+PONG\r\n"})
    k = kafka.produce("kafka://localhost", topic="orders", key="k1", value="v")
    print(k.records[0].partition, k.records[0].offset)
    g = grpc.call("localhost:50051", method="/pkg.Greeter/SayHello", body={"name": "x"})
    check(g, {"greeted": lambda x: x.ok and x.json.message == "Hello x"})
```

`grpc.call` sends and reads JSON, which needs server reflection on the
server. Without it, the call throws. For such a server use
`encoding="base64"`, and send the encoded message in base64.

A call set up wrongly raises `VegaloadError`.

[`examples/scenarios/mqtt-tcp.vl.js`](./examples/scenarios/mqtt-tcp.vl.js)
(and its Python twin, [`mqtt_tcp.py`](./examples/scenarios/mqtt_tcp.py))
is a small example, and
[`examples/scenarios/kafka-orders.vl.js`](./examples/scenarios/kafka-orders.vl.js)
shows Kafka, and
[`examples/scenarios/mixed-protocols.vl.js`](./examples/scenarios/mixed-protocols.vl.js)
(Python: [`mixed_protocols.py`](./examples/scenarios/mixed_protocols.py)) mixes
HTTP, gRPC and WebSocket in one flow, and
[`examples/scenarios/postgres-orders.vl.js`](./examples/scenarios/postgres-orders.vl.js)
(Python: [`postgres_orders.py`](./examples/scenarios/postgres_orders.py))
writes and reads rows in PostgreSQL, and
[`examples/scenarios/mysql-orders.vl.js`](./examples/scenarios/mysql-orders.vl.js)
(Python: [`mysql_orders.py`](./examples/scenarios/mysql_orders.py))
reads and writes a MySQL row.
[`examples/scenarios/redis-cache.vl.js`](./examples/scenarios/redis-cache.vl.js)
(Python: [`redis_cache.py`](./examples/scenarios/redis_cache.py))
reads a cache key, fills it on a miss, and updates a counter.

### Start from a browser recording

Record a real session in your browser (the network tab can save it as a
HAR file), then turn it into a scenario:

```
./vegaload import har recording.har -o checkout.vl.js
./vegaload validate -secret-env VL_COOKIE checkout.vl.js
```

The importer makes a first draft, and you edit it. It keeps the real calls,
in the order they were recorded, one `http` call each, with a check on the
status the browser got. It leaves out images, fonts, style sheets and
scripts, requests to other sites (analytics, ads, CDNs), CORS preflight
requests, and requests that failed. "Other sites" means sites other than the
one of the first page you opened in the recording. Use `-include-static`,
`-include-third-party` or `-host` to change that, and `-max N` to stop after
N requests.

The importer keeps the secrets it can recognise out of the file. A `Cookie`
or `Authorization` header, any header, query parameter or body field whose
name looks secret (password, token, api key, session, csrf and similar), and
any value that is a JWT, is read from the environment as `env.VL_NAME`. The
file lists the variables, and you pass each one by name with `-secret-env`.
It works on names and on the shape of a JWT, so a secret with an ordinary
name stays as it was recorded: a token in a path such as `/reset/<token>`, a
query parameter named `code`, or a field named `key`. Read the file before
you share it, and do not commit the HAR file. The importer also drops headers a client
sets by itself (`User-Agent`, `Content-Length`, `Referer`, `Origin`, `Sec-*`
and similar).

Some things are left for you, and the file marks them with `TODO` lines:

- A value that looks like it changes on every run (a UUID, a long number, a
  token). When the same value was in the answer to an earlier request, the
  note says which request, so you can carry it forward with `r2.json()`.
- A multipart body. It is left out.
- Waits between requests, and cookies that an answer sets. A scenario has no
  sleep and no cookie jar, so pass the `Cookie` header as a secret.

The hosts in the file are real. A host that is not localhost needs
`-allow-target` on the run, and the file's first lines say which.

### Check a scenario before a real run

```
./vegaload validate examples/scenarios/crud-flow.vl.js
```

`validate` runs the scenario once, with one user and one iteration, and says
whether it works. It catches a script that will not load (a syntax error, a
missing file, no `iteration()` in Python) and an iteration that fails (a
wrong URL, a thrown error, a host that is not allowed). It makes real
network calls, under the same host rules as `run`, so a host that is not
localhost needs `-allow-target` or `-yes`. Any `check()` results are listed,
and a failed check does not make the scenario invalid. Add `-output json`
for a machine-readable result.

| Exit code | Meaning                          |
|-----------|----------------------------------|
| 0         | The scenario loaded and its iteration ran |
| 1         | The scenario is not valid        |
| 2         | Bad usage                        |

From an agent, the `validate_scenario` tool does the same and returns
`valid`, and for an invalid scenario the `stage` (`load` or `iteration`) and
`error`.

### Feed a scenario data and settings

A scenario can read rows from a file and settings from the environment, so
each virtual user can act on different data.

```
API_KEY=demo-key ./vegaload run -vus 5 -duration 30s \
  -data users.csv -env REGION -secret-env API_KEY scenario.vl.js
```

- `-data FILE` is a CSV file (the first line names the columns) or a JSON
  file (an array of objects). The script reads it as `data.NAME`, where
  NAME is the file name without its extension: `users.csv` is `data.users`.
  Give another name with `-data name=path`. The flag can be repeated.
  `data.users.next()` takes the next row in file order, shared by all
  users, and starts again after the last row. `data.users.random()` takes
  any row. `data.users.length` is the number of rows. CSV values are
  strings. JSON values keep their types. In Python, use `len(data.users)`.
- `-env NAME` lets the script read the environment variable NAME as
  `env.NAME`. A script can only read the variables you name. It cannot see
  the rest of your environment. A variable that is not set is a usage error
  (exit 2), before any load is sent.
- `-secret-env NAME` is the same, and the value is a secret. It is taken out
  of the summary, the JSON output, the HTML report, the audit log and
  `console.log` output. Use it for tokens and passwords.

```js
const row = data.users.next();
http.post(url, { body: JSON.stringify({ user: row.name }),
                 headers: { Authorization: "Bearer " + env.API_TOKEN } });
```

```python
row = data.users.next()
http.post(url, body=json.dumps({"user": row["name"]}),
          headers={"Authorization": "Bearer " + env.API_TOKEN})
```

Secrets are removed by matching the value in text VegaLoad writes: check
names, error messages, the audit log and console output. A script that
changes a secret first, for example by encoding it, can still show the
changed text. Python scenarios are ordinary Python programs, so they can
also read the whole process environment through `os.environ`; only the
`env` object is limited to the names you give. `validate` takes the same
flags as `run`. From an agent, `run_test` and `validate_scenario` take
`data_files`, `env` and `secret_env`. See `examples/scenarios/data-env.vl.js`.

### 3. Explain a run's results

Keep a run's JSON summary alongside its HTML report with `-out`:

```
./vegaload run -target http://127.0.0.1:8080/widgets -protocol http1 \
  -vus 10 -duration 30s -out baseline.json
./vegaload diagnose baseline.json
```

`diagnose` reports the failure rate, flags a latency long tail (p99 more
than 5x p50), and says whether failures were concentrated in one period or
spread out — plus suggested pass/fail thresholds (a p95 ceiling, an error
rate ceiling) derived from that same run, for writing into a CI gate. Add a
bring-your-own-LLM connector for a plain-English narrative on top of the
rule-based findings:

```
export VEGALOAD_LLM_PROVIDER=openai       # or anthropic, ollama
export VEGALOAD_LLM_API_KEY=sk-...
./vegaload diagnose baseline.json
```

Nothing about a run or its results leaves your machine unless you configure
this yourself (`-no-llm` skips narration even if it's configured).

### 4. Compare a later run against the baseline

After another run with `-out candidate.json`:

```
./vegaload compare baseline.json candidate.json
```

Prints deltas (error rate, latency percentiles, totals, overall RPS) and
exits non-zero if error rate or p95 latency got worse. If the scenario makes
`check()` calls, it also lists each check's pass rate in both runs, and a
check whose pass rate fell counts as a regression too. A check that only one
run made is shown as `new` or `removed`, and does not fail the comparison.
The `check_rate` row is the plain average of the shared checks. It is for
reading and never fails the comparison on its own.
Optional slack:

```
./vegaload compare -error-rate-delta 0.01 -p95-ratio 1.2 -check-rate-delta 0.02 \
  baseline.json candidate.json
```

Checked-in fixtures under `examples/scenarios/compare/` show an ok and a
regressed pair, with and without checks, without needing a live target.

Or do it in one command, with the baseline as a file you keep:

```
./vegaload run -target http://127.0.0.1:8080/widgets -protocol http1 \
  -vus 10 -duration 30s -baseline baseline.json -max-regression 10
```

The run exits 3 if p95 latency, or the error rate, is worse than the
baseline by more than 10 percent. It uses the same comparison as
`vegaload compare`. The error rate may rise by that percent of the
baseline's error rate, so a baseline with no errors allows none. Use a
`-threshold "error_rate < 1%"` for an absolute limit. The `-baseline` gate does not look at checks; use
`-threshold "check_rate >= 99%"` for those. Without
`-max-regression`, any increase fails. VegaLoad keeps no baseline store.
The baseline is a file you supply, from `-out`. The summary, JSON, HTML
report and audit log show the verdict. From an agent, `run_test` takes
`baseline_path` and `max_regression`.

### Gate a run on pass/fail thresholds

Add `-threshold` to make a run pass or fail on its own numbers, for example
in CI:

```
./vegaload run -target http://127.0.0.1:8080/widgets -protocol http1 \
  -vus 10 -duration 30s \
  -threshold "p95 < 300ms" \
  -threshold "api stays up: error_rate < 1%"
```

Each threshold is an optional `name:`, a metric, an operator (`<`, `<=`,
`>`, `>=`) and a value. The metrics are `p50`, `p90`, `p95`, `p99`, `mean`,
`min`, `max` (durations such as `300ms`), `error_rate` (a fraction like
`0.01` or a percentage like `1%`), `rps`, `failed`, `total` and `check_rate` (the share of `check()` calls
that passed; see below). A threshold can
target one named step: `p95{step="login"} < 300ms` (see Steps below;
`check_rate` cannot). Every
threshold is judged once, on the finished run. A run that completed no
requests fails all of them.

The text summary, the JSON output, the HTML report and the audit log all
show each threshold's verdict. The exit code tells a script what happened:

| Exit code | Meaning                                                  |
|-----------|----------------------------------------------------------|
| 0         | The run finished and every threshold passed (or none set) |
| 1         | The run itself failed                                    |
| 2         | Bad usage, such as a threshold that cannot be parsed     |
| 3         | The run finished but broke at least one threshold        |

#### Stop early on a breach

Add `-abort-on-breach` and the run stops as soon as a threshold is broken
beyond recovery. It exits 3, like any breach, and saves the time and load
of a run that has already failed. The summary, JSON, HTML report and GitHub
job summary say that the run was aborted, at what time, and by which
threshold.

```
vegaload run -vus 50 -duration 10m -abort-on-breach \
  -threshold "failed < 20" -threshold "p95 < 300ms" -threshold "error_rate < 1%" scenario.vl.js
```

Two kinds of threshold are judged while the run goes on:

- A threshold that cannot recover, such as `failed < 20`, `max < 2s` or
  `total < 5000`, stops the run at once.
- A statistic that moves up and down, such as `p95`, `error_rate`, `mean`
  or `check_rate` (also for one step), stops the run only if it stays
  broken for three seconds in a row. It is not judged during a warm-up (5
  seconds, or a quarter of the run if that is shorter; change it with
  `-abort-grace`; `-abort-grace 0` turns it off), and not until it has at least 20 samples.

A threshold that needs the whole run, such as `rps >= 100` or
`total >= 1000`, is judged at the end, on what ran before the stop. The run
tells you which ones before it starts. `-abort-on-breach` needs at least
one threshold. From an agent, `run_test` takes `abort_on_breach` and
`abort_grace`.

#### Checks

In a scenario script, `check(value, {name: test})` counts named assertions
without failing the iteration. Each test is a function or a boolean; a test
that throws counts as failed. `check` returns true only if all tests passed.

```js
check(res, { "status is 200": (r) => r.status === 200 });
```

In Python the tests are callables or bools, and a test that raises counts as
failed. The summary, JSON, and HTML report list each check's passes and
fails. Gate on them with `-threshold "check_rate >= 99%"`. A run with no
checks fails that threshold. Use fixed check names. A run keeps at most 100 distinct
names; any more are counted together as `(other checks)`. See
`examples/scenarios/checks.vl.js`.

#### Steps

Name the stages of a flow with `step(name, fn)` and the summary, the JSON
output, the HTML report and the GitHub step summary show the latency and
error rate of each one, so you can see which stage is slow.

```js
const token = step("login", () => http.post(base + "/login", { body }).json().token);
step("checkout", () => { /* ... */ });
```

In Python, `with step("login"):` times a block, and `step("login", fn)` runs
a function and returns its value. A step that throws (or raises) is a failed
step, and the error carries on, so the iteration fails too. Steps can nest;
each is counted on its own. Gate on one step with a selector:
`-threshold 'p95{step="login"} < 300ms'`. A step that never ran fails its
threshold. Step names with a quote mark in them cannot be targeted. A run
keeps at most 100 distinct step names; any more are counted together as
`(other steps)`. See `examples/scenarios/crud-flow.vl.js`.

#### JUnit XML for CI

`-junit results.xml` also writes the verdicts as JUnit XML, so a CI system
can show them as test results. Each threshold, each check and the baseline
gate is one test case. A failed threshold, a check that failed even once,
or a baseline the run is worse than is a failed test case. A failed check
does not change the exit code of `vegaload run`, but it does show as a
failed test case, because the report says what happened and the exit code
is the gate. A run with none of these writes an empty suite.

Thresholds can also come from a file, with `-thresholds gate.json`. The file
is a JSON list of `{"name", "metric", "operator", "value"}`, or the output of
`vegaload diagnose -output json`, so a baseline run can set the bar for the
next ones:

```
./vegaload diagnose -no-llm -output json baseline.json > gate.json
./vegaload run -target http://127.0.0.1:8080/widgets -protocol http1 \
  -vus 10 -duration 30s -thresholds gate.json
```

`diagnose` also prints the same suggestion as ready-to-paste `-threshold`
flags. From an agent, `run_test` takes a `thresholds` list; a breach comes
back as a normal result with `thresholds_passed: false`.

### 5. Hand all of this to an agent

```
./vegaload init
```

writes six files into the current project, each skippable if already
present: two Claude Code skills (`.claude/skills/vegaload` for real load tests
and `.claude/skills/vegaload-smoke` for a quick ten-second check), two Cursor
rules files (`.cursor/rules/vegaload.mdc` and `.cursor/rules/vegaload-smoke.mdc`),
and an MCP server entry merged
into both `.mcp.json` and `.cursor/mcp.json`, pointing at this same compiled
binary running `vegaload mcp serve`. Open the project in Claude Code or
Cursor afterward and the agent has eight tools — `create_scenario`,
`run_test`, `get_results`, `suggest_thresholds`, `diagnose_failure`,
`compare_reports`, `generate_from_spec`, `validate_scenario` — each one calling the exact CLI
command shown above and parsing its `-output json` result; there is no
agent-only path that skips the CLI.

To set up only one editor, use `-editor`:

```
./vegaload init -editor cursor
./vegaload init -editor claude-code,cursor
```

To see what is set up, without changing anything, use `-status`:

```
./vegaload init -status
```

It lists each file as `ok`, `missing`, `outdated` (the rules file differs from
the one this binary ships), `other binary` (the MCP entry points to another
copy of `vegaload`), `wrong args` (the MCP entry does not run `mcp serve`) or
`invalid` (the file cannot be read). It exits 1 if anything is not `ok`, so a script can check
it. Run `vegaload init -force` to fix what it reports.

By default the MCP server talks over stdio. To use it over the network (for
example from a container), run it over HTTP:

```
vegaload mcp serve -http 127.0.0.1:8765
```

`POST /mcp` takes one JSON-RPC message and returns the answer. `GET /sse` opens
a Server-Sent Events stream (MCP protocol 2024-11-05) and `POST /message` sends
requests to it. On a loopback address no token is needed. On any other address
a token is required: put it in an environment variable and pass its name, for
example `-token-env VEGALOAD_MCP_TOKEN`. Clients then send
`Authorization: Bearer <token>`. Browser requests are refused unless their
Origin is a loopback host or the one given with `-allow-origin`. HTTP is off by
default, and stays off in the Docker image unless you pass `-http`.

`vegaload mcp eval` runs a versioned, non-LLM suite of {tool call, expected
outcome} cases against those same tools directly — the thing to run in
CI after upgrading, to check the tool layer itself still behaves, independent
of any model's tool-picking behavior:

```
./vegaload mcp eval
```

There are two suites. `v1` is the default and never changes. `v2` keeps every
v1 case and adds cases for `validate_scenario`, the baseline gate, JUnit
output and checks. Run it with `./vegaload mcp eval -suite v2`.

### 5. Check that everything is wired up

```
./vegaload doctor
```

checks that VegaLoad works from here and says how to fix anything that does
not. It looks at the CLI itself (version, `PATH`, writable folders), at each
agent host it finds (Cursor, Claude Code, and Claude Desktop on macOS), and
optionally at a target. For a host that is installed but has no VegaLoad
MCP entry, that is a warning, not a failure — using the CLI alone is
healthy. When VegaLoad *is* registered, doctor starts the configured
server, performs a real MCP handshake, and expects all eight tools. An
installed editor with no VegaLoad setup does not fail the command.

```
./vegaload doctor -target http://localhost:8080   # also check a target
./vegaload doctor -fix                            # repair what can be repaired safely
./vegaload doctor -fix -dry-run                   # show what -fix would change
./vegaload doctor -output json                    # for CI; exits 1 if any check fails
```

`-fix` only adds missing MCP entries and rules files in the project, and
rewrites a stale server command. It never overwrites a rules file you edited,
and it changes files in your home directory only when you name the host, as in
`-host cursor`. `-smoke` adds a one-user, one-second test against `-target`
(it sends real traffic, so it is off by default). `-harness` is a placeholder
until `--move-to-harness` ships: it makes no network call and cannot verify
credentials.

See [`examples/scenarios/README.md`](./examples/scenarios/README.md) for
this same walkthrough as a standalone, copy-pasteable script.

## Command reference

| Command                 | What it does                                                          |
|--------------------------|------------------------------------------------------------------------|
| `vegaload run`           | Run a load test: a scenario file, or a protocol-direct target         |
| `vegaload validate`      | Run a scenario once, with one user, to check that it works            |
| `vegaload new`           | Scaffold a starter scenario file, or a runbook from an OpenAPI spec    |
| `vegaload import har`    | Make a scenario file from a browser recording (a HAR file)             |
| `vegaload diagnose`      | Print environment info, or explain a report's results                |
| `vegaload mcp serve`     | Run an MCP server over stdio (or `-http addr`) for agent-native use   |
| `vegaload mcp eval`      | Run the versioned MCP tool-calling eval suite against this binary     |
| `vegaload init`          | Register the MCP server and skill bundles (`-editor`, `-status`)      |
| `vegaload doctor`        | Check the CLI, agent hosts, and a target; `-fix` repairs what it can   |

Every command supports `-output text` (default), `json`, or (`run` only)
`jsonl`. Run `vegaload <command> -h` for its full flag list, or `vegaload
help` for the top-level summary.

## Use in Kubernetes

A Helm chart in [`charts/vegaload`](./charts/vegaload) runs a load test as a
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

A scenario file works too: `--set-file scenario=./checkout.vl.js`. A broken
threshold fails the Job. See the chart's README. For plain `kubectl`, use
[`examples/k8s/job.yaml`](./examples/k8s/job.yaml).

## Use in GitHub Actions

This repository is also a GitHub Action. It installs a released VegaLoad
(Linux and macOS runners) and runs it, and the step fails when `vegaload`
exits with a non-zero code. With `-threshold`, that means a broken
threshold fails the build:

```yaml
- uses: vegaload/vegaload@v0.3.0
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
- uses: vegaload/vegaload@v0.3.0
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

## License

Apache-2.0. See `LICENSE`.