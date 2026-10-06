// checks.vl.js -- check() records pass/fail for named assertions without
// failing the iteration. A failed check is counted and shown in the
// summary and HTML report; the run carries on. Pair it with the
// check_rate threshold to fail a run when too many checks fail.
//
//   cd ../sample-app && go run .                     # in one terminal
//   vegaload run -vus 5 -duration 10s \
//     -threshold "checks pass: check_rate >= 99%" checks.vl.js
//
// Flags go before the scenario file.
const base = "http://127.0.0.1:8080";

export default function () {
  const res = http.post(base + "/widgets", {
    body: JSON.stringify({ name: "checks-widget" }),
  });

  // Each test is a function (or a plain boolean). A test that throws
  // counts as failed. check() returns true only if every test passed.
  const ok = check(res, {
    "status is 201": (r) => r.status === 201,
    "has an id": (r) => r.json().id !== undefined,
  });

  if (ok) {
    check(http.get(base + "/widgets/" + res.json().id), {
      "read back is 200": (r) => r.status === 200,
    });
  }
}
