---
title: "Test a PostgreSQL database"
description: "The `postgres` driver runs one SQL text for each iteration, read from `-body`, and reads every row it returns."
sidebar_position: 5
---

# Test a PostgreSQL database

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
vegaload run -target postgres://127.0.0.1:5432/app -protocol postgres \
  -body 'select id, total from orders where customer_id = 42' \
  -opt username=app -opt password_env=DB_PASSWORD -opt pool=20 -opt min_rows=1 \
  -vus 20 -duration 30s

# A parameterised, prepared statement: $1 and $2 come from -opt args.
vegaload run -target postgres://127.0.0.1:5432/app -protocol postgres \
  -body 'select * from orders where customer_id = $1 and status = $2' \
  -opt 'args=[42, "paid"]' -opt query_mode=extended -opt username=app

# A transaction that writes. Several statements in one text work in the
# default mode, and writes need allow_writes.
vegaload run -target postgres://127.0.0.1:5432/app -protocol postgres \
  -body 'begin; update accounts set n = n + 1 where id = 1; select n from accounts where id = 1; commit' \
  -opt username=app -opt allow_writes=true -opt expect=1
```

The options are `username`, `password_env`, `database`, `sslmode`
(`disable`, `prefer`, `require`, `verify-full`), `application_name`, `pool`,
`allow_writes`, `query_mode`, `args`, `min_rows`, `expect` and `max_rows`.
`ca_file`, `cert_file`, `key_file` and `tls_server_name` are in
[TLS with your own CA](/guides/tls). `ca_file` needs
`sslmode=verify-full`.

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
