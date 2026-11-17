#!/usr/bin/env bash
# Self-test of secrets_gate.sh (OpenSpec task 10.3). It builds fixtures at run
# time (credential-shaped strings are assembled from pieces so this file itself
# does not trip the gate) and checks that:
#   - each bad fixture is reported with file, line and rule,
#   - no matched value ever appears in the gate output,
#   - a clean tree and .env.example pass.
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
GATE="$HERE/secrets_gate.sh"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
fail=0
ok()  { echo "ok   - $1"; }
bad() { echo "FAIL - $1" >&2; fail=1; }

# Pieces (never written together in this source file).
P1="-----BEGIN RSA"; P2=" PRIVATE KEY-----"
A1="AKIA"; A2="IOSFODNN7EXAMPLQ"
J1="eyJhbGciOiJIUzI1NiJ9"; J2="eyJzdWIiOiIxMjM0NTY3ODkwIn0"; J3="dBjftJeZ4CVPmB92K27uhbUJU1p1r_wW1gFWFOEjXk"
G1="ghp_"; G2="aB3dE5gH7jK9mN1pQ3sT5vX7zC9fH2kM4nP6"
K1="3f9a1c7e2b4d6f8a0c1e3a5b"            # 24 hex chars
S1="Zx9Qw3Er7Ty1Ui5Op8As2Df6Gh0Jk4Lm"
V_LIST=("$A1$A2" "$J1.$J2.$J3" "$G1$G2" "$K1" "$S1")

expect_hit() { # <label> <relative path> <rule>
  local label="$1" path="$2" rule="$3" out
  out="$("$GATE" "$TMP" 2>&1 || true)"
  if grep -q "^$path:.*$rule" <<<"$out" || grep -q "^$path:[0-9]*: $rule" <<<"$out"; then ok "$label"; else bad "$label (expected $path / $rule)"; echo "$out" >&2; fi
  for v in "${V_LIST[@]}"; do
    if grep -qF -- "$v" <<<"$out"; then bad "$label leaked a value"; fi
  done
  rm -rf "${TMP:?}"/*; rm -f "$TMP"/.env*
}

# Clean tree passes.
mkdir -p "$TMP/cfg"
printf 'server:\n  port: 10600\n' > "$TMP/cfg/config.yaml"
printf 'GCT_PASSWORD=\nSESSION_SECRET=change-me-please-1234\n' > "$TMP/.env.example"
printf 'password: "${TRADING_CONFIG_DB_PASSWORD}"\n' > "$TMP/cfg/db.yaml"
if "$GATE" "$TMP" >/dev/null 2>&1; then ok "clean tree passes"; else bad "clean tree must pass"; "$GATE" "$TMP" >&2 || true; fi
rm -rf "${TMP:?}"/*; rm -f "$TMP"/.env*

printf 'x\n%s%s\nabc\n' "$P1" "$P2" > "$TMP/key.txt";            expect_hit "private key PEM" "key.txt:2" "private-key-pem"
printf 'a\nid=%s%s\n' "$A1" "$A2" > "$TMP/aws.conf";              expect_hit "AWS key" "aws.conf:2" "aws-access-key"
printf 'token: %s.%s.%s\n' "$J1" "$J2" "$J3" > "$TMP/t.yaml";     expect_hit "JWT" "t.yaml:1" "jwt-token"
printf 'x=%s%s\n' "$G1" "$G2" > "$TMP/gh.sh";                     expect_hit "GitHub token" "gh.sh:1" "github-token"
printf '\nkucoin_key = "%s"\n' "$K1" > "$TMP/k.ini";              expect_hit "KuCoin key" "k.ini:2" "kucoin-credential"
printf 'JWT_SECRET=%s\n' "$S1" > "$TMP/app.env.txt";              expect_hit "generic secret assignment" "app.env.txt:1" "high-entropy-credential-assignment"
printf 'DB_PASSWORD: "%s"\n' "$S1" > "$TMP/c.yml";                expect_hit "generic password assignment" "c.yml:1" "high-entropy-credential-assignment"
printf 'A=1\n' > "$TMP/.env";                                     expect_hit "real .env" ".env" "real-env-file"
printf 'x' > "$TMP/data.db";                                      expect_hit "database" "data.db" "database-file"
printf '\177ELFxxxxxxxx' > "$TMP/prog";                          expect_hit "ELF binary" "prog" "binary-elf"
printf '\317\372\355\376xxxxxxxx' > "$TMP/prog2";                expect_hit "Mach-O binary" "prog2" "binary-macho"
mkdir -p "$TMP/web/node_modules/a"; printf 'x' > "$TMP/web/node_modules/a/i.js"; expect_hit "node_modules" "web/node_modules/a/i.js" "build-artifact"
mkdir -p "$TMP/bin"; printf 'x' > "$TMP/bin/t"; expect_hit "bin directory" "bin/t" "build-artifact"
printf 'password = "%s" # secrets-gate: allow\n' "$S1" > "$TMP/ok.ini"
if "$GATE" "$TMP" >/dev/null 2>&1; then ok "inline allow marker"; else bad "inline allow marker"; fi

[ "$fail" -eq 0 ] && echo "secrets_gate_test: all passed" || { echo "secrets_gate_test: FAILED" >&2; exit 1; }
