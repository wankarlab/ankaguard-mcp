#!/usr/bin/env bash
# Secrets and artifacts gate for the product export (OpenSpec task 10.3).
#
#   scripts/product/secrets_gate.sh <dir>
#
# Fails (exit 1) when <dir> contains:
#   - credential-shaped strings (private keys, AWS/GitHub/Google/Slack/LLM keys,
#     JWTs, KuCoin key/secret/passphrase literals, high-entropy values assigned
#     to *_SECRET / *_KEY / *_PASSWORD / *_PASSPHRASE / *_TOKEN names);
#   - real environment files (.env, .env.local, ...; .env.example is fine);
#   - key/credential files (id_rsa, *.pem with a private key, *.key, *.p12, ...);
#   - databases (*.db, *.sqlite, *.sqlite3, *.db-wal, *.db-shm);
#   - compiled binaries (ELF / Mach-O / PE by magic number);
#   - build artifacts (node_modules, .next, bin/, dist/, build/, __pycache__).
#
# It prints "<path>:<line>: <rule>" and NEVER the matched value. A line that
# carries the marker `secrets-gate: allow` is ignored (use it for documented
# placeholders only).
set -euo pipefail
export LC_ALL=C

[ $# -eq 1 ] && [ -d "$1" ] || { echo "usage: secrets_gate.sh <dir>" >&2; exit 2; }
DIR="$(cd "$1" && pwd)"
cd "$DIR"

findings="$(mktemp)"
trap 'rm -f "$findings"' EXIT
report() { printf '%s\n' "$1" >> "$findings"; }

# --- Path-based rules ---------------------------------------------------------
while IFS= read -r p; do
  base="${p##*/}"
  case "$base" in
    .env.example|.env.sample|.env.template|.env.dist) ;;
    .env|.env.*) report "$p: real-env-file" ;;
  esac
  case "$base" in
    *.db|*.sqlite|*.sqlite3|*.db-wal|*.db-shm|*.db-journal) report "$p: database-file" ;;
    id_rsa|id_dsa|id_ecdsa|id_ed25519|*.key|*.p12|*.pfx|*.jks|*.keystore) report "$p: key-file" ;;
  esac
  case "/$p/" in
    */node_modules/*|*/.next/*|*/bin/*|*/dist/*|*/build/*|*/__pycache__/*|*/.venv/*|*/target/*)
      report "$p: build-artifact" ;;
  esac
done < <(find . -path ./.git -prune -o -type f -print | sed 's|^\./||' | sort)

# --- Binaries by magic number ---------------------------------------------------
while IFS= read -r p; do
  magic="$(head -c 4 -- "$p" 2>/dev/null | od -An -tx1 | tr -d ' \n')"
  case "$magic" in
    7f454c46) report "$p: binary-elf" ;;
    feedface|feedfacf|cefaedfe|cffaedfe|cafebabe|bebafeca) report "$p: binary-macho" ;;
    4d5a*) case "$p" in *.go|*.md|*.txt|*.sh|*.yml|*.yaml|*.json|*.sql|*.js|*.ts|*.tsx|*.css|*.html) ;; *) report "$p: binary-pe" ;; esac ;;
  esac
done < <(find . -path ./.git -prune -o -type f -size +3c -print | sed 's|^\./||' | sort)

# --- Content rules (text files only, never printing the value) -------------------
textfiles="$(mktemp)"; trap 'rm -f "$findings" "$textfiles"' EXIT
find . -path ./.git -prune -o -type f -size -6M -print | sed 's|^\./||' | sort \
  | while IFS= read -r p; do grep -Iq . -- "$p" 2>/dev/null && printf '%s\n' "$p"; done > "$textfiles" || true

# Fixed-shape credentials: one grep per rule, keep only file:line.
fixed_rule() { # <rule-id> <ERE>
  local id="$1" re="$2"
  xargs -d '\n' grep -HnE -- "$re" < "$textfiles" 2>/dev/null \
    | grep -v 'secrets-gate: allow' \
    | cut -d: -f1,2 | sed "s/\$/: $id/" | while IFS= read -r l; do report "$l"; done || true
}
fixed_rule private-key-pem   '-----BEGIN ([A-Z]+ )?PRIVATE KEY( BLOCK)?-----'
fixed_rule aws-access-key    '(AKIA|ASIA)[0-9A-Z]{16}'
fixed_rule github-token      '(ghp|gho|ghu|ghs|ghr)_[A-Za-z0-9]{30,}|github_pat_[A-Za-z0-9_]{40,}'
fixed_rule google-api-key    'AIza[0-9A-Za-z_-]{35}'
fixed_rule slack-token       'xox[baprs]-[0-9A-Za-z-]{10,}'
fixed_rule llm-api-key       "sk-(ant-|proj-|or-)?[A-Za-z0-9_-]{32,}"
fixed_rule jwt-token         'eyJ[A-Za-z0-9_-]{10,}\.eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}'
# KuCoin: a line that mentions kucoin together with a 24-hex key or a UUID literal.
xargs -d '\n' grep -HniE -- "kucoin" < "$textfiles" 2>/dev/null \
  | grep -E '(^|[^0-9a-fA-F])([0-9a-f]{24}|[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})([^0-9a-fA-F]|$)' \
  | grep -v 'secrets-gate: allow' | cut -d: -f1,2 | sed 's/$/: kucoin-credential/' \
  | while IFS= read -r l; do report "$l"; done || true

# Generic: NAME ending in SECRET/KEY/PASSWORD/PASSPHRASE/TOKEN assigned a literal
# of >= 16 chars with letters and digits and entropy >= 3.5 bits/char
# (Go test files are skipped by this heuristic rule; the fixed-shape rules still apply).
# The value is extracted and judged inside awk and never printed.
xargs -d '\n' awk '
  function entropy(s,   n, i, c, cnt, h, p) {
    n = length(s); if (n == 0) return 0
    delete cnt
    for (i = 1; i <= n; i++) cnt[substr(s, i, 1)]++
    h = 0
    for (c in cnt) { p = cnt[c] / n; h -= p * log(p) / log(2) }
    return h
  }
  function placeholder(v,   l) {
    l = tolower(v)
    return (l ~ /(change|example|your|replace|placeholder|dummy|fake|sample|xxxx|\*\*\*\*|redacted|secret_here|todo|env\.|getenv|process)/)
  }
  FNR == 1 { fname = FILENAME; skipfile = (fname ~ /_test\.go$/) }
  skipfile { next }
  /secrets-gate: allow/ { next }
  {
    line = $0
    low = tolower(line)
    if (low !~ /(secret|key|password|passphrase|token)[a-z0-9_]*["\047]?[ \t]*(:=|=|:)[ \t]*["\047]?[a-z0-9+\/_=.-]{16,}/) next
    # Take the text after the first assignment operator following such a name.
    if (match(low, /(secret|key|password|passphrase|token)[a-z0-9_]*["\047]?[ \t]*(:=|=|:)[ \t]*["\047]?/)) {
      start = RSTART + RLENGTH
      rest = substr(line, start)
      if (match(rest, /^[A-Za-z0-9+\/_=.-]+/)) {
        v = substr(rest, 1, RLENGTH)
        after = substr(rest, RLENGTH + 1, 1)
        # Code and config identifiers are not secrets: calls (value followed by
        # "("), dotted names, snake_case words and UPPER_CASE constants.
        if (after == "(" || v ~ /\./ || v ~ /^[a-z0-9_\/-]+$/ || v ~ /^[A-Z0-9]+(_[A-Z0-9]+)+$/) next
        if (length(v) >= 16 && v ~ /[0-9]/ && v ~ /[A-Za-z]/ && v !~ /^\$/ && !placeholder(v) && entropy(v) >= 3.5) {
          print fname ":" FNR ": high-entropy-credential-assignment"
        }
      }
    }
  }
' < "$textfiles" 2>/dev/null | while IFS= read -r l; do report "$l"; done || true

if [ -s "$findings" ]; then
  echo "secrets-gate: FAILED. Findings (path:line: rule; values are never printed):" >&2
  sort -u "$findings" >&2
  exit 1
fi
echo "secrets-gate: OK ($(wc -l < "$textfiles") text files scanned in $DIR)"
