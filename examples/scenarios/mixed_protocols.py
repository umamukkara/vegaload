# mixed_protocols.py -- the same flow as mixed-protocols.vl.js, in Python:
# an HTTP call says which service to check, a gRPC call checks it, and the
# answer goes out over a WebSocket. Each stage is a named step, so the summary
# shows latency and errors for each one.
#
# You need an HTTP API that answers GET /config with {"service": "..."}, a gRPC
# server with the standard health service and reflection on localhost:50051,
# and a WebSocket server on ws://localhost:8081/echo. Then:
#   vegaload run -vus 2 -duration 10s mixed_protocols.py
#
# A call does not raise when the network fails. It returns a reply with
# ok False. A call that is set up wrongly raises VegaloadError.
API = "http://localhost:8080"
GRPC_SERVER = "localhost:50051"
WS_SERVER = "ws://localhost:8081/echo"


def iteration():
    with step("config over HTTP"):
        cfg = http.get(API + "/config").json()

    with step("health over gRPC"):
        health = grpc.call(GRPC_SERVER, method="/grpc.health.v1.Health/Check", body={"service": cfg["service"]})
        check(health, {
            "grpc call worked": lambda x: x.ok,
            "service is serving": lambda x: x.ok and x.json.status == "SERVING",
        })

    with step("report over WebSocket"):
        conn = ws.connect(WS_SERVER)
        status = health.json.status if health.ok else "UNKNOWN"
        conn.send('{"service": "%s", "status": "%s"}' % (cfg["service"], status))
        conn.receive(2000)
        conn.close()
