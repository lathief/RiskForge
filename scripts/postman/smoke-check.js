// Checks a Newman JSON report produced by scripts/smoke-postman.sh.
// Expectations for a read-only token (demo_auditor):
//   public requests (noauth)        -> 200
//   protected GET                   -> 200
//   protected POST/PUT/PATCH/DELETE -> 403 (no write permission, so nothing is changed)
//   POST /api/v1/auth/login         -> 401 (the saved example uses a placeholder password)
// Any other status, or a transport error, fails the run.
"use strict";

const fs = require("fs");

const report = JSON.parse(fs.readFileSync(process.argv[2], "utf8"));
const executions = report.run.executions || [];
let failures = 0;

for (const ex of executions) {
  const req = ex.item.request;
  const name = `${req.method} ${ex.item.name}`;
  if (ex.requestError || !ex.response) {
    console.log(`FAIL ${name}: ${ex.requestError ? ex.requestError.message : "no response"}`);
    failures++;
    continue;
  }
  const code = ex.response.code;
  const isPublic = req.auth && req.auth.type === "noauth";
  const isWrite = ["POST", "PUT", "PATCH", "DELETE"].includes(req.method);
  const path = "/" + (req.url.path || []).join("/");
  let expected;
  // The saved login example carries the placeholder "<DEMO_USER_PASSWORD>", never a real
  // password, so the route must answer 401 INVALID_CREDENTIALS.
  if (path === "/api/v1/auth/login") expected = 401;
  else if (isPublic) expected = 200;
  else if (isWrite) expected = 403;
  else expected = 200;

  const ok = code === expected;
  console.log(`${ok ? "ok  " : "FAIL"} ${String(code).padEnd(3)} ${name}${ok ? "" : ` (expected ${expected})`}`);
  if (!ok) failures++;
}

if (executions.length === 0) {
  console.log("FAIL no requests executed");
  failures++;
}
console.log(`postman smoke: ${executions.length - failures}/${executions.length} requests as expected`);
process.exit(failures === 0 ? 0 : 1);
