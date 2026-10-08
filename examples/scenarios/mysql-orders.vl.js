// mysql-orders.vl.js -- a scenario that writes a row to MySQL or MariaDB,
// reads it back, and checks the answer.
//
// You need a server on localhost:3306 and a table:
//   create table orders (id int primary key auto_increment, item varchar(255) not null, qty int not null);
// Then run it with the password in an environment variable:
//   DB_PASSWORD=secret vegaload run -vus 4 -duration 10s -secret-env DB_PASSWORD mysql-orders.vl.js
//
// A call does not throw when the server fails. It returns {ok, error},
// so check it. A call that is set up wrongly (an unknown option) does throw.
// Each user keeps one connection for the whole run.
export default function () {
  const url = "mysql://localhost:3306/app";
  // This scenario inserts rows, so it asks to write. Without allow_writes,
  // VegaLoad runs every query read-only. Both calls share this login, so
  // they share one pool.
  const login = { username: "app", password: env.DB_PASSWORD, tls: "preferred", allow_writes: true };

  const made = mysql.query(url, {
    ...login,
    body: "insert into orders (item, qty) values (?, ?)",
    args: ["book", 1 + Math.floor(Math.random() * 5)],
  });
  check(made, {
    "order stored": (r) => r.ok,
    "database gave an id": (r) => r.ok && r.lastInsertId > 0,
  });
  if (!made.ok) return;

  const found = mysql.query(url, {
    ...login,
    body: "select item, qty from orders where id = ?",
    args: [made.lastInsertId],
    min_rows: 1,
  });
  check(found, {
    "order read back": (r) => r.ok && r.rows[0].item === "book",
  });
}
