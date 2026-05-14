#!/usr/bin/env bash
# coverage-gate.sh — per-package coverage gate.
#
# Usage: coverage-gate.sh <coverage.out> <min-percent>
#
# Gates packages under internal/, models/, contracts/, providers/. cmd/, test/,
# and generated code are excluded. Passes silently when the profile is empty or
# no gated packages are present (scaffold PRs have no Go code yet).

set -euo pipefail

cover="${1:-coverage.out}"
min="${2:-95}"

if [[ ! -s "$cover" ]]; then
  echo "coverage: no profile at $cover; treating as pass (no Go code yet)"
  exit 0
fi

module=$(awk '/^module / { print $2; exit }' go.mod 2>/dev/null || echo "")
if [[ -z "$module" ]]; then
  echo "coverage: cannot read module path from go.mod" >&2
  exit 1
fi

# coverage.out lines after `mode:` header:
#   <import-path>/<file>:startLine.col,endLine.col numStmt count
exec awk -v module="$module" -v min="$min" '
BEGIN {
  gated_re = "^" module "/(internal|models|contracts|providers)(/|$)"
}
/^mode:/ { next }
NF == 0 { next }
{
  file_part = $1
  num = $2 + 0
  cnt = $3 + 0
  i = index(file_part, ":")
  if (i == 0) next
  file = substr(file_part, 1, i - 1)
  j = length(file)
  while (j > 0 && substr(file, j, 1) != "/") j--
  if (j == 0) next
  pkg = substr(file, 1, j - 1)
  total[pkg] += num
  if (cnt > 0) covered[pkg] += num
}
END {
  gated = 0
  failed = 0
  for (pkg in total) {
    if (pkg !~ gated_re) continue
    gated = 1
    tot = total[pkg]
    cov = covered[pkg] + 0
    if (tot == 0) continue
    pct = (cov * 100.0) / tot
    short = pkg
    sub("^" module "/", "", short)
    if (pct + 0 < min + 0) {
      printf "FAIL: %s coverage %.1f%% < %s%%\n", short, pct, min
      failed = 1
    } else {
      printf "ok:   %s coverage %.1f%%\n", short, pct
    }
  }
  if (gated == 0) {
    print "coverage: no gated packages present; treating as pass"
    exit 0
  }
  if (failed) {
    print "coverage: gate failed (threshold " min "%)" > "/dev/stderr"
    exit 1
  }
  print "coverage: gate passes (all gated packages >= " min "%)"
}
' "$cover"
