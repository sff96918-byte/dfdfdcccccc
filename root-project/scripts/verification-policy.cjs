// Shared release policy. Missing checks must never imply verification success.
const REQUIRED_CHECKS = Object.freeze([
  "frontend-syntax",
  "ui-contract-package-tests",
  "go-toolchain",
  "changed-go-format",
  "go-dependencies",
  "module-checksums",
  "go-regression-tests",
  "go-static-analysis",
  "windows-cross-build",
  "windows-app-tests",
  "windows-app-vet",
  "wails-production-build",
  "source-unchanged-during-verification",
]);

function overall(checks) {
  if (!checks.length) return "BLOCKED";
  if (checks.some((check) => check.status === "FAIL")) return "FAIL";
  return checks.every((check) => check.status === "PASS") ? "PASS" : "BLOCKED";
}

function assertCompleteChecks(checks) {
  if (!Array.isArray(checks)) throw new Error("Incomplete verification report");
  const names = checks.map((check) => check?.name);
  if (
    new Set(names).size !== names.length ||
    names.length !== REQUIRED_CHECKS.length ||
    REQUIRED_CHECKS.some((name) => !names.includes(name))
  ) {
    throw new Error("Incomplete required checks or duplicate/unknown checks");
  }
  if (
    checks.some((check) => !["PASS", "FAIL", "BLOCKED"].includes(check.status))
  ) {
    throw new Error("Unknown verification status");
  }
}

// Environment/network problems are BLOCKED; invalid modules/checksum errors are FAIL.
function classifyResult(
  result,
  { dependency = false, emptyOutput = false } = {},
) {
  const text = (result.stdout || "") + (result.stderr || "");
  if (result.error?.code === "ENOENT")
    return { status: "BLOCKED", reason: "Required tool not found on PATH" };
  if (result.error) return { status: "FAIL", reason: result.error.message };
  if (result.status === 0) {
    return emptyOutput && text.trim()
      ? { status: "FAIL", reason: "Formatting changes required (see log)" }
      : { status: "PASS", reason: "Command completed successfully" };
  }
  const networkFailure =
    /(?:Get|Post|Head) "https?:\/\/[^\n]+": (?:EOF|.*(?:timeout|no such host|connection refused|network is unreachable|TLS handshake|certificate|connection reset))|proxyconnect tcp|dial tcp|ECONNREFUSED|ENETUNREACH/i;
  if (dependency && networkFailure.test(text)) {
    return {
      status: "BLOCKED",
      reason: "Dependency server/network unavailable; see log",
    };
  }
  return {
    status: "FAIL",
    reason: result.signal
      ? `Process terminated: ${result.signal}`
      : `Command failed (exit ${result.status})`,
  };
}

module.exports = {
  REQUIRED_CHECKS,
  overall,
  assertCompleteChecks,
  classifyResult,
};
