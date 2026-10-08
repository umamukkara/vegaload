# postgres_orders.py -- the same scenario as postgres-orders.vl.js, in
# Python: write a row to PostgreSQL, read it back, and check the answer.
#
# You need a PostgreSQL server on localhost:5432 and a table:
#   create table orders (id serial primary key, item text not null, qty int not null);
# Then run it with the password in an environment variable. Python scenarios
# need python3.
#   DB_PASSWORD=secret vegaload run -vus 4 -duration 10s -secret-env DB_PASSWORD postgres_orders.py
#
# A call does not raise when the server fails. It returns a reply with
# ok False and error set, so check it. A call that is set up wrongly (an
# unknown option) does raise.

import random

URL = "postgres://localhost:5432/app"


def iteration():
    login = dict(username="app", password=env.DB_PASSWORD, sslmode="prefer")

    made = postgres.query(
        URL, **login,
        body="insert into orders (item, qty) values ($1, $2) returning id",
        args=["book", random.randint(1, 5)],
    )
    check(made, {
        "order stored": lambda r: r.ok,
        "database gave an id": lambda r: r.ok and r.rows[0].id > 0,
    })
    if not made.ok:
        return

    found = postgres.query(
        URL, **login,
        body="select item, qty from orders where id = $1",
        args=[made.rows[0].id],
        min_rows=1,
    )
    check(found, {
        "order read back": lambda r: r.ok and r.rows[0].item == "book",
    })
