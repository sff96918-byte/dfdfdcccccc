// Optional portable-bundle check. Does not launch EXEs or access accounts.
const fs = require("node:fs");
const path = require("node:path");
const crypto = require("node:crypto");
function executableX64(file) {
  const fd = fs.openSync(file, "r");
  try {
    const header = Buffer.alloc(64);
    if (
      fs.readSync(fd, header, 0, 64, 0) !== 64 ||
      header.toString("ascii", 0, 2) !== "MZ"
    )
      return false;
    const pe = Buffer.alloc(6);
    if (fs.readSync(fd, pe, 0, 6, header.readUInt32LE(60)) !== 6) return false;
    return pe.readUInt32LE(0) === 0x4550 && pe.readUInt16LE(4) === 0x8664;
  } finally {
    fs.closeSync(fd);
  }
}
function requireRegular(file, directory = false) {
  const stat = fs.lstatSync(file);
  if (
    stat.isSymbolicLink() ||
    (directory ? !stat.isDirectory() : !stat.isFile())
  ) {
    throw new Error(
      `Expected regular ${directory ? "directory" : "file"} (no symlinks): ${file}`,
    );
  }
}
function hashFile(file) {
  const hash = crypto.createHash("sha256");
  const fd = fs.openSync(file, "r");
  const buffer = Buffer.alloc(1024 * 1024);
  try {
    let count;
    while ((count = fs.readSync(fd, buffer, 0, buffer.length, null)) > 0)
      hash.update(buffer.subarray(0, count));
    return hash.digest("hex");
  } finally {
    fs.closeSync(fd);
  }
}
function bundleFiles(base) {
  const result = {};
  function visit(dir, rel = "") {
    requireRegular(dir, true);
    for (const e of fs.readdirSync(dir, { withFileTypes: true })) {
      const name = rel + e.name;
      if (e.isSymbolicLink())
        throw new Error("Bundle symlinks are not supported");
      if (e.isDirectory()) visit(path.join(dir, e.name), name + "/");
      else {
        const file = path.join(dir, e.name);
        requireRegular(file);
        result[name] = file;
      }
    }
  }
  requireRegular(base, true);
  for (const name of ["chitchatui.exe", "chitchatbot.exe"]) {
    const file = path.join(base, name);
    requireRegular(file);
    result[name] = file;
  }
  visit(path.join(base, "runtime"), "runtime/");
  return result;
}
function inspect(base) {
  const files = bundleFiles(base);
  const required = [
    { exact: "chitchatui.exe" },
    { exact: "chitchatbot.exe" },
    { prefix: "runtime/chromium/", suffix: "/chrome.exe" },
    { prefix: "runtime/webview2/", suffix: "/msedgewebview2.exe" },
  ];
  for (const spec of required) {
    const match = Object.keys(files).find((k) =>
      spec.exact
        ? k === spec.exact
        : k.startsWith(spec.prefix) && k.endsWith(spec.suffix),
    );
    if (!match)
      throw new Error(
        `Missing required runtime/executable: ${spec.exact || spec.prefix + spec.suffix.slice(1)}`,
      );
    if (!executableX64(files[match]))
      throw new Error(`Expected Windows x64 executable: ${match}`);
  }
  // Stream one file at a time: large runtime bundles do not all reside in RAM.
  return Object.fromEntries(
    Object.keys(files)
      .sort()
      .map((name) => [name, hashFile(files[name])]),
  );
}
if (require.main === module) {
  try {
    const base = path.resolve(process.argv[2] || "dist/windows-x64");
    const manifest = path.join(base, "BUNDLE-SHA256.json");
    const actual = inspect(base);
    if (process.argv.includes("--write-manifest")) {
      fs.writeFileSync(
        manifest,
        JSON.stringify({ schema: 1, sha256: actual }, null, 2) + "\n",
      );
      console.log(
        "Bundle checksum baseline written. This is integrity evidence, NOT vendor authenticity or runtime verification.",
      );
    } else {
      const expected = JSON.parse(fs.readFileSync(manifest, "utf8")).sha256;
      if (JSON.stringify(actual) !== JSON.stringify(expected))
        throw new Error("Bundle checksum mismatch");
      if (process.platform !== "win32" || process.arch !== "x64")
        throw new Error("Windows x64 is required for deployment");
      let probe;
      try {
        probe = fs.mkdtempSync(path.join(base, ".preflight-"));
        fs.writeFileSync(path.join(probe, "write-test"), "ok");
      } finally {
        if (probe) fs.rmSync(probe, { recursive: true });
      }
      console.log(
        "Preflight PASS: architecture, executable files, checksums, writable folder. No executable was launched. Login/network/antivirus behavior is not verified.",
      );
    }
  } catch (e) {
    console.error("Preflight FAIL: " + e.message);
    process.exitCode = 1;
  }
}
module.exports = { executableX64, inspect, hashFile };
