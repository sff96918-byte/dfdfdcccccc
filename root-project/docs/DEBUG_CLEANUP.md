# Debug/cleanup fixes

## Bugs reproduced and corrected

- Partial verification reports could be marked PASS with only one passing check.
  Packaging now requires the complete named checklist, unique names and valid
  statuses. An explicit unverified handoff does not bypass completeness checks.
- Source exclusions were case-sensitive; Windows paths like DATA/ or Profiles/
  could leak into source packages. Exclusions now normalize case/separators.
- Archive path checks did not reject Windows drive/alternate-stream paths.
  Checks now reject those, case collisions, device names, dot segments and
  trailing-dot/space paths.
- Workflow changes after verification were not included in stale-source checks.
  The external CI workflow now has its own fingerprint in the report.
- A failed/interrupted new verification could leave an old report available.
  Both the Windows dependency-install wrapper and runner invalidate it first;
  reports are written to a temporary file and renamed after completion.
- Missing/modified log evidence was previously silently omitted during packaging.
  Packaging now checks each recorded log hash and refuses missing/changed logs.
- Dependency failures were all labelled BLOCKED, hiding actual module/checksum
  failures. Only recognized missing-tool/network conditions are now BLOCKED;
  code/tool/module failures are FAIL, with structured diagnostics.
- Portable preflight could accept runtimes in folders the app does not search,
  and could follow a symlink at the runtime root. It now verifies the expected
  layout and rejects symlinks. Runtime hashing streams one file at a time.
- A profile of '.' or a Windows device name such as CON/LPT1 could produce an
  invalid or unintended profile directory. The existing sanitizer now normalizes
  those, trailing dots/spaces and control characters. Normal valid names are
  unchanged; no existing profile data is renamed or removed.

## Scope

Production change this round: safeProfileName in app.go only. Verification and
packaging scripts/tests were repaired and formatted for maintainability. A
START-HERE guide provides a single entry point without deleting historical docs
or legacy source. New verification-policy.cjs centralizes the release checklist.
No changes to message flow, AI, license, proxy/browser drivers or dependency
versions. No real browser or account was used.

## Evidence and limits

Three new regression tests initially failed against the old packaging code and
passed after the fixes. Expanded Node tests cover all tooling changes. Exact
production validation/profile functions were also copied to a temporary isolated
Go package with the real model source: TestValidateBrowserRun and
TestSafeProfileNameWindows passed. That isolated run is NOT a full app test.

The complete Windows build/lifecycle suite remains subject to dependency access
and a Windows runner. The delivered report is authoritative; do not describe
BLOCKED checks or the unexecuted CI workflow as successful.
