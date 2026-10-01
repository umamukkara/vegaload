// smoke.vl.js -- the simplest possible VegaLoad scenario file: an
// example of "a test is a real file" (see AGENTS.md), not a network
// test. Phase 0's scripting runtimes (JavaScript here, via the embedded
// interpreter; Python is the other option) can't reach the network
// yet, so a scripted scenario like this one is good for exercising
// VegaLoad's VU pool and executor shapes against your own logic, but
// not for load testing an HTTP target -- that's what protocol-direct
// mode (-target/-protocol) and the runbook generated from
// sample-app/openapi.json are for. See ../README.md for both.
//
// Run it (flags before the scenario file -- vegaload's flag parser
// stops at the first non-flag argument, see "vegaload run -h"):
//   vegaload run -vus 5 -duration 5s smoke.vl.js
export default function () {
  console.log("iteration");
}
