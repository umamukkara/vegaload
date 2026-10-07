// mixed-protocols.vl.js -- one flow that uses HTTP, gRPC and WebSocket
// together: an HTTP call says which service to check, a gRPC call checks it,
// and the answer goes out over a WebSocket. Each stage is a named step(),
// so the summary shows latency and errors for each one.
//
// You need three things on localhost (change the addresses below):
//   - an HTTP API that answers GET /config with {"service": "..."}
//   - a gRPC server with the standard health service and reflection on
//     localhost:50051. Reflection lets the script send and read JSON with no
//     .proto file.
//   - a WebSocket server on ws://localhost:8081/echo
// Then:
//   vegaload run -vus 2 -duration 10s mixed-protocols.vl.js
//
// A call does not throw when the network fails. It returns {ok, error}, so
// check it. A call that is set up wrongly (a missing method, a bad body) does
// throw.
const api = "http://localhost:8080";
const grpcServer = "localhost:50051";
const wsServer = "ws://localhost:8081/echo";

export default function () {
  const cfg = step("config over HTTP", () => http.get(api + "/config").json());

  const health = step("health over gRPC", () => {
    const r = grpc.call(grpcServer, {
      method: "/grpc.health.v1.Health/Check",
      body: { service: cfg.service },
    });
    check(r, {
      "grpc call worked": (x) => x.ok,
      "service is serving": (x) => x.ok && x.json.status === "SERVING",
    });
    return r;
  });

  step("report over WebSocket", () => {
    const conn = ws.connect(wsServer);
    conn.send(JSON.stringify({ service: cfg.service, status: health.ok ? health.json.status : "UNKNOWN" }));
    conn.receive(2000);
    conn.close();
  });
}
