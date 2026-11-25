// HTTP load generator for scripts/benchmark.sh (RF-024). It runs inside a Node container on the
// Compose network of a disposable stack and measures end-to-end latency (HTTP, authentication,
// handler, PostgreSQL) of the main routes at several concurrency levels. No dependencies: it uses
// the fetch and performance APIs of Node 22.
//
// Environment:
//   API_URL        base URL, default http://api:3000
//   DEMO_PASSWORD  password of the demo users (never printed)
//   DURATION       seconds per scenario and concurrency level, default 8
//   CONCURRENCY    comma separated worker counts, default 1,8,32
//   WARMUP         seconds of warm-up per scenario, default 2
//
// Prints a Markdown table on stdout and the same data as JSON on the last line prefixed `JSON `.
// `node load.js --self-test` checks the statistics code and exits.
"use strict";

// percentile returns the nearest-rank percentile (0 < p <= 100) of an ascending sorted array.
function percentile(sorted, p) {
  if (sorted.length === 0) return NaN;
  const rank = Math.ceil((p / 100) * sorted.length);
  return sorted[Math.min(sorted.length, Math.max(1, rank)) - 1];
}

// summarize turns raw latencies (ms) and an elapsed time (s) into the reported figures.
function summarize(latencies, errors, elapsedSeconds) {
  const sorted = [...latencies].sort((a, b) => a - b);
  const sum = sorted.reduce((a, b) => a + b, 0);
  return {
    requests: sorted.length,
    errors,
    rps: sorted.length / elapsedSeconds,
    mean: sorted.length ? sum / sorted.length : NaN,
    p50: percentile(sorted, 50),
    p95: percentile(sorted, 95),
    p99: percentile(sorted, 99),
    max: sorted.length ? sorted[sorted.length - 1] : NaN,
  };
}

function selfTest() {
  const assert = require("node:assert/strict");
  const hundred = Array.from({ length: 100 }, (_, i) => i + 1);
  assert.equal(percentile(hundred, 50), 50);
  assert.equal(percentile(hundred, 95), 95);
  assert.equal(percentile(hundred, 99), 99);
  assert.equal(percentile(hundred, 100), 100);
  assert.equal(percentile([7], 99), 7, "one sample is every percentile");
  assert.equal(percentile([1, 2, 3, 4], 50), 2, "nearest rank, not interpolation");
  assert.ok(Number.isNaN(percentile([], 50)));
  const s = summarize([30, 10, 20], 1, 2);
  assert.deepEqual([s.requests, s.errors, s.rps, s.mean, s.p50, s.max], [3, 1, 1.5, 20, 20, 30]);
  assert.ok(Number.isNaN(summarize([], 0, 1).mean));
  console.log("load.js self-test: ok");
}

if (process.argv.includes("--self-test")) {
  selfTest();
  process.exit(0);
}

const base = process.env.API_URL || "http://api:3000";
const duration = Number(process.env.DURATION || 8);
const warmup = Number(process.env.WARMUP || 2);
const levels = (process.env.CONCURRENCY || "1,8,32").split(",").map(Number).filter((n) => n > 0);
const runId = Date.now().toString(36);
const APPLICANT = {
  age_years: 29, monthly_income: "12000000.00", existing_installment: "2500000.00",
  employment_tenure_months: 36, slik_max_kol: 1, is_blacklisted: false,
};

async function login(username) {
  const res = await fetch(`${base}/api/v1/auth/login`, {
    method: "POST", headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ username, password: process.env.DEMO_PASSWORD }),
  });
  const body = await res.json();
  if (!body.access_token) throw new Error(`login of ${username} failed with ${res.status}`);
  return body.access_token;
}

let counter = 0;
const nextKey = () => `bench-${runId}-${++counter}`;

async function once(spec) {
  const started = performance.now();
  let ok = false;
  try {
    const res = await fetch(spec.url, {
      method: spec.method, headers: spec.headers(), body: spec.body ? spec.body() : undefined,
      signal: AbortSignal.timeout(10000),
    });
    await res.arrayBuffer();
    ok = res.status === 200;
  } catch (e) {
    ok = false;
  }
  return { ms: performance.now() - started, ok };
}

