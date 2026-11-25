// Stress phases for scripts/stress.sh (M5 validation). It runs inside a Node container on the
// Compose network of a disposable stack and drives the API past the load it is meant to carry, to
// see how it fails: with a clean status code and without losing or duplicating data, or not.
// No dependencies: it uses the fetch and performance APIs of Node 22.
//
//   node stress.js PHASE      PHASE is one of the names below; the options are environment variables.
//
//   baseline    WORKERS=8 SECONDS=8      latency of new decisions at a modest load (before and after).
//   ramp        LEVELS=16,64,128,256,512 SECONDS=10   new decisions at growing concurrency.
//   samekey     N=100                    N concurrent requests with one Idempotency-Key and body.
//   loginstorm  N=150 SECONDS=6          wrong passwords from one address while /health is probed.
//   fairness    SECONDS=6                one analyst floods simulate; another user must be served.
//   probe       SECONDS=12               one request every 200 ms, for runs that break the database.
//
// Environment: API_URL (default http://api:3000), DEMO_PASSWORD (never printed), RUN_ID.
// Every phase prints a Markdown table and, on the last line, `JSON {...}` for the shell script.
// A phase exits 3 when it saw a response that must never happen (HTTP 500, or a connection that
// the server dropped), and 1 when it could not run.
"use strict";
const base = process.env.API_URL || "http://api:3000";
const runId = process.env.RUN_ID || Date.now().toString(36);
const APPLICANT = {
  age_years: 29, monthly_income: "12000000.00", existing_installment: "2500000.00",
  employment_tenure_months: 36, slik_max_kol: 1, is_blacklisted: false,
};

function percentile(sorted, p) {
  if (sorted.length === 0) return NaN;
  return sorted[Math.min(sorted.length, Math.max(1, Math.ceil((p / 100) * sorted.length))) - 1];
}
const f = (n, d = 1) => (Number.isFinite(n) ? n.toFixed(d) : "n/a");
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

async function req(method, path, { token, headers = {}, body, timeout = 15000 } = {}) {
  const started = performance.now();
  let status;
  try {
    const res = await fetch(base + path, {
      method,
      headers: { "Content-Type": "application/json", ...(token ? { Authorization: `Bearer ${token}` } : {}), ...headers },
      body: body === undefined ? undefined : typeof body === "string" ? body : JSON.stringify(body),
      signal: AbortSignal.timeout(timeout),
    });
    await res.arrayBuffer();
    status = String(res.status);
    return { status, ms: performance.now() - started, headers: res.headers };
  } catch (e) {
    status = e.name === "TimeoutError" ? "client-timeout" : "dropped";
    return { status, ms: performance.now() - started, headers: new Headers() };
  }
}
async function login(username) {
  const r = await fetch(`${base}/api/v1/auth/login`, {
    method: "POST", headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ username, password: process.env.DEMO_PASSWORD }),
  });
  const b = await r.json();
  if (!b.access_token) throw new Error(`login of ${username} failed with ${r.status}`);
  return b.access_token;
}
const evaluateBody = (id) => ({ application_id: id, product_code: "KTA_DEMO", applicant: APPLICANT });
// Keys and application ids are unique per process, so two phases of one run never replay each other.
let counter = 0;
const salt = Date.now().toString(36);
const newDecision = (token) => {
  const n = ++counter;
  return req("POST", "/api/v1/decisions/evaluate", {
    token, headers: { "Idempotency-Key": `stress-${runId}-${salt}-${n}` }, body: evaluateBody(`STRESS-${runId}-${salt}-${n}`),
  });
};
function histogram(results) {
  const h = {};
  for (const r of results) h[r.status] = (h[r.status] || 0) + 1;
  return h;
}
const histText = (h) => Object.entries(h).sort().map(([k, v]) => `${k}:${v}`).join(" ");
const forbidden = (h) => (h["500"] || 0) + (h["dropped"] || 0);

