# checks.py -- check() records pass/fail for named assertions without
# failing the iteration. A failed check is counted and shown in the
# summary and HTML report; the run carries on. Pair it with the
# check_rate threshold to fail a run when too many checks fail.
#
#   cd ../sample-app && go run .                  # in one terminal
#   vegaload run -vus 5 -duration 10s \
#     -threshold "checks pass: check_rate >= 99%" checks.py
#
# Flags go before the scenario file.
import json

BASE = "http://127.0.0.1:8080"


def iteration():
    res = http.post(BASE + "/widgets", body=json.dumps({"name": "checks-widget"}))

    # Each test is a function (or a plain bool). A test that raises counts
    # as failed. check() returns True only if every test passed.
    ok = check(res, {
        "status is 201": lambda r: r.status == 201,
        "has an id": lambda r: "id" in r.json(),
    })

    if ok:
        check(http.get(BASE + "/widgets/%d" % res.json()["id"]), {
            "read back is 200": lambda r: r.status == 200,
        })
