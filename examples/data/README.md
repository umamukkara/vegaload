# Data files with -protocol

`users.csv` is one row per user. The file name, without `.csv`, is the data
set name `users`. Each call takes one row. `-data-pick next` (the default)
walks the file in order, shared by every user. `-data-pick random` picks
any row.

Put a password in an environment variable and pass its name. Do not put the
password on the command line.

PostgreSQL. The value goes in `-opt args`, not in the SQL.

```
vegaload run -target postgres://127.0.0.1:5432/app -protocol postgres \
  -data users.csv \
  -opt username=app -opt password_env=PGPASS \
  -body 'SELECT name, plan FROM users WHERE id = $1' \
  -opt 'args=[{data.users.id:int}]' \
  -vus 20 -duration 30s
```

MySQL. Same rule: the value is a parameter.

```
vegaload run -target mysql://127.0.0.1:3306/app -protocol mysql \
  -data users.csv \
  -opt username=app -opt password_env=MYSQL_PW \
  -body 'SELECT name, plan FROM users WHERE id = ?' \
  -opt 'args=[{data.users.id:int}]' \
  -vus 20 -duration 30s
```

Redis. A placeholder in the command is one whole argument, even when the
cell contains a space.

```
vegaload run -target redis://127.0.0.1:6379 -protocol redis \
  -data users.csv \
  -body 'GET session:{data.users.token}' \
  -vus 20 -duration 30s
```

SFTP. The path is filled from the row. `{id}` is still a new value on each
call.

```
vegaload run -target sftp://host -protocol sftp \
  -data users.csv \
  -opt password_env=SFTP_PW -opt mode=download \
  -opt 'path=/export/{data.users.file}' \
  -vus 20 -duration 30s
```
