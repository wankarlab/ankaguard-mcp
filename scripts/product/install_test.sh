#!/usr/bin/env bash
# Install and reinstall tests of the product stack (OpenSpec tasks 10.7, 10.8).
#
#   scripts/product/install_test.sh            # Part A only (no Docker needed)
#   INSTALL_TEST_DOCKER=1 scripts/product/install_test.sh   # Part A + Part B
#
# Part A (10.8, and the .env half of 10.7) runs install.sh in temporary
# directories: random secrets with the right shape, mode 600, nothing printed,
# a second run preserves the file byte for byte, two installs differ.
#
# Part B (10.7, needs a Docker daemon and network to GitHub/Docker Hub) runs the
# real flow in an empty directory: install, `docker compose up`, healthy in
# under 5 minutes, set the operator password, create a `paper` token, list tools
# with an MCP call and place a paper order, then check that a reinstall keeps
# the secrets AND the data (the operator session still works). It uses the
# compose project name "tmcp-install-test" and alternative host ports; it never
# touches ports 10600, 8081, 9090 or 9052.
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
INSTALL="$HERE/../../deploy/product/install.sh"
[ -x "$INSTALL" ] || { echo "install_test: cannot find $INSTALL" >&2; exit 2; }

WORK="$(mktemp -d)"
cleanup() {
  if [ "${DOCKER_STARTED:-0}" = 1 ]; then
    (cd "$WORK/stack/deploy/product" && docker compose -p tmcp-install-test down -v >/dev/null 2>&1) || true
  fi
  rm -rf "$WORK"
}
trap cleanup EXIT

fail=0
ok()  { echo "ok   - $1"; }
bad() { echo "FAIL - $1" >&2; fail=1; }
getv() { grep -m1 "^$2=" "$1" | cut -d= -f2-; }   # never echoed to the terminal

SECRET_KEYS=(GCT_PASSWORD TRADING_CONFIG_DB_PASSWORD CREDENTIAL_ENCRYPTION_KEY CONFIG_ENCRYPTION_KEY ADMIN_API_TOKEN)

# ---------------------------------------------------------------- Part A
mkdir -p "$WORK/a" "$WORK/b"
out_a="$("$INSTALL" "$WORK/a" 2>&1)"
out_b="$("$INSTALL" "$WORK/b" 2>&1)"

[ -f "$WORK/a/.env" ] && ok "install creates .env in an empty directory" || bad "install did not create .env"
[ "$(stat -c %a "$WORK/a/.env")" = 600 ] && ok ".env is mode 600" || bad ".env must be mode 600"
ls -A "$WORK/a" | grep -qv '^\.env$' && bad "install left temporary files behind: $(ls -A "$WORK/a" | tr '\n' ' ')" || ok "no temporary files left behind"

leaked=0
for k in "${SECRET_KEYS[@]}"; do
  v="$(getv "$WORK/a/.env" "$k")"
  [ -n "$v" ] || { bad "$k is empty"; continue; }
  if grep -qF -- "$v" <<<"$out_a$out_b"; then leaked=1; fi
done
[ "$leaked" -eq 0 ] && ok "install never prints a secret" || bad "install printed a secret value"

# Shapes: hex for passwords/token, base64 of 32 bytes for the encryption keys.
shape_ok=1
[[ "$(getv "$WORK/a/.env" GCT_PASSWORD)" =~ ^[0-9a-f]{48}$ ]] || shape_ok=0
[[ "$(getv "$WORK/a/.env" TRADING_CONFIG_DB_PASSWORD)" =~ ^[0-9a-f]{48}$ ]] || shape_ok=0
[[ "$(getv "$WORK/a/.env" ADMIN_API_TOKEN)" =~ ^[0-9a-f]{64}$ ]] || shape_ok=0
for k in CREDENTIAL_ENCRYPTION_KEY CONFIG_ENCRYPTION_KEY; do
  [ "$(getv "$WORK/a/.env" "$k" | base64 -d 2>/dev/null | wc -c)" -eq 32 ] || shape_ok=0
done
[ "$shape_ok" -eq 1 ] && ok "secrets have the expected shape (hex / base64 of 32 bytes)" || bad "unexpected secret shape"

# 10.8: reinstall preserves the file, byte for byte.
sum_before="$(sha256sum "$WORK/a/.env" | cut -d' ' -f1)"
out_again="$("$INSTALL" "$WORK/a" 2>&1)"
sum_after="$(sha256sum "$WORK/a/.env" | cut -d' ' -f1)"
[ "$sum_before" = "$sum_after" ] && ok "reinstall leaves .env untouched" || bad "reinstall changed .env"
grep -q "already exists" <<<"$out_again" && ok "reinstall says it kept the file" || bad "reinstall message missing"

# A pre-existing hand-written .env is never overwritten either.
mkdir -p "$WORK/c"; printf 'CUSTOM=1\n' > "$WORK/c/.env"
"$INSTALL" "$WORK/c" >/dev/null 2>&1
[ "$(cat "$WORK/c/.env")" = "CUSTOM=1" ] && ok "an existing custom .env is preserved" || bad "an existing .env was overwritten"

# 10.8: two installs have different secrets (every one of them).
same=0
for k in "${SECRET_KEYS[@]}"; do
  [ "$(getv "$WORK/a/.env" "$k")" = "$(getv "$WORK/b/.env" "$k")" ] && { bad "$k is identical across installs"; same=1; }
done
[ "$same" -eq 0 ] && ok "two installs have different secrets" || true

