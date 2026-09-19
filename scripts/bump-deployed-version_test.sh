#!/usr/bin/env bash
# Offline test harness for scripts/bump-deployed-version.sh.
#
# Operates entirely on temp-dir copies of
# scripts/testdata/bump_deployed_version_env_fixture — never touches the
# real deploy/percival/karakeep-random-rss.env, never touches the network.
#
# Not a testing framework: fail() prints a diagnostic and exits 1
# immediately.
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BUMP="$SCRIPT_DIR/bump-deployed-version.sh"
FIXTURE="$SCRIPT_DIR/testdata/bump_deployed_version_env_fixture"

fail() {
  echo "bump-deployed-version_test.sh: FAILED — $*" >&2
  exit 1
}

check_contains() {
  # $1 = haystack, $2 = needle, $3 = description
  if [[ "$1" != *"$2"* ]]; then
    fail "expected $3 to contain '$2', got:
$1"
  fi
}

WORK_DIR="$(mktemp -d)"

cleanup() {
  if [[ -n "$WORK_DIR" ]]; then
    rm -rf "$WORK_DIR"
  fi
  return 0
}
trap cleanup EXIT

fresh_env_file() {
  f="$(mktemp "$WORK_DIR/env.XXXXXX")"
  cp "$FIXTURE" "$f"
  echo "$f"
}

run_bump() {
  OUT="$("$BUMP" "$@" 2>&1)"
  STATUS=$?
}

# ============================================================
echo "== test 1: happy path rewrites only KARAKEEP_RANDOM_RSS_VERSION= line =="
env_file="$(fresh_env_file)"
expected="$WORK_DIR/expected1"
sed -E 's/^KARAKEEP_RANDOM_RSS_VERSION=.*/KARAKEEP_RANDOM_RSS_VERSION=v0.3.0/' "$FIXTURE" >"$expected"

run_bump "v0.3.0" --file "$env_file"
if [[ "$STATUS" -ne 0 ]]; then
  fail "expected exit 0 on happy path, got $STATUS. Output:
$OUT"
fi
check_contains "$OUT" "v0.2.1 -> v0.3.0" "happy-path output"

if ! diff "$expected" "$env_file" >"$WORK_DIR/diff1" 2>&1; then
  fail "rewritten file does not byte-match expected:
$(cat "$WORK_DIR/diff1")"
fi

# Explicitly confirm the leading comment block and trailing newline
# survived, not just that a global diff passed.
if [[ "$(head -n1 "$env_file")" != "# karakeep-random-rss stack environment file (percival) — fixture for" ]]; then
  fail "leading comment was not preserved"
fi
if [[ "$(tail -c1 "$env_file" | xxd -p)" != "0a" ]]; then
  fail "trailing newline was not preserved"
fi
echo "== test 1: PASS =="

# ============================================================
echo "== test 2: idempotent — second run at the same version is a no-op =="
env_file="$(fresh_env_file)"
run_bump "v0.4.0" --file "$env_file"
if [[ "$STATUS" -ne 0 ]]; then
  fail "expected exit 0 on first bump, got $STATUS. Output:
$OUT"
fi
cp "$env_file" "$WORK_DIR/after_first_bump"

run_bump "v0.4.0" --file "$env_file"
if [[ "$STATUS" -ne 0 ]]; then
  fail "expected exit 0 on idempotent re-run, got $STATUS. Output:
$OUT"
fi
check_contains "$OUT" "already at v0.4.0" "idempotent re-run output"
if ! diff -q "$WORK_DIR/after_first_bump" "$env_file" >/dev/null; then
  fail "file changed on idempotent re-run"
fi
echo "== test 2: PASS =="

# ============================================================
echo "== test 3: missing KARAKEEP_RANDOM_RSS_VERSION key fails and leaves file untouched =="
no_version_file="$WORK_DIR/no_version_env"
grep -v '^KARAKEEP_RANDOM_RSS_VERSION=' "$FIXTURE" >"$no_version_file"
cp "$no_version_file" "$WORK_DIR/no_version_before"

run_bump "v0.5.0" --file "$no_version_file"
if [[ "$STATUS" -eq 0 ]]; then
  fail "expected non-zero exit when KARAKEEP_RANDOM_RSS_VERSION is absent, got 0. Output:
$OUT"
fi
check_contains "$OUT" "KARAKEEP_RANDOM_RSS_VERSION" "missing-key error output"
if ! diff -q "$WORK_DIR/no_version_before" "$no_version_file" >/dev/null; then
  fail "file was mutated when KARAKEEP_RANDOM_RSS_VERSION was absent"
fi
echo "== test 3: PASS =="

# ============================================================
echo "== test 4: invalid version strings are rejected and leave file untouched =="
for bad_version in "0.2.2" "v1.2" "latest" "v1.2.3-rc1" "vX.Y.Z"; do
  env_file="$(fresh_env_file)"
  cp "$env_file" "$WORK_DIR/before_bad_$bad_version"

  run_bump "$bad_version" --file "$env_file"
  if [[ "$STATUS" -eq 0 ]]; then
    fail "expected non-zero exit for invalid version '$bad_version', got 0. Output:
$OUT"
  fi
  if ! diff -q "$WORK_DIR/before_bad_$bad_version" "$env_file" >/dev/null; then
    fail "file was mutated for invalid version '$bad_version'"
  fi
done
echo "== test 4: PASS =="

# ============================================================
echo "== test 5: missing <version> argument prints usage and fails =="
run_bump
if [[ "$STATUS" -eq 0 ]]; then
  fail "expected non-zero exit with no arguments, got 0. Output:
$OUT"
fi
check_contains "$OUT" "Usage" "no-args usage output"
echo "== test 5: PASS =="

# ============================================================
echo "== test 6: --help exits 0 and documents the default file and version format =="
run_bump --help
if [[ "$STATUS" -ne 0 ]]; then
  fail "expected exit 0 for --help, got $STATUS. Output:
$OUT"
fi
check_contains "$OUT" "Usage" "--help output"
check_contains "$OUT" "deploy/percival/karakeep-random-rss.env" "--help documents default file"
echo "== test 6: PASS =="

echo "bump-deployed-version_test.sh: PASSED"
