// redis-cache.vl.js -- a cache-aside read against Redis or Valkey.
//
// On a miss it stores the value for 60 seconds. It also counts hits with
// a pipeline of INCR and EXPIRE. Both writes need allow_writes.
//
// You need a server on localhost:6379. Put the password in an environment
// variable:
//   REDIS_PASSWORD=secret vegaload run -vus 4 -duration 10s -secret-env REDIS_PASSWORD redis-cache.vl.js
//
// A call does not throw when the server fails. It returns {ok, error},
// so check it. A call that is set up wrongly (an unknown option) does throw.
// Each user keeps one client for the whole run.
export default function () {
  const url = "redis://localhost:6379";
  const login = { password: env.REDIS_PASSWORD, allow_writes: true };
  const key = "cache:order:42";

  const got = redis.command(url, { ...login, body: "GET ?", args: [key] });
  check(got, { "cache read": (r) => r.ok });
  if (!got.ok) return;

  if (got.value === null) {
    const stored = redis.command(url, {
      ...login,
      body: "SET ? ? EX 60",
      args: [key, "order-42"],
    });
    check(stored, { "cache filled": (r) => r.ok && r.value === "OK" });
  }

  const hits = key + ":hits";
  const counted = redis.command(url, {
    ...login,
    body: "INCR ?\nEXPIRE ? 60",
    args: [hits, hits],
  });
  check(counted, {
    "hit counted": (r) => r.ok && r.values.length === 2,
  });
}
