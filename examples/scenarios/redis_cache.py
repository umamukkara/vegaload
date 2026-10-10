# redis_cache.py -- the same scenario as redis-cache.vl.js, in Python:
# read a cache key, fill it on a miss, and count the hit.
#
# You need a server on localhost:6379. Put the password in an environment
# variable. Python scenarios need python3.
#   REDIS_PASSWORD=secret vegaload run -vus 4 -duration 10s -secret-env REDIS_PASSWORD redis_cache.py
#
# A call does not raise when the server fails. It returns a reply with
# ok False and error set, so check it. A call that is set up wrongly (an
# unknown option) does raise.

URL = "redis://localhost:6379"


def iteration():
    # This scenario writes, so it asks to. Without allow_writes, SET, INCR
    # and EXPIRE are refused before they are sent. Every call shares this
    # login, so they share one client.
    login = dict(password=env.REDIS_PASSWORD, allow_writes=True)
    key = "cache:order:42"

    got = redis.command(URL, **login, body="GET ?", args=[key])
    check(got, {"cache read": lambda r: r.ok})
    if not got.ok:
        return

    if got.value is None:
        stored = redis.command(URL, **login, body="SET ? ? EX 60", args=[key, "order-42"])
        check(stored, {"cache filled": lambda r: r.ok and r.value == "OK"})

    hits = key + ":hits"
    counted = redis.command(URL, **login, body="INCR ?\nEXPIRE ? 60", args=[hits, hits])
    check(counted, {"hit counted": lambda r: r.ok and len(r.values) == 2})
