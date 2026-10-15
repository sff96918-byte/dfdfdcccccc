const fs = require("node:fs");
const path = require("node:path");
const { zipSync, unzipSync } = require("fflate");
const {
  root,
  inventory,
  hashes,
  fingerprint,
  workflowFingerprint,
} = require("./source-files.cjs");
const { overall, assertCompleteChecks } = require("./verification-policy.cjs");
function validateReport(
  report,
  currentFingerprint,
  allowUnverified = false,
  currentWorkflowFingerprint = workflowFingerprint(),
) {
  if (
    report.schema !== 1 ||
    !Array.isArray(report.checks) ||
    !report.checks.length
  )
    throw new Error("Incomplete verification report");
  assertCompleteChecks(report.checks);
  if (report.workflowFingerprint !== currentWorkflowFingerprint)
    throw new Error("Workflow changed after verification; verify again");
  if (report.sourceFingerprint !== currentFingerprint)
    throw new Error("Source changed after verification; verify again");
  if (report.overall !== overall(report.checks))
    throw new Error("Inconsistent verification report");
  if (report.overall !== "PASS" && !allowUnverified)
    throw new Error("Release blocked: required checks did not all pass");
}
function validateArchive(bytes) {
  const files = unzipSync(bytes);
  if (!files["SOURCE-MANIFEST.json"])
    throw new Error("Missing SOURCE-MANIFEST.json");
  const manifest = JSON.parse(
    Buffer.from(files["SOURCE-MANIFEST.json"]).toString(),
  );
  const expected = manifest.sha256;
  if (!expected || typeof expected !== "object" || Array.isArray(expected))
    throw new Error("Invalid checksum manifest");
  const actual = {};
  const seen = new Set();
  for (const [name, data] of Object.entries(files)) {
    if (name === "SOURCE-MANIFEST.json") continue;
    const segments = name.split("/");
    if (
      name.startsWith("/") ||
      /[\\:\x00-\x1f]/.test(name) ||
      segments.some(
        (part) =>
          !part ||
          part === "." ||
          part === ".." ||
          /[. ]$/.test(part) ||
          /^(con|prn|aux|nul|com[1-9]|lpt[1-9])(?:\.|$)/i.test(part),
      )
    )
      throw new Error("Unsafe archive path");
    const folded = name.toLowerCase();
    if (seen.has(folded))
      throw new Error("Unsafe Windows case-colliding archive paths");
    seen.add(folded);
    actual[name] = Buffer.from(data);
  }
  const actualHashes = hashes(actual);
  if (
    Object.keys(actualHashes).length !== Object.keys(expected).length ||
    Object.keys(actualHashes).some(
      (name) => actualHashes[name] !== expected[name],
    )
  )
    throw new Error("Archive content/hash mismatch");
  return manifest;
}
function createPackage(allowUnverified = false) {
  const files = inventory();
  const reportPath = path.join(root, "verification-output/report.json");
  if (!fs.existsSync(reportPath))
    throw new Error("Run npm run verify first; report is required");
  const report = JSON.parse(fs.readFileSync(reportPath, "utf8"));
  validateReport(report, fingerprint(files), allowUnverified);
  const payload = {};
  for (const [name, data] of Object.entries(files))
    payload["root-project/" + name] = data;
  payload["VERIFICATION-REPORT.json"] = Buffer.from(
    JSON.stringify(report, null, 2) + "\n",
  );
  for (const check of report.checks) {
    const match = /log=([a-zA-Z0-9-]+\.txt)$/.exec(check.detail || "");
    if (match) {
      const log = path.join(root, "verification-output", match[1]);
      if (!fs.existsSync(log))
        throw new Error("Missing verification evidence: " + match[1]);
      const data = fs.readFileSync(log);
      const digest = require("node:crypto")
        .createHash("sha256")
        .update(data)
        .digest("hex");
      if (check.logSHA256 !== digest)
        throw new Error("Verification log changed: " + match[1]);
      payload["verification-evidence/" + match[1]] = data;
    }
  }
  // Root-project-only download: the repository-level CI workflow is NOT
  // bundled beside the source. It stays in the repo under .github/.
  const manifest = {
    schema: 1,
    kind: "source-only",
    verification: report.overall,
    liveBrowser: "NOT_RUN",
    sourceFingerprint: report.sourceFingerprint,
    sha256: hashes(payload),
  };
  payload["SOURCE-MANIFEST.json"] = Buffer.from(
    JSON.stringify(manifest, null, 2) + "\n",
  );
  const bytes = zipSync(payload, { level: 6 });
  validateArchive(bytes);
  const output = path.join(
    root,
    "verification-output",
    report.overall === "PASS"
      ? "root-project-verified-source.zip"
      : "root-project-UNVERIFIED-source.zip",
  );
  fs.writeFileSync(output, bytes);
  console.log(
    `Source package: ${output}\nVerification: ${report.overall}; live browser/login: NOT_RUN`,
  );
  return output;
}
if (require.main === module) {
  try {
    if (process.argv[2] === "--check") {
      validateArchive(fs.readFileSync(process.argv[3]));
      console.log("Archive integrity PASS");
    } else createPackage(process.argv.includes("--allow-unverified"));
  } catch (e) {
    console.error(e.message);
    process.exitCode = 1;
  }
}
module.exports = { validateArchive, validateReport, createPackage };
