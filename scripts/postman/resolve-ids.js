// Prints "<policyId> <versionId> <workflowId> <workflowVersionId>" of the first policy and the
// first approval workflow returned by the API, for the smoke run. Reads the bearer token from
// TOKEN and the API origin from API_URL. Fails when the stack has no policy or no workflow,
// which means it was not seeded (./artisan db:seed).
"use strict";
const base = process.env.API_URL || "http://api:3000";
const headers = { Authorization: `Bearer ${process.env.TOKEN}` };

async function first(path, what) {
  const res = await fetch(`${base}${path}`, { headers });
  const body = await res.json();
  const item = body.data && body.data[0];
  if (!item) {
    console.error(`no ${what} found; seed the stack first (./artisan db:seed)`);
    process.exit(1);
  }
  return item;
}

(async () => {
  const policy = await first("/api/v1/policies?per_page=1", "policy");
  const workflow = await first("/api/v1/approval-workflows?per_page=1", "approval workflow");
  if (!policy.latest_version || !workflow.current_version) {
    console.error("the first policy or workflow has no version; seed the stack first (./artisan db:seed)");
    process.exit(1);
  }
  process.stdout.write(`${policy.id} ${policy.latest_version.id} ${workflow.id} ${workflow.current_version.id}`);
})().catch((e) => {
  console.error(e.message);
  process.exit(1);
});
