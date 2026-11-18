// Prints "<policyId> <versionId>" of the first policy returned by the API, for the smoke run.
// Reads the bearer token from TOKEN and the API origin from API_URL. Fails when the stack has
// no policy, which means it was not seeded (./artisan db:seed).
"use strict";
const base = process.env.API_URL || "http://api:3000";
const headers = { Authorization: `Bearer ${process.env.TOKEN}` };
(async () => {
  const res = await fetch(`${base}/api/v1/policies?per_page=1`, { headers });
  const body = await res.json();
  const item = body.data && body.data[0];
  if (!item || !item.latest_version) {
    console.error("no policy found; seed the stack first (./artisan db:seed)");
    process.exit(1);
  }
  process.stdout.write(`${item.id} ${item.latest_version.id}`);
})().catch((e) => {
  console.error(e.message);
  process.exit(1);
});
