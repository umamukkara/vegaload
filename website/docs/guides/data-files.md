---
title: "Feed a scenario data and settings"
description: "A scenario can read rows from a file and settings from the environment, so each virtual user can act on different data."
sidebar_position: 14
---

# Feed a scenario data and settings

A scenario can read rows from a file and settings from the environment, so
each virtual user can act on different data.

```
API_KEY=demo-key vegaload run -vus 5 -duration 30s \
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

## Data files with -protocol

A scenario reads a data file as `data.NAME.next()`. With `-target` and
`-protocol`, the same file fills `{data.NAME.COLUMN}` in `-body` and in the
`-opt` values that describe one call.

Each call takes one row from each file it names. Every placeholder for that
file in that call uses the same row. `-data-pick next` (the default) walks
the file in order, shared by every user, and starts again at the top.
`-data-pick random` picks any row of the file.

NAME is the file name without its extension, or the name you give as
`name=path`. COLUMN is a CSV column or a JSON key. Both are letters, digits
and `_`, and do not start with a digit. Text that is not exactly
`{data.NAME.COLUMN}` is left as it was written. `{id}` is still a fresh
value on each call.

A CSV cell is text. A JSON number stays a number. In `-opt args`, a
placeholder with no suffix is written as JSON (`"42"` from a CSV cell, `42`
from a JSON number). Add `:int` or `:float` to turn a text cell into a
number. Anywhere else the cell is inserted as text. No other suffix is a
placeholder.

PostgreSQL and MySQL do not take a placeholder in the SQL. Put the value in
`-opt args`, so it is sent as a parameter.

```
vegaload run -target postgres://127.0.0.1:5432/app -protocol postgres \
  -data examples/data/users.csv \
  -opt username=app -opt password_env=PGPASS \
  -body 'SELECT name, plan FROM users WHERE id = $1' \
  -opt 'args=[{data.users.id:int}]' \
  -vus 20 -duration 30s
```

This works with postgres, mysql, redis, ftp, sftp, kafka and rabbitmq. A
placeholder is not allowed in a connection option, a mode, a password, or a
TLS option. `-env` and `-secret-env` stay for scenario files.

http1, http2, grpc, websocket, mqtt, tcp and udp do not take `-data` yet.
Use a scenario file, where `data.NAME` works for every protocol.

[`examples/data/README.md`](https://github.com/vegaload/vegaload/blob/main/examples/data/README.md) has the same idea for
MySQL, Redis and SFTP.
