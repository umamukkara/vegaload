// crud-flow.vl.js -- a stateful REST flow in one scenario: create a
// widget, read it back by id, then list all widgets and find it. Each
// step uses what the one before returned, which is what a real user
// session looks like and what a single fixed -target cannot do.
//
// It runs against ../sample-app (see ../sample-app/README.md for its
// endpoints). Each stage is a named step(), so the summary and the HTML
// report show latency and error rate for create, read and list on their
// own, and a threshold can target one: -threshold 'p95{step="list"} < 300ms'.
// The sample app fails about 3% of creates on purpose, so
// a few failed iterations are expected and healthy.
//
//   cd ../sample-app && go run .                     # in one terminal
//   vegaload run -vus 5 -duration 10s crud-flow.vl.js  # in another
//
// Flags go before the scenario file. localhost needs no
// -allow-target/-yes; a host elsewhere would (see "vegaload run -h").
const base = "http://127.0.0.1:8080";

export default function () {
  // 1. Create.
  const widget = step("create", () => {
    const created = http.post(base + "/widgets", {
      body: JSON.stringify({ name: "crud-flow-widget" }),
    });
    if (created.status !== 201) {
      throw new Error("create: status " + created.status);
    }
    return created.json();
  });

  // 2. Read it back by the id the create returned.
  step("read", () => {
    const fetched = http.get(base + "/widgets/" + widget.id);
    if (fetched.status !== 200) {
      throw new Error("read: status " + fetched.status + " for id " + widget.id);
    }
    if (fetched.json().name !== widget.name) {
      throw new Error("read: expected name " + widget.name + ", got " + fetched.json().name);
    }
  });

  // 3. List, and check the new widget is in the list.
  step("list", () => {
    const listed = http.get(base + "/widgets");
    if (listed.status !== 200) {
      throw new Error("list: status " + listed.status);
    }
    if (!listed.json().some((w) => w.id === widget.id)) {
      throw new Error("list: widget " + widget.id + " is missing from the list");
    }
  });
}