// closed loop: `workers` requests in flight for `seconds`.
async function loop(workers, seconds, fn) {
  const results = [];
  const end = performance.now() + seconds * 1000;
  const begin = performance.now();
  await Promise.all(Array.from({ length: workers }, async () => {
    while (performance.now() < end) results.push(await fn());
  }));
  return { results, elapsed: (performance.now() - begin) / 1000 };
}
function row(label, results, elapsed) {
  const ok = results.filter((r) => r.status === "200").map((r) => r.ms).sort((a, b) => a - b);
  const h = histogram(results);
  return {
    label, requests: results.length, rps: results.length / elapsed, hist: h,
    p50: percentile(ok, 50), p95: percentile(ok, 95), p99: percentile(ok, 99), max: ok.length ? ok[ok.length - 1] : NaN,
    okRps: ok.length / elapsed,
  };
}
function print(title, rows, extra = {}) {
  console.log(`### ${title}\n`);
  console.log("| Step | Requests | Req/s | 200 per s | p50 ms | p95 ms | p99 ms | Max ms | Status counts |");
  console.log("|---|---:|---:|---:|---:|---:|---:|---:|---|");
  for (const r of rows) {
    console.log(`| ${r.label} | ${r.requests} | ${f(r.rps, 0)} | ${f(r.okRps, 0)} | ${f(r.p50)} | ${f(r.p95)} | ${f(r.p99)} | ${f(r.max)} | ${histText(r.hist)} |`);
  }
  console.log(`\nJSON ${JSON.stringify({ title, rows, ...extra })}`);
  const bad = rows.reduce((n, r) => n + forbidden(r.hist), 0);
  if (bad > 0) { console.log(`\nFAIL: ${bad} responses were an HTTP 500 or a dropped connection`); process.exitCode = 3; }
}

