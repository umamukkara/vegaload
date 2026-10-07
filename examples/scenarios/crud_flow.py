# crud_flow.py -- the same flow as crud-flow.vl.js, in Python: create a
# widget, read it back by id, then list all widgets and find it.
#
# Each stage is a named step: the summary and the HTML report show latency and
# error rate per step, and a threshold can target one, for example
# -threshold 'p95{step="list"} < 300ms'.
#
# It runs against ../sample-app (see ../sample-app/README.md). The sample
# app fails about 3% of creates on purpose, so a few failed iterations are
# expected and healthy. Python scenarios need python3 on the machine.
#
#   cd ../sample-app && go run .                  # in one terminal
#   vegaload run -vus 5 -duration 10s crud_flow.py  # in another
#
# Flags go before the scenario file. localhost needs no
# -allow-target/-yes; a host elsewhere would (see "vegaload run -h").
import json

BASE = "http://127.0.0.1:8080"


def iteration():
    # 1. Create.
    with step("create"):
        created = http.post(BASE + "/widgets", body=json.dumps({"name": "crud-flow-widget"}))
        if created.status != 201:
            raise ValueError("create: status %d" % created.status)
        widget = created.json()

    # 2. Read it back by the id the create returned.
    with step("read"):
        fetched = http.get(BASE + "/widgets/%d" % widget["id"])
        if fetched.status != 200:
            raise ValueError("read: status %d for id %d" % (fetched.status, widget["id"]))
        if fetched.json()["name"] != widget["name"]:
            raise ValueError("read: expected name %r, got %r" % (widget["name"], fetched.json()["name"]))

    # 3. List, and check the new widget is in the list.
    with step("list"):
        listed = http.get(BASE + "/widgets")
        if listed.status != 200:
            raise ValueError("list: status %d" % listed.status)
        if not any(w["id"] == widget["id"] for w in listed.json()):
            raise ValueError("list: widget %d is missing from the list" % widget["id"])