// run keeps `workers` requests in flight for `seconds` and returns the summary.
async function run(spec, workers, seconds) {
  const latencies = [];
  let errors = 0;
  const end = performance.now() + seconds * 1000;
  const begin = performance.now();
  await Promise.all(Array.from({ length: workers }, async () => {
    while (performance.now() < end) {
      const r = await once(spec);
      if (r.ok) latencies.push(r.ms); else errors++;
    }
  }));
  return summarize(latencies, errors, (performance.now() - begin) / 1000);
}

(async () => {
  const [loan, auditor, analyst] = await Promise.all([login("demo_loan_application"), login("demo_auditor"), login("demo_analyst")]);
  const json = (token, extra = {}) => ({ "Content-Type": "application/json", Authorization: `Bearer ${token}`, ...extra });
  const policies = await (await fetch(`${base}/api/v1/policies?per_page=1`, { headers: json(auditor) })).json();
  const version = policies.data[0].latest_version.id;
  const evaluateBody = (id) => JSON.stringify({ application_id: id, product_code: "KTA_DEMO", applicant: APPLICANT });

  // One stored decision for the read scenario, and one key for the replay scenario.
  const seed = await fetch(`${base}/api/v1/decisions/evaluate`, {
    method: "POST", headers: json(loan, { "Idempotency-Key": `bench-${runId}-seed` }), body: evaluateBody("BENCH-SEED"),
  });
  const seeded = await seed.json();
  if (seed.status !== 200 || seeded.final_decision !== "APPROVE") throw new Error(`seed decision failed: ${seed.status}`);

  const scenarios = [
    { name: "health (no auth, no database)", method: "GET", url: `${base}/health/live`, headers: () => ({}) },
    { name: "auth/me (JWT, user lookup)", method: "GET", url: `${base}/api/v1/auth/me`, headers: () => json(analyst) },
    { name: "evaluate, new decision (stored)", method: "POST", url: `${base}/api/v1/decisions/evaluate`,
      headers: () => json(loan, { "Idempotency-Key": nextKey() }), body: () => evaluateBody(`BENCH-${counter}`) },
    { name: "evaluate, replay of one key", method: "POST", url: `${base}/api/v1/decisions/evaluate`,
      headers: () => json(loan, { "Idempotency-Key": `bench-${runId}-seed` }), body: () => evaluateBody("BENCH-SEED") },
    { name: "get stored decision", method: "GET", url: `${base}/api/v1/decisions/${seeded.decision_id}`, headers: () => json(auditor) },
    { name: "simulate (compile + evaluate, no write)", method: "POST", url: `${base}/api/v1/policy-versions/${version}/simulate`,
      headers: () => json(analyst), body: () => JSON.stringify({ applicant: APPLICANT }) },
  ];

  const rows = [];
  for (const spec of scenarios) {
    await run(spec, 4, warmup);
    for (const c of levels) {
      const s = await run(spec, c, duration);
      rows.push({ scenario: spec.name, concurrency: c, ...s });
    }
  }

  const f = (n, d = 1) => (Number.isFinite(n) ? n.toFixed(d) : "n/a");
  console.log(`Node ${process.version}, ${duration} s per row after ${warmup} s of warm-up, keep-alive connections.\n`);
  console.log("| Scenario | Concurrency | Requests | Errors | Req/s | Mean ms | p50 ms | p95 ms | p99 ms | Max ms |");
  console.log("|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|");
  for (const r of rows) {
    console.log(`| ${r.scenario} | ${r.concurrency} | ${r.requests} | ${r.errors} | ${f(r.rps, 0)} | ${f(r.mean, 2)} | ${f(r.p50, 2)} | ${f(r.p95, 2)} | ${f(r.p99, 2)} | ${f(r.max, 1)} |`);
  }
  console.log(`\nJSON ${JSON.stringify(rows)}`);
  if (rows.some((r) => r.errors > 0)) process.exitCode = 3;
})().catch((e) => {
  console.error(e.message);
  process.exit(1);
});
