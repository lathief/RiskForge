// Normalizes the raw openapi-to-postmanv2 output into the committed collection.
// Runs inside the generator container (scripts/generate-postman.sh); never edit the
// generated JSON by hand.
//
// Usage: node normalize.js <openapi.yaml> <raw.json> <out.json>
//
// - Removes the converter's random ids so two runs over the same spec are byte-identical.
// - Collection variable baseUrl = http://127.0.0.1:3000; every request uses {{baseUrl}}.
// - Bearer token variable is {{accessToken}} (filled from POST /api/v1/auth/login).
// - Operations with `security: []` get auth "noauth"; all others get bearer {{accessToken}}.
// - Path variables: {code} -> {{productCode}}, {variable_code} -> {{variableCode}},
//   {policy_id} -> {{policyId}}, {version_id} -> {{versionId}}, {decision_id} -> {{decisionId}},
//   {workflow_id} -> {{workflowId}}, {workflow_version_id} -> {{workflowVersionId}}.
// - Fails if the collection and the spec disagree on the set of (METHOD, path).
"use strict";

const fs = require("fs");
const yaml = require("js-yaml");

const [specPath, rawPath, outPath] = process.argv.slice(2);
if (!specPath || !rawPath || !outPath) {
  console.error("usage: node normalize.js <openapi.yaml> <raw.json> <out.json>");
  process.exit(2);
}

const COLLECTION_ID = "4b6e2f0a-9c3d-4e51-8a7b-1f2e3d4c5b6a"; // fixed so output is stable
const BASE_URL = "http://127.0.0.1:3000";
const PATH_VARIABLES = {
  code: "{{productCode}}",
  variable_code: "{{variableCode}}",
  policy_id: "{{policyId}}",
  version_id: "{{versionId}}",
  decision_id: "{{decisionId}}",
  workflow_id: "{{workflowId}}",
  workflow_version_id: "{{workflowVersionId}}",
};
const METHODS = ["get", "put", "post", "delete", "patch", "head", "options", "trace"];

const spec = yaml.load(fs.readFileSync(specPath, "utf8"));
const collection = JSON.parse(fs.readFileSync(rawPath, "utf8"));

// (METHOD path) -> { operationId, secured }
const operations = new Map();
const globalSecured = Array.isArray(spec.security) && spec.security.length > 0;
for (const [path, item] of Object.entries(spec.paths || {})) {
  for (const method of METHODS) {
    const op = item[method];
    if (!op) continue;
    const secured = Array.isArray(op.security) ? op.security.length > 0 : globalSecured;
    operations.set(`${method.toUpperCase()} ${path}`, { operationId: op.operationId, secured });
  }
}

const bearer = { type: "bearer", bearer: [{ key: "token", value: "{{accessToken}}", type: "string" }] };
const seen = new Set();

function stripIds(node) {
  if (Array.isArray(node)) {
    node.forEach(stripIds);
  } else if (node && typeof node === "object") {
    delete node.id;
    delete node._postman_id;
    Object.values(node).forEach(stripIds);
  }
}

function requestPath(url) {
  return "/" + url.path.map((seg) => (seg.startsWith(":") ? `{${seg.slice(1)}}` : seg)).join("/");
}

function fixUrl(url) {
  url.host = ["{{baseUrl}}"];
  for (const v of url.variable || []) {
    if (PATH_VARIABLES[v.key]) v.value = PATH_VARIABLES[v.key];
  }
  url.raw = "{{baseUrl}}/" + url.path.join("/");
}

function visit(items) {
  for (const item of items) {
    if (item.item) {
      visit(item.item);
      continue;
    }
    const req = item.request;
    const key = `${req.method.toUpperCase()} ${requestPath(req.url)}`;
    const op = operations.get(key);
    if (!op) {
      console.error(`collection request not in spec: ${key}`);
      process.exit(1);
    }
    if (seen.has(key)) {
      console.error(`duplicate request in collection: ${key}`);
      process.exit(1);
    }
    seen.add(key);
    fixUrl(req.url);
    req.auth = op.secured ? bearer : { type: "noauth" };
    for (const response of item.response || []) {
      const original = response.originalRequest;
      if (!original) continue;
      if (original.url) fixUrl(original.url);
      // Saved examples must not suggest that public endpoints take a token.
      if (!op.secured && Array.isArray(original.header)) {
        original.header = original.header.filter((h) => h.key.toLowerCase() !== "authorization");
      }
    }
  }
}

stripIds(collection);
visit(collection.item);

for (const key of operations.keys()) {
  if (!seen.has(key)) {
    console.error(`spec operation missing from collection: ${key}`);
    process.exit(1);
  }
}

collection.info._postman_id = COLLECTION_ID;
collection.auth = bearer;
collection.variable = [{ key: "baseUrl", value: BASE_URL, type: "string" }];
collection.event = [];

fs.writeFileSync(outPath, JSON.stringify(collection, null, 2) + "\n");
console.log(`postman: ${seen.size} requests written to ${outPath}`);
