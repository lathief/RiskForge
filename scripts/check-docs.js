// Documentation check of RF-025 (run by scripts/docs-check.sh in a Node container, no
// dependencies). It keeps the guides from pointing at things that no longer exist, which is how
// a renamed script or file silently leaves stale text behind:
//
//   1. every relative Markdown link points at a file or directory that exists, and a #fragment
//      names a heading of the target file;
//   2. every `code span` that looks like a path of this repository (scripts/..., docs/..., api/...,
//      app/..., tests/..., .github/..., and so on, with a file extension) exists;
//   3. every script in scripts/ is described in docs/SCRIPTS.md, and every guide in docs/ is listed
//      in docs/README.md.
//
// Usage: node check-docs.js <repository root>      node check-docs.js --self-test
// Exit code 0 when everything holds, 1 when a problem was found.
"use strict";
const fs = require("fs");
const path = require("path");

// Names that the guides mention on purpose because they were renamed or merged. The text around
// them says so.
const HISTORICAL = new Set([
  "docs/POST_M5_BACKLOG.md", "docs/POST_M5_BACKLOG_M4.md", "POST_M5_BACKLOG.md", "POST_M5_BACKLOG_M4.md",
  "scripts/m4-e2e.sh", "m4-e2e.sh", "docs/coverage.md",
]);

const ROOTS = ["scripts", "docs", "api", "app", "database", "tests", "routes", "config", "bootstrap", "public", ".github"];
const PATH_SPAN = new RegExp(`^(?:\\./)?((?:${ROOTS.map((r) => r.replace(".", "\\.")).join("|")})/[A-Za-z0-9_./-]*[A-Za-z0-9_-]\\.[A-Za-z0-9]+)$`);

// slug follows GitHub's rule for heading anchors: lower case, punctuation dropped, spaces to hyphens.
function slug(heading) {
  return heading.trim().toLowerCase().replace(/`/g, "").replace(/[^\p{L}\p{N}\s_-]/gu, "").replace(/\s/g, "-");
}

function headingsOf(text) {
  const slugs = new Set();
  let inFence = false;
  for (const line of text.split("\n")) {
    if (/^\s*```/.test(line)) inFence = !inFence;
    if (inFence) continue;
    const m = /^#{1,6}\s+(.*)$/.exec(line);
    if (m) slugs.add(slug(m[1]));
  }
  return slugs;
}

// stripFences removes fenced code blocks, where paths and links are examples.
function stripFences(text) {
  let inFence = false;
  return text.split("\n").map((line) => {
    if (/^\s*```/.test(line)) { inFence = !inFence; return ""; }
    return inFence ? "" : line;
  }).join("\n");
}

// check returns the problems of one Markdown text. exists(relativePathFromRoot) says whether a file
// or directory exists; headings(relativePathFromRoot) returns the anchor slugs of a Markdown file.
function check(file, text, exists, headings) {
  const problems = [];
  const dir = path.posix.dirname(file);
  const body = stripFences(text);
  const lines = body.split("\n");
  lines.forEach((line, i) => {
    const where = `${file}:${i + 1}`;
    // 1. Markdown links. Inline code is removed first, because `[x](y)` inside a code span is an example.
    const noCode = line.replace(/`[^`]*`/g, "");
    for (const m of noCode.matchAll(/\[[^\]]*\]\(([^)\s]+)(?:\s+"[^"]*")?\)/g)) {
      const target = m[1];
      if (/^(?:[a-z][a-z0-9+.-]*:|\/\/)/i.test(target)) continue; // http:, https:, mailto:
      const [rawPath, fragment] = target.split("#");
      const resolved = rawPath === "" ? file : path.posix.normalize(path.posix.join(dir, decodeURIComponent(rawPath)));
      if (!exists(resolved)) { problems.push(`${where}: the link ${target} points at ${resolved}, which does not exist`); continue; }
      if (fragment && resolved.endsWith(".md") && !headings(resolved).has(fragment.toLowerCase())) {
        problems.push(`${where}: the link ${target} names a heading that ${resolved} does not have`);
      }
    }
    // 2. Paths in code spans.
    for (const m of line.matchAll(/`([^`]+)`/g)) {
      const span = m[1].trim().split(/\s+/)[0];
      const pm = PATH_SPAN.exec(span);
      if (!pm) continue;
      const rel = pm[1];
      if (/[{}<>*]/.test(rel) || rel.includes("...") || HISTORICAL.has(rel)) continue;
      if (!exists(rel)) problems.push(`${where}: \`${rel}\` does not exist`);
    }
  });
  return problems;
}

