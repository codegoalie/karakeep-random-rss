#!/usr/bin/env bash
# bump-deployed-version.sh — rewrite the KARAKEEP_RANDOM_RSS_VERSION= line in the
# committed dockhand env file (deploy/percival/karakeep-random-rss.env by default).
#
# This is part of a release workflow: a version is tagged and pushed, CI builds and
# publishes the image, and then CI runs this script and commits the result to main.
# dockhand polls main on a cron and redeploys once it sees the new commit
# — bumping this file *is* the deploy trigger, so the rewrite has to be
# exact and idempotent (a retried or re-dispatched CI job must be able to
# run this again safely).
#
# Usage:
#   scripts/bump-deployed-version.sh <version> [--file PATH]
#
#   <version>       version to deploy, e.g. v0.1.0 (must match
#                   ^v[0-9]+\.[0-9]+\.[0-9]+$)
#
# Options:
#   --file PATH     env file to rewrite (default:
#                   deploy/percival/karakeep-random-rss.env, resolved relative to
#                   the repo root, not the current working directory)
#   -h, --help      show this help
#
# Behavior:
#   - Only the KARAKEEP_RANDOM_RSS_VERSION= line is rewritten; every other byte in
#     the file (comments, blank lines, other keys, trailing newline) is
#     preserved exactly.
#   - If KARAKEEP_RANDOM_RSS_VERSION is absent from the file, this fails rather
#     than appending it — a missing key almost always means the wrong
#     file was given, and silently appending could point a deploy at the
#     wrong stack's env file.
#   - If the file is already at the requested version, this makes no
#     change, prints a message, and exits 0. This must be idempotent: a
#     re-run CI job (retry, or workflow_dispatch after a partial failure)
#     must not fail just because a previous run already made the change.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
DEFAULT_FILE="$REPO_ROOT/deploy/percival/karakeep-random-rss.env"

usage() {
  cat <<EOF
Usage: scripts/bump-deployed-version.sh <version> [--file PATH]

  <version>     version to deploy, e.g. v0.1.0

Options:
  --file PATH   env file to rewrite (default: $DEFAULT_FILE)
  -h, --help    show this help
EOF
}

fail() {
  # $1 = message, $2 = optional exit code (default 1)
  echo "bump-deployed-version: $1" >&2
  exit "${2:-1}"
}

require_value() {
  # $1 = flag name, $2 = remaining arg count
  if [[ "$2" -lt 2 ]]; then
    echo "bump-deployed-version: missing value for $1" >&2
    usage >&2
    exit 1
  fi
}

file="$DEFAULT_FILE"
version=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --file)
      require_value "$1" "$#"
      file="$2"
      shift 2
      ;;
    -h | --help)
      usage
      exit 0
      ;;
    --*)
      echo "bump-deployed-version: unknown argument: $1" >&2
      usage >&2
      exit 1
      ;;
    *)
      if [[ -n "$version" ]]; then
        echo "bump-deployed-version: unexpected extra argument: $1" >&2
        usage >&2
        exit 1
      fi
      version="$1"
      shift
      ;;
  esac
done

if [[ -z "$version" ]]; then
  echo "bump-deployed-version: missing required <version> argument" >&2
  usage >&2
  exit 1
fi

if [[ ! "$version" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "bump-deployed-version: invalid version '$version' — expected e.g. v0.1.0" >&2
  usage >&2
  exit 1
fi

if [[ ! -f "$file" ]]; then
  fail "file not found: $file"
fi

# grep exits 1 when there's no match; that's a real (expected) outcome
# here, not a script error, so don't let `set -e` treat it as one.
old_line="$(grep -m1 -E '^KARAKEEP_RANDOM_RSS_VERSION=' "$file" || true)"
if [[ -z "$old_line" ]]; then
  fail "KARAKEEP_RANDOM_RSS_VERSION not found in $file — refusing to append it (this probably isn't the right env file)"
fi
old_version="${old_line#KARAKEEP_RANDOM_RSS_VERSION=}"

if [[ "$old_version" == "$version" ]]; then
  echo "==> already at $version; no change"
  exit 0
fi

sed -i -E "s/^KARAKEEP_RANDOM_RSS_VERSION=.*/KARAKEEP_RANDOM_RSS_VERSION=${version}/" "$file"

echo "==> KARAKEEP_RANDOM_RSS_VERSION: ${old_version} -> ${version}"
