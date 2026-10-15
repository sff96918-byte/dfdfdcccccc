// Single source inventory shared by verification and packaging. No real data.
const fs = require("node:fs");
const path = require("node:path");
const crypto = require("node:crypto");
const root = path.resolve(__dirname, "..");
const blockedDirs = new Set([
  ".git",
  "node_modules",
  "data",
  "runtime",
  "vendor",
  "build",
  "release",
  "verification-output",
  ".cache",
  ".gocache",
  "profiles",
  "account_sessions",
  "headless_sessions",
  ".venv",
  "__pycache__",
  // Capture sessions contain runtime browsing data (redacted or not) and must
  // never travel inside a source package. The capture tool source itself is
  // still included; only its session output is excluded.
  "capture-sessions",
]);
function excluded(rel) {
  const parts = rel.replaceAll("\\", "/").toLowerCase().split("/");
  const name = parts.at(-1);
  return (
    parts.some((p) => blockedDirs.has(p) || p.startsWith(".tmp")) ||
    parts[0] === "dist" ||
    /\.(zip|exe|dll|cab|msi|syso|log|sst|vlog|pem|key|pfx|p12)$/i.test(name) ||
    /^\.env(?:\.|$)/.test(name) ||
    [".netrc", ".git-credentials", "logs.txt"].includes(name) ||
    /^(login_info.*\.json|storage_state\.json)$/i.test(name)
  );
}
function inventory(base = root) {
  const files = {};
  function visit(dir, prefix = "") {
    for (const entry of fs
      .readdirSync(dir, { withFileTypes: true })
      .sort((a, b) => a.name.localeCompare(b.name, "en"))) {
      const rel = prefix + entry.name;
      if (excluded(rel)) continue;
      if (entry.isSymbolicLink())
        throw new Error(`Source symlink is not allowed: ${rel}`);
      if (entry.isDirectory()) visit(path.join(dir, entry.name), rel + "/");
      else if (entry.isFile())
        files[rel] = fs.readFileSync(path.join(dir, entry.name));
    }
  }
  visit(base);
  return files;
}
function hashes(files) {
  return Object.fromEntries(
    Object.keys(files)
      .sort()
      .map((k) => [
        k,
        crypto.createHash("sha256").update(files[k]).digest("hex"),
      ]),
  );
}
function fingerprint(files) {
  return crypto
    .createHash("sha256")
    .update(JSON.stringify(hashes(files)))
    .digest("hex");
}
const workflowPath = path.join(
  root,
  "../.github/workflows/browser-free-verification.yml",
);
function workflowFingerprint() {
  if (!fs.existsSync(workflowPath)) return null;
  if (fs.lstatSync(workflowPath).isSymbolicLink())
    throw new Error("Workflow symlink is not allowed");
  return crypto
    .createHash("sha256")
    .update(fs.readFileSync(workflowPath))
    .digest("hex");
}
module.exports = {
  root,
  excluded,
  inventory,
  hashes,
  fingerprint,
  workflowPath,
  workflowFingerprint,
};