function selfTest() {
  const assert = require("node:assert/strict");
  const files = new Set(["docs/A.md", "docs/B.md", "scripts/x.sh", "scripts"]);
  const exists = (p) => files.has(p);
  const headings = (p) => (p === "docs/B.md" ? new Set(["a-heading", "second-one"]) : new Set());
  const run = (file, text) => check(file, text, exists, headings);
  assert.deepEqual(run("docs/A.md", "see [b](B.md) and [x](../scripts/x.sh) and [web](https://example.com/y)"), []);
  assert.equal(run("docs/A.md", "see [gone](Gone.md)").length, 1);
  assert.equal(run("docs/A.md", "see [b](B.md#a-heading)").length, 0);
  assert.equal(run("docs/A.md", "see [b](B.md#missing)").length, 1);
  assert.equal(run("docs/B.md", "see [self](#a-heading)").length, 0, "a fragment of the same file is checked too");
  assert.equal(run("docs/B.md", "see [self](#nope)").length, 1);
  assert.equal(run("README.md", "see [a](docs/A.md)").length, 0);
  assert.equal(run("docs/A.md", "run `./scripts/x.sh --help` or `scripts/y.sh`").length, 1, "only the missing script is reported");
  assert.equal(run("docs/A.md", "the old `scripts/m4-e2e.sh` was renamed").length, 0, "historical names are allowed");
  assert.equal(run("docs/A.md", "`docs/<name>.md` and `docs/*.md` and `docs/{a}.md` are patterns").length, 0);
  assert.equal(run("docs/A.md", "```sh\n./scripts/none.sh\n[x](nowhere.md)\n```").length, 0, "fenced blocks are examples");
  assert.equal(run("docs/A.md", "an example `[x](nowhere.md)` in a code span").length, 0);
  assert.equal(run("docs/A.md", "`app/nowhere/file.go` is missing").length, 1);
  assert.equal(run("docs/A.md", "`storage/logs/error.log` is runtime output").length, 0, "storage is not a checked root");
  assert.equal(slug("RF-025 CI/CD: the `ci.sh` run!"), "rf-025-cicd-the-cish-run");
  console.log("check-docs.js self-test: ok");
}

function main(root) {
  const problems = [];
  const exists = (p) => fs.existsSync(path.join(root, p));
  const cache = new Map();
  const headings = (p) => {
    if (!cache.has(p)) cache.set(p, headingsOf(fs.readFileSync(path.join(root, p), "utf8").replace(/\r\n/g, "\n")));
    return cache.get(p);
  };
  const markdown = [];
  const walk = (rel) => {
    for (const entry of fs.readdirSync(path.join(root, rel), { withFileTypes: true })) {
      const next = rel ? `${rel}/${entry.name}` : entry.name;
      if (entry.isDirectory()) {
        if (["node_modules", ".git", "storage", "semantic-review", "vendor"].includes(entry.name)) continue;
        walk(next);
      } else if (entry.name.endsWith(".md")) markdown.push(next);
    }
  };
  walk("");
  for (const file of markdown) {
    const text = fs.readFileSync(path.join(root, file), "utf8").replace(/\r\n/g, "\n");
    problems.push(...check(file, text, exists, headings));
  }
  // 3. Every script and every guide is described.
  const scriptsDoc = fs.readFileSync(path.join(root, "docs/SCRIPTS.md"), "utf8");
  for (const f of fs.readdirSync(path.join(root, "scripts"))) {
    if (f.endsWith(".sh") && !scriptsDoc.includes(f)) problems.push(`scripts/${f} is not described in docs/SCRIPTS.md`);
  }
  const docsIndex = fs.readFileSync(path.join(root, "docs/README.md"), "utf8");
  for (const f of fs.readdirSync(path.join(root, "docs"))) {
    if (f.endsWith(".md") && f !== "README.md" && !docsIndex.includes(f)) problems.push(`docs/${f} is not listed in docs/README.md`);
  }
  if (problems.length) {
    console.log(problems.join("\n"));
    console.log(`\ndocs check: ${problems.length} problem(s) in ${markdown.length} Markdown files`);
    process.exit(1);
  }
  console.log(`docs check: ok (${markdown.length} Markdown files)`);
}

if (process.argv.includes("--self-test")) selfTest();
else main(process.argv[2] || ".");