# Compose file sanity without a daemon: ports bound to loopback only.
COMPOSE="$HERE/../../deploy/product/docker-compose.yml"
if grep -nE '^\s*-\s*"?[0-9]+:[0-9]+' "$COMPOSE" >/dev/null; then bad "a published port is not bound to 127.0.0.1"; else ok "every published port is bound to 127.0.0.1"; fi
if grep -nE '^\s*-\s*"?0\.0\.0\.0:' "$COMPOSE" >/dev/null; then bad "a port is bound to 0.0.0.0"; fi

# ---------------------------------------------------------------- Part B
if [ "${INSTALL_TEST_DOCKER:-0}" != 1 ]; then
  echo "skip - Part B (clean install with Docker, task 10.7) needs INSTALL_TEST_DOCKER=1 and a Docker daemon"
  [ "$fail" -eq 0 ] && { echo "install_test: Part A passed (Part B not run)"; exit 0; } || { echo "install_test: FAILED" >&2; exit 1; }
fi

docker info >/dev/null 2>&1 || { echo "install_test: no Docker daemon" >&2; exit 2; }
command -v jq >/dev/null || { echo "install_test: jq is required for Part B" >&2; exit 2; }

# Clean directory = an export of the product (same layout a customer gets).
mkdir -p "$WORK/stack"
if [ -x "$HERE/export.sh" ]; then
  ALLOW_PLACEHOLDER_KEY=1 "$HERE/export.sh" "$WORK/stack" >/dev/null
else
  # Inside the product repository (no export.sh): the repository is the export.
  (cd "$HERE/../.." && tar --exclude=./.git -cf - .) | tar -xf - -C "$WORK/stack"
fi
cd "$WORK/stack/deploy/product"
"./install.sh" >/dev/null
# Alternative host ports so the test cannot collide with a running instance.
printf 'SERVER_PORT=%s\nCONSOLE_PORT=%s\n' "${TEST_SERVER_PORT:-18600}" "${TEST_CONSOLE_PORT:-18602}" >> .env
export COMPOSE_PROJECT_NAME=tmcp-install-test
DOCKER_STARTED=1
SERVER="http://127.0.0.1:${TEST_SERVER_PORT:-18600}"

start=$SECONDS
docker compose -p tmcp-install-test up -d --build server >/dev/null
healthy=0
while [ $((SECONDS - start)) -lt 300 ]; do
  if [ "$(curl -fsS "$SERVER/health" 2>/dev/null | jq -r .status 2>/dev/null)" = ok ]; then healthy=1; break; fi
  sleep 3
done
[ "$healthy" -eq 1 ] && ok "healthy in $((SECONDS - start))s (limit 300s, build included)" || { bad "server not healthy within 5 minutes"; exit 1; }

PW="install-test-$(openssl rand -hex 6)"
dexec() { docker compose -p tmcp-install-test exec -T server "$@"; }
printf '%s\n' "$PW" | dexec trading-mcp-server -config /app/configs/product/config.yaml operator set-password >/dev/null 2>&1 \
  && ok "operator password set" || bad "operator set-password failed"
rest() { # <method> <path> <token> [json]  (REST is not published on the host: call it inside the container)
  dexec curl -sS -X "$1" -H 'Content-Type: application/json' ${3:+-H "Authorization: Bearer $3"} ${4:+-d "$4"} "http://127.0.0.1:8081$2"
}
login_out="$(rest POST /api/v1/auth/local/login "" "{\"password\":\"$PW\"}")"
OP="$(jq -r '.token // empty' <<<"$login_out" 2>/dev/null)"
# On failure show the response without any token value.
[ -n "$OP" ] && ok "operator login" || bad "operator login failed: $(jq -c 'del(.token)' <<<"$login_out" 2>/dev/null || echo "${login_out:0:200}")"
PAPER="$(rest POST /api/v1/auth/local/tokens "$OP" '{"name":"install-test","profile":"paper","expires_in_days":1}' | jq -r .token)"
[ -n "$PAPER" ] && [ "$PAPER" != null ] && ok "paper token created" || bad "paper token not created"
mcp() { curl -sS -X POST -H 'Content-Type: application/json' -H "Authorization: Bearer $PAPER" -d "$1" "$SERVER/mcp"; }
n="$(mcp '{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}' | jq '.result.tools | length')"
[ "${n:-0}" -ge 30 ] && ok "tools/list returns $n tools" || bad "tools/list returned ${n:-0} tools"
# GoCryptoTrader may still be loading markets when the server is already
# healthy: retry for up to 90 s while the answer is a data error.
for _ in $(seq 1 30); do
  order="$(mcp '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"submit_market_order","arguments":{"exchange":"kucoin","pair":"BTC-USDT","side":"buy","amount":0.00002}}}')"
  jq -e '.error' <<<"$order" >/dev/null 2>&1 || break
  sleep 3
done
# Needs GoCryptoTrader reachable for a price; without it the paper broker answers DATA_UNAVAILABLE.
if grep -q paper <<<"$order" && ! jq -e '.error' <<<"$order" >/dev/null 2>&1; then ok "paper order executed"; else bad "paper order failed: $(jq -c '.error // .' <<<"$order" 2>/dev/null | cut -c1-400)"; fi

# 10.8 with data: secrets and data survive a reinstall and a restart.
sum_before="$(sha256sum .env | cut -d' ' -f1)"
./install.sh >/dev/null
[ "$sum_before" = "$(sha256sum .env | cut -d' ' -f1)" ] && ok "reinstall keeps .env" || bad "reinstall changed .env"
docker compose -p tmcp-install-test up -d --build server >/dev/null
sleep 5
[ -n "$(rest POST /api/v1/auth/local/login "" "{\"password\":\"$PW\"}" | jq -r '.token // empty')" ] && ok "operator password (data) survives the reinstall" || bad "data lost on reinstall"

[ "$fail" -eq 0 ] && echo "install_test: all passed" || { echo "install_test: FAILED" >&2; exit 1; }
