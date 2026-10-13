---
title: "Test a MySQL or MariaDB database"
description: "The `mysql` driver runs one SQL text for each iteration, read from `-body`, and reads every row it returns."
sidebar_position: 6
---

# Test a MySQL or MariaDB database

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
vegaload run -target mysql://127.0.0.1:3306/app -protocol mysql \
  -body 'select id, total from orders where customer_id = 42' \
  -opt username=app -opt password_env=DB_PASSWORD -opt pool=20 -opt min_rows=1 \
  -vus 20 -duration 30s

# A parameterised statement: ? comes from -opt args.
vegaload run -target mysql://127.0.0.1:3306/app -protocol mysql \
  -body 'select * from orders where customer_id = ? and status = ?' \
  -opt 'args=[42, "paid"]' -opt username=app

# A transaction that writes. Several statements in one text work in the
# default mode, and writes need allow_writes.
vegaload run -target mysql://127.0.0.1:3306/app -protocol mysql \
  -body 'begin; update accounts set n = n + 1 where id = 1; select n from accounts where id = 1; commit' \
  -opt username=app -opt allow_writes=true -opt expect=1
```

The options are `username`, `password_env`, `database`, `tls`
(`preferred`, `false`, `true`, `skip-verify`), `pool`, `allow_writes`,
`query_mode`, `args`, `min_rows`, `expect` and `max_rows`.
`ca_file`, `cert_file`, `key_file` and `tls_server_name` are in
[TLS with your own CA](/guides/tls). With `tls=preferred` and
no `ca_file`, the certificate is not checked. With `ca_file`, it is
checked, and a server that does not offer TLS still uses plain text.

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
