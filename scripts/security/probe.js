// Black-box security checks of a running, seeded RiskForge API (M5 validation). Each check sends
// hostile input and asserts the documented, safe answer. No dependencies: Node 22 or newer.
//
// Environment:
//   API_URL        base URL, default http://127.0.0.1:3000
//   DEMO_PASSWORD  password of the demo users (never printed)
//   JWT_SECRET     optional. With it the script also forges well-signed tokens that must still be
//                  refused (expired, unknown user, no key claim). Never printed.
//
// Prints one line per check and exits 1 when one fails. It changes no data (logins and reads,
// plus requests that must be refused), but it uses up the login budget of the address for about
// a minute, so run it on a disposable stack.
"use strict";
const crypto = require("node:crypto");
const base = (process.env.API_URL || "http://127.0.0.1:3000").replace(/\/$/, "");
let passed = 0;
let failed = 0;

async function call(method, path, { token, headers = {}, body, raw } = {}) {
  const h = { ...headers };
  if (token) h.Authorization = `Bearer ${token}`;
  if (body !== undefined && !h["Content-Type"]) h["Content-Type"] = "application/json";
  let res;
  try {
    res = await fetch(base + path, {
      method, headers: h, redirect: "manual",
      body: raw !== undefined ? raw : body === undefined ? undefined : JSON.stringify(body),
      signal: AbortSignal.timeout(15000),
    });
  } catch (e) {
    // The server closed the connection (fasthttp does so for a request line or headers above its
    // limit) or did not answer in time. Status 0 lets a check decide whether that is acceptable.
    return { status: 0, headers: new Headers(), text: "", json: null };
  }
  const text = await res.text();
  let json = null;
  try { json = JSON.parse(text); } catch { /* not JSON */ }
  return { status: res.status, headers: res.headers, text, json };
}
function check(name, ok, detail = "") {
  if (ok) { passed++; console.log(`ok   ${name}`); } else { failed++; console.log(`FAIL ${name}${detail ? `: ${detail}` : ""}`); }
}
const code = (r) => r.json && r.json.error && r.json.error.code;
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

function b64url(v) { return Buffer.from(typeof v === "string" ? v : JSON.stringify(v)).toString("base64url"); }
function sign(header, payload, secret) {
  const data = `${b64url(header)}.${b64url(payload)}`;
  return `${data}.${crypto.createHmac("sha256", secret).update(data).digest("base64url")}`;
}

async function login(username) {
  const r = await call("POST", "/api/v1/auth/login", { body: { username, password: process.env.DEMO_PASSWORD } });
  if (!r.json || !r.json.access_token) throw new Error(`login of ${username} failed with ${r.status}`);
  return r.json.access_token;
}