const phases = {
  async baseline() {
    const loan = await login("demo_loan_application");
    await loop(4, 2, () => newDecision(loan));
    const { results, elapsed } = await loop(Number(process.env.WORKERS || 8), Number(process.env.SECONDS || 8), () => newDecision(loan));
    print("Baseline", [row(`${process.env.WORKERS || 8} workers`, results, elapsed)], { applicationPrefix: `STRESS-${runId}-` });
  },

  async ramp() {
    const loan = await login("demo_loan_application");
    const levels = (process.env.LEVELS || "16,64,128,256,512").split(",").map(Number);
    const seconds = Number(process.env.SECONDS || 10);
    const rows = [];
    let ok = 0;
    for (const w of levels) {
      const { results, elapsed } = await loop(w, seconds, () => newDecision(loan));
      rows.push(row(`${w} workers`, results, elapsed));
      ok += results.filter((r) => r.status === "200").length;
      await sleep(1500); // let the server and the pool drain between levels
    }
    print("Ramp: new decisions at growing concurrency", rows, { stored200: ok, applicationPrefix: `STRESS-${runId}-` });
  },

  async samekey() {
    const loan = await login("demo_loan_application");
    const n = Number(process.env.N || 100);
    const key = `stress-${runId}-same`;
    const id = `STRESS-${runId}-SAME`;
    const started = performance.now();
    const results = await Promise.all(Array.from({ length: n }, () =>
      req("POST", "/api/v1/decisions/evaluate", { token: loan, headers: { "Idempotency-Key": key }, body: evaluateBody(id) })));
    const replayed = results.filter((r) => r.headers.get("idempotent-replayed") === "true").length;
    print(`${n} concurrent requests with one Idempotency-Key`, [row("one key", results, (performance.now() - started) / 1000)],
      { applicationId: id, replayed, fresh: results.filter((r) => r.status === "200").length - replayed });
  },

  async loginstorm() {
    const n = Number(process.env.N || 150);
    const seconds = Number(process.env.SECONDS || 6);
    const attempts = [];
    const health = [];
    const end = performance.now() + seconds * 1000;
    const attackers = Array.from({ length: n }, async () => {
      while (performance.now() < end) {
        attempts.push(await req("POST", "/api/v1/auth/login", { body: { username: "demo_admin", password: "not-the-password-0000" } }));
      }
    });
    const prober = (async () => {
      while (performance.now() < end) { health.push(await req("GET", "/health/live")); await sleep(100); }
    })();
    const begin = performance.now();
    await Promise.all([...attackers, prober]);
    const elapsed = (performance.now() - begin) / 1000;
    const healthMax = Math.max(...health.map((r) => r.ms));
    const rows = [row("wrong passwords", attempts, elapsed), row("health during the storm", health, elapsed)];
    print(`Login storm: ${n} workers, one address, ${seconds} s`, rows, { healthMaxMs: healthMax, tooManyRequests: rows[0].hist["429"] || 0 });
    console.log(`\nHealth probe: slowest answer ${f(healthMax, 0)} ms while the storm ran.`);
  },

  async fairness() {
    const analyst = await login("demo_analyst");
    const approver = await login("demo_approver_1");
    const seconds = Number(process.env.SECONDS || 6);
    const policies = await (await fetch(`${base}/api/v1/policies?per_page=1`, { headers: { Authorization: `Bearer ${analyst}` } })).json();
    const version = policies.data[0].latest_version.id;
    const end = performance.now() + seconds * 1000;
    const flood = [];
    const floods = Array.from({ length: 40 }, async () => {
      while (performance.now() < end) flood.push(await req("POST", `/api/v1/policy-versions/${version}/simulate`, { token: analyst, body: { applicant: APPLICANT } }));
    });
    const other = [];
    const polite = (async () => {
      await sleep(1000);
      while (performance.now() < end - 500) {
        other.push(await req("GET", `/api/v1/policy-versions/${version}/diff`, { token: approver }));
        await sleep(1500);
      }
    })();
    const begin = performance.now();
    await Promise.all([...floods, polite]);
    const elapsed = (performance.now() - begin) / 1000;
    const rows = [row("analyst flooding simulate", flood, elapsed), row("another user, diff every 1.5 s", other, elapsed)];
    const fine = other.length > 0 && other.every((r) => r.status === "200");
    print("Fairness: one user's flood must not use another user's budget", rows, { otherAllServed: fine });
    if (!fine) { console.log("\nFAIL: the other user was refused or failed while the analyst flooded"); process.exitCode = 3; }
  },

  async probe() {
    const loan = await login("demo_loan_application").catch(() => null);
    const seconds = Number(process.env.SECONDS || 12);
    const timeline = [];
    const end = performance.now() + seconds * 1000;
    const begin = performance.now();
    while (performance.now() < end) {
      const [ready, decision] = await Promise.all([
        req("GET", "/health/ready", { timeout: 6000 }),
        loan ? newDecision(loan) : Promise.resolve({ status: "no-token", ms: 0 }),
      ]);
      timeline.push({ at: (performance.now() - begin) / 1000, ready: ready.status, readyMs: ready.ms, decision: decision.status, decisionMs: decision.ms });
      await sleep(200);
    }
    const bySecond = {};
    for (const t of timeline) {
      const s = Math.floor(t.at);
      (bySecond[s] ||= []).push(t);
    }
    console.log("| Second | health/ready | evaluate | slowest ms |");
    console.log("|---:|---|---|---:|");
    for (const [s, ts] of Object.entries(bySecond)) {
      console.log(`| ${s} | ${histText(histogram(ts.map((t) => ({ status: t.ready }))))} | ${histText(histogram(ts.map((t) => ({ status: t.decision }))))} | ${f(Math.max(...ts.map((t) => Math.max(t.readyMs, t.decisionMs))), 0)} |`);
    }
    const last = timeline.slice(-5);
    const recovered = last.length > 0 && last.every((t) => t.ready === "200" && t.decision === "200");
    console.log(`\nJSON ${JSON.stringify({ title: "probe", recovered, samples: timeline.length, statuses: histogram(timeline.map((t) => ({ status: t.decision }))) })}`);
    const fiveHundreds = timeline.filter((t) => t.decision === "500").length;
    console.log(`\nEvaluate answered 500 ${fiveHundreds} times; the last five samples were ${recovered ? "all 200 (recovered)" : "not all 200"}.`);
  },
};

const name = process.argv[2];
if (!phases[name]) {
  console.error(`usage: node stress.js ${Object.keys(phases).join("|")}`);
  process.exit(2);
}
phases[name]().catch((e) => { console.error(e.message); process.exit(1); });
