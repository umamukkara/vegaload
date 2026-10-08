# mysql_orders.py -- the same scenario as mysql-orders.vl.js, in Python:
# write a row to MySQL or MariaDB, read it back, and check the answer.
#
# You need a server on localhost:3306 and a table:
#   create table orders (id int primary key auto_increment, item varchar(255) not null, qty int not null);
# Then run it with the password in an environment variable. Python scenarios
# need python3.
#   DB_PASSWORD=secret vegaload run -vus 4 -duration 10s -secret-env DB_PASSWORD mysql_orders.py
#
# A call does not raise when the server fails. It returns a reply with
# ok False and error set, so check it. A call that is set up wrongly (an
# unknown option) does raise.

import random

URL = "mysql://localhost:3306/app"


def iteration():
    # This scenario inserts rows, so it asks to write. Without allow_writes,
    # VegaLoad runs every query read-only. Both calls share this login, so
    # they share one pool.
    login = dict(username="app", password=env.DB_PASSWORD, tls="preferred", allow_writes=True)

    made = mysql.query(
        URL, **login,
        body="insert into orders (item, qty) values (?, ?)",
        args=["book", random.randint(1, 5)],
    )
    check(made, {
        "order stored": lambda r: r.ok,
        "database gave an id": lambda r: r.ok and r.lastInsertId > 0,
    })
    if not made.ok:
        return

    found = mysql.query(
        URL, **login,
        body="select item, qty from orders where id = ?",
        args=[made.lastInsertId],
        min_rows=1,
    )
    check(found, {
        "order read back": lambda r: r.ok and r.rows[0].item == "book",
    })