(async () => {
  const auditor = await login("demo_auditor");
  const consumer = await login("demo_loan_application");
  const analyst = await login("demo_analyst");
  const admin = await login("demo_admin");
  const me = (await call("GET", "/api/v1/auth/me", { token: auditor })).json;
  const [h, p] = auditor.split(".").slice(0, 2).map((s) => JSON.parse(Buffer.from(s, "base64url").toString()));

  console.log("== Tokens ==");
  const none = `${b64url({ alg: "none", typ: "JWT" })}.${b64url(p)}.`;
  check("alg none is refused", (await call("GET", "/api/v1/auth/me", { token: none })).status === 401);
  const parts = auditor.split(".");
  const flipped = `${parts[0]}.${parts[1]}.${parts[2].slice(0, -2)}${parts[2].endsWith("AA") ? "BB" : "AA"}`;
  check("a changed signature is refused", (await call("GET", "/api/v1/auth/me", { token: flipped })).status === 401);
  const escalated = `${parts[0]}.${b64url({ ...p, key: "00000000-0000-4000-8000-000000000000", roles: ["ADMIN"] })}.${parts[2]}`;
  check("a changed payload with the old signature is refused", (await call("GET", "/api/v1/auth/me", { token: escalated })).status === 401);
  check("a token signed with another secret is refused",
    (await call("GET", "/api/v1/auth/me", { token: sign(h, p, "not-the-secret-not-the-secret-0000") })).status === 401);
  check("HS512 with the same claims is refused (algorithm fixed by the server)",
    (await call("GET", "/api/v1/auth/me", { token: (() => {
      const data = `${b64url({ alg: "HS512", typ: "JWT" })}.${b64url(p)}`;
      return `${data}.${crypto.createHmac("sha512", "x").update(data).digest("base64url")}`;
    })() })).status === 401);
  check("garbage in the Authorization header is a 401, not a 500",
    (await call("GET", "/api/v1/auth/me", { headers: { Authorization: "Bearer " + "x".repeat(3000) } })).status === 401);
  check("a Basic scheme is refused", (await call("GET", "/api/v1/auth/me", { headers: { Authorization: "Basic ZGVtbzpkZW1v" } })).status === 401);
  check("the token in a query string is ignored", (await call("GET", `/api/v1/auth/me?token=${auditor}`)).status === 401);
  if (process.env.JWT_SECRET) {
    const now = Math.floor(Date.now() / 1000);
    const s = process.env.JWT_SECRET;
    check("a well-signed expired token is refused", (await call("GET", "/api/v1/auth/me", { token: sign(h, { ...p, iat: now - 7200, exp: now - 3600 }, s) })).status === 401);
    check("a well-signed token for an unknown user is refused",
      (await call("GET", "/api/v1/auth/me", { token: sign(h, { ...p, key: "00000000-0000-4000-8000-000000000000" }, s) })).status === 401);
    check("a well-signed token without the key claim is refused",
      (await call("GET", "/api/v1/auth/me", { token: sign(h, { sub: "user", iat: now, exp: now + 600 }, s) })).status === 401);
  } else {
    console.log("skip forged well-signed tokens (JWT_SECRET not given)");
  }

  console.log("== Access control ==");
  const matrix = [
    ["GET", "/api/v1/audit/events", { auditor: 200, consumer: 403, analyst: 403, admin: 403 }],
    ["GET", "/api/v1/audit/decisions", { auditor: 200, consumer: 403, analyst: 403, admin: 403 }],
    ["GET", "/metrics", { auditor: 403, consumer: 403, analyst: 403, admin: 200 }],
    ["GET", "/api/v1/approval-workflows", { auditor: 200, consumer: 403, analyst: 403, admin: 200 }],
    ["GET", "/api/v1/policies", { auditor: 200, consumer: 403, analyst: 200, admin: 403 }],
  ];
  const tokens = { auditor, consumer, analyst, admin };
  for (const [m, path, want] of matrix) {
    for (const [who, status] of Object.entries(want)) {
      const r = await call(m, path, { token: tokens[who] });
      check(`${who} on ${m} ${path} is ${status}`, r.status === status, `got ${r.status}`);
    }
    check(`anonymous on ${m} ${path} is 401`, (await call(m, path)).status === 401);
  }
  const evalBody = { application_id: "SEC-PROBE-1", product_code: "KTA_DEMO", applicant: {} };
  check("the auditor cannot create a decision", (await call("POST", "/api/v1/decisions/evaluate", { token: auditor, headers: { "Idempotency-Key": "sec-probe-1" }, body: evalBody })).status === 403);
  check("an admin cannot approve", (await call("POST", "/api/v1/approval-requests/00000000-0000-4000-8000-000000000000/actions", { token: admin, body: { action: "APPROVE" } })).status === 403);

  console.log("== Input handling ==");
  const injections = ["' OR '1'='1", "1; DROP TABLE audit.events;--", "%27%20OR%201=1", "../../etc/passwd", "<script>alert(1)</script>", "\u0000", "A\nB"];
  for (const v of injections) {
    const label = JSON.stringify(v).slice(0, 30);
    for (const param of ["action", "aggregate_id", "actor_id", "from", "order"]) {
      const r = await call("GET", `/api/v1/audit/events?${param}=${encodeURIComponent(v)}`, { token: auditor });
      check(`audit events ${param}=${label} is a 422 or an empty page, never a 500`,
        r.status === 422 || (r.status === 200 && r.json && r.json.meta && r.json.meta.total === 0), `got ${r.status}`);
    }
    const r = await call("GET", `/api/v1/audit/decisions?application_id=${encodeURIComponent(v)}`, { token: auditor });
    check(`audit decisions application_id=${label} is a 422 or an empty page`, r.status === 422 || (r.status === 200 && r.json.meta.total === 0), `got ${r.status}`);
  }
  // Bytes that are not UTF-8 cannot be written with encodeURIComponent, so they go in raw.
  for (const path of ["/api/v1/audit/events?aggregate_id=%ff%fe", "/api/v1/audit/events?action=%ff", "/api/v1/audit/decisions?application_id=%ff%fe",
    "/api/v1/audit/decisions?product_code=%00"]) {
    const r = await call("GET", path, { token: auditor });
    check(`invalid UTF-8 or NUL in ${path.split("?")[1]} is a 422`, r.status === 422, `got ${r.status}`);
  }
  const huge = await call("GET", `/api/v1/audit/events?action=${"A".repeat(5000)}`, { token: auditor });
  check("a query string above the header limit is refused (431 or a closed connection), not a 500", huge.status === 431 || huge.status === 0, `got ${huge.status}`);
  const nulBodies = [
    ["POST", "/api/v1/products", admin, { code: "SEC_PROBE", name: "bad\u0000name", description: "x" }],
    ["POST", "/api/v1/approval-workflows", admin, { product_code: "KTA_DEMO", name: "w\u0000x", steps: [{ name: "a", required_role: "RISK_APPROVER" }] }],
    ["POST", "/api/v1/decisions/evaluate", consumer, { ...evalBody, application_id: "A\u0000B" }, { "Idempotency-Key": "sec-probe-nul" }],
  ];
  for (const [m, path, token, b, headers] of nulBodies) {
    const r = await call(m, path, { token, body: b, headers });
    check(`a NUL character in a ${path} body is a 422`, r.status === 422, `got ${r.status}`);
  }
  for (const [q, why] of [["per_page=101", "per_page above 100"], ["per_page=0", "per_page 0"], ["page=10001", "page above 10000"], ["page=-1", "negative page"], ["per_page=1e3", "exponent"]]) {
    const r = await call("GET", `/api/v1/audit/events?${q}`, { token: auditor });
    check(`${why} is a 422`, r.status === 422, `got ${r.status}`);
  }
  const unknown = await call("GET", `/api/v1/decisions/${encodeURIComponent("1' OR '1'='1")}`, { token: auditor });
  check("an injected decision id is a 404 or 422, not a 500", unknown.status === 404 || unknown.status === 422, `got ${unknown.status}`);
  const big = await call("POST", "/api/v1/decisions/evaluate", { token: consumer, headers: { "Idempotency-Key": "sec-probe-big" }, raw: JSON.stringify({ ...evalBody, pad: "x".repeat(70 * 1024) }) });
  check("a body above 64 KiB is refused with 413", big.status === 413, `got ${big.status}`);
  const deep = "[".repeat(5000) + "]".repeat(5000);
  const nested = await call("POST", "/api/v1/decisions/evaluate", { token: consumer, headers: { "Idempotency-Key": "sec-probe-deep" }, raw: `{"application_id":"X","product_code":"KTA_DEMO","applicant":${deep}}` });
  check("deeply nested JSON is a 422, not a crash", nested.status === 422, `got ${nested.status}`);
  const broken = await call("POST", "/api/v1/decisions/evaluate", { token: consumer, headers: { "Idempotency-Key": "sec-probe-broken" }, raw: "{not json" });
  check("malformed JSON is a 4xx with the error envelope", broken.status >= 400 && broken.status < 500 && !!code(broken), `got ${broken.status}`);
  const proto = await call("POST", "/api/v1/decisions/evaluate", { token: consumer, headers: { "Idempotency-Key": "sec-probe-proto" },
    raw: `{"application_id":"SEC-PROBE-2","product_code":"KTA_DEMO","applicant":{"__proto__":{"is_blacklisted":false},"constructor":1}}` });
  check("unknown keys such as __proto__ are a validation error", proto.status === 422, `got ${proto.status}`);
  const longKey = await call("POST", "/api/v1/decisions/evaluate", { token: consumer, headers: { "Idempotency-Key": "k".repeat(300) }, body: evalBody });
  check("an Idempotency-Key longer than allowed is a 422", longKey.status === 422, `got ${longKey.status}`);
  const ctype = await call("POST", "/api/v1/auth/login", { headers: { "Content-Type": "text/plain" }, raw: `{"username":"demo_auditor","password":"x"}` });
  check("login with a non-JSON content type does not succeed", ctype.status !== 200, `got ${ctype.status}`);

  console.log("== Headers, errors, and paths ==");
  const crlf = await fetch(`${base}/health/live`, { headers: { "X-Request-ID": "abcdefgh\u2028injected" } }).catch(() => null);
  check("an invalid X-Request-ID is replaced, not echoed", !crlf || crlf.headers.get("x-request-id") !== "abcdefgh\u2028injected");
  const longId = await call("GET", "/health/live", { headers: { "X-Request-ID": "a".repeat(200) } });
  check("an over-long X-Request-ID is replaced", longId.headers.get("x-request-id") !== "a".repeat(200));
  for (const path of ["/.env", "/.git/config", "/storage/logs/activity.log", "/swagger/../.env", "/public/../.env.agent", "/api/v1/../../.env"]) {
    const r = await call("GET", path);
    check(`${path} is not served`, r.status === 404 && !/APP_KEY|JWT_SECRET|DB_PASSWORD|\[core\]/.test(r.text), `got ${r.status}`);
  }
  const notFound = await call("GET", "/no/such/route");
  for (const [hname, want] of [["x-content-type-options", "nosniff"], ["x-frame-options", "DENY"], ["cache-control", "no-store"], ["referrer-policy", "no-referrer"]]) {
    check(`a 404 has ${hname}: ${want}`, notFound.headers.get(hname) === want, `got ${notFound.headers.get(hname)}`);
  }
  check("the server does not announce itself", !notFound.headers.get("x-powered-by") && !/fiber|goravel/i.test(notFound.headers.get("server") || ""), `server: ${notFound.headers.get("server")}`);
  // fetch refuses to send TRACE, so the method check uses PUT on a GET-only route; TRACE itself was
  // checked with curl (404 ROUTE_NOT_FOUND, nothing echoed; docs/SECURITY_TESTING.md).
  const put = await call("PUT", "/health/live");
  check("an unregistered method on a route is a JSON 404", put.status === 404 && code(put) === "ROUTE_NOT_FOUND", `got ${put.status}`);
  const opt = await call("OPTIONS", "/api/v1/auth/me", { headers: { Origin: "https://evil.example", "Access-Control-Request-Method": "GET" } });
  check("no CORS grant for a foreign origin", opt.headers.get("access-control-allow-origin") !== "https://evil.example" && opt.headers.get("access-control-allow-origin") !== "*",
    `allow-origin: ${opt.headers.get("access-control-allow-origin")}`);
  const leak = [broken, nested, notFound, big].map((r) => r.text).join("\n");
  check("errors carry no stack trace, file path, or SQL", !/goroutine|\.go:\d+|panic|SELECT |pq:|pgx|SQLSTATE/i.test(leak));

  console.log("== Data exposure ==");
  const dec = await call("GET", "/api/v1/audit/decisions?per_page=100", { token: auditor });
  check("the decision history has no applicant input or trace", dec.status === 200 && !/monthly_income|existing_installment|rac_results|score_breakdown/.test(dec.text));
  const ev = await call("GET", "/api/v1/audit/events?per_page=100", { token: auditor });
  check("audit events carry no password, token, or secret", ev.status === 200 && !/password|access_token|secret|Bearer /i.test(ev.text.replace(/\[REDACTED\]/g, "")));
  const met = await call("GET", "/metrics", { token: admin });
  check("metrics carry no user, application, or key", met.status === 200 && !/demo_|LOAN-|DEMO-APP|STRESS-|user_id|Idempotency/i.test(met.text));
  const meBody = JSON.stringify(me);
  check("/auth/me has no password hash", !/\$2[aby]\$|password/i.test(meBody));

  console.log("== Login guessing ==");
  await sleep(1000);
  let limited = null;
  for (let i = 0; i < 40; i++) {
    const r = await call("POST", "/api/v1/auth/login", { headers: { "X-Forwarded-For": `198.51.100.${i}`, "X-Real-IP": `198.51.100.${i}` },
      body: { username: "demo_admin", password: `guess-${i}-guess-guess` } });
    if (r.status === 429) { limited = { i, r }; break; }
    if (r.status !== 401) { check("a wrong password is a 401", false, `got ${r.status}`); break; }
  }
  check("guessing is limited although X-Forwarded-For changes on every attempt", !!limited, "never 429 in 40 attempts");
  if (limited) {
    check("the 429 has Retry-After", Number(limited.r.headers.get("retry-after")) >= 1);
    const right = await call("POST", "/api/v1/auth/login", { body: { username: "demo_auditor", password: process.env.DEMO_PASSWORD } });
    check("even the right password waits while the budget is spent", right.status === 429, `got ${right.status}`);
  }
  const unknownUser = await call("POST", "/api/v1/auth/login", { body: { username: "nobody-here", password: "x" } });
  check("an unknown user and a wrong password look the same", unknownUser.status === 429 || code(unknownUser) === "INVALID_CREDENTIALS");

  console.log(`\nsecurity probe: ${passed} passed, ${failed} failed`);
  process.exit(failed ? 1 : 0);
})().catch((e) => { console.error(e.message); process.exit(1); });
