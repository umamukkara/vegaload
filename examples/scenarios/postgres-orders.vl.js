// postgres-orders.vl.js -- a scenario that writes a row to PostgreSQL, reads
// it back, and checks the answer.
//
// You need a PostgreSQL server on localhost:5432 and a table:
//   create table orders (id serial primary key, item text not null, qty int not null);
// Then run it with the password in an environment variable:
//   DB_PASSWORD=secret vegaload run -vus 4 -duration 10s -secret-env DB_PASSWORD postgres-orders.vl.js
//
// A call does not throw when the server fails. It returns {ok, error},
// so check it. A call that is set up wrongly (an unknown option) does throw.
// Each user keeps one connection for the whole run.
export default function () {
  const url = "postgres://localhost:5432/app";
  // This scenario inserts rows, so it asks to write. Without allow_writes,
  // VegaLoad runs every query read-only.
  const login = { username: "app", password: env.DB_PASSWORD, sslmode: "prefer", allow_writes: true };

  const made = postgres.query(url, {
    ...login,
    body: "insert into orders (item, qty) values ($1, $2) returning id",
    args: ["book", 1 + Math.floor(Math.random() * 5)],
  });
  check(made, {
    "order stored": (r) => r.ok,
    "database gave an id": (r) => r.ok && r.rows[0].id > 0,
  });
  if (!made.ok) return;

  const found = postgres.query(url, {
    ...login,
    body: "select item, qty from orders where id = $1",
    args: [made.rows[0].id],
    min_rows: 1,
  });
  check(found, {
    "order read back": (r) => r.ok && r.rows[0].item === "book",
  });
}
