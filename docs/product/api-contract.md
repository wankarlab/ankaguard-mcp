# Contrato de la API del producto

Rutas nuevas que comparten el servidor, la licencia y la consola. Todas cuelgan del
servidor REST (gin, `:8081` en este despliegue) bajo `/api/v1`. Los errores siguen la
forma `{"error": "<texto>", "code": "<CÓDIGO>"}`.

"Sesión de operador" significa un JWT con `token_use = "operator_session"` (modo
`standalone`) o, en modo `external`, un token con nivel de usuario 4 o superior.

## Autenticación local (`internal/localauth`, ya implementado)

| Método y ruta | Auth | Respuesta |
|---|---|---|
| `GET /api/v1/auth/local/mode` | pública | `200 {"mode": "standalone"\|"external", "password_set": bool}` |
| `POST /api/v1/auth/local/login` `{"password"}` | pública | `200 {"token", "token_type": "Bearer", "expires_at"}`; `401 INVALID_CREDENTIALS`; `409 PASSWORD_NOT_SET`; `429 LOGIN_LOCKED` con cabecera `Retry-After` (segundos) |
| `GET /api/v1/auth/local/tokens` | operador | `200 {"tokens": [{"id", "name", "profile", "created_at", "expires_at", "last_used_at", "revoked_at"}]}` |
| `POST /api/v1/auth/local/tokens` `{"name", "profile", "expires_in_days"}` | operador | `201 {"token", "token_type", "agent_token": {…}}`; `400 INVALID_TOKEN_REQUEST`; `402 LICENSE_REQUIRED` si `profile = "live"` sin licencia Pro válida (edición de producto) |
| `DELETE /api/v1/auth/local/tokens/:id` | operador | `200 {"revoked": true}`; `404 NOT_FOUND` |

En modo `external` todas salvo `mode` responden `501 AUTH_MODE_EXTERNAL`. Sin sesión,
`401 UNAUTHORIZED`; con token de agente, `403 OPERATOR_REQUIRED`.

## Licencia (`internal/license`)

| Método y ruta | Auth | Respuesta |
|---|---|---|
| `GET /api/v1/license` | operador | `200 LicenseStatus` |
| `PUT /api/v1/license` `{"license": "<documento>"}` | operador | `200 LicenseStatus`; `400 {"code": "LICENSE_INVALID", "reason": "bad_signature"\|"malformed"\|"wrong_key"\|"expired"}` y la licencia anterior se conserva |

`LicenseStatus`:

```json
{
  "edition": "community" | "pro",
  "state": "valid" | "grace" | "expired" | "invalid",
  "holder": "string o null",
  "license_id": "string o null",
  "expires_at": "RFC3339 o null",
  "days_remaining": 12,
  "grace_days_remaining": 0,
  "warning": "expiring_soon" | "in_grace" | "clock_skew" | null
}
```

`edition` es la edición efectiva: `pro` solo con `state` en `valid` o `grace` y una
compilación Pro. El documento de licencia nunca aparece en ninguna respuesta.

## Estado del producto

| Método y ruta | Auth | Respuesta |
|---|---|---|
| `GET /health` (`:10600`) | pública | `200 {"status", "version", "commit", "edition"}` y nada más |
| `GET /api/v1/product/status` | cualquier token válido | `200 {"edition", "build": "standard"\|"product"\|"product-pro", "auth_mode", "live_enabled": bool, "license": LicenseStatus}` |

`live_enabled` es el valor de `trading.live_enabled`. La consola lo usa para el
indicador permanente de dinero real.

## Broker de papel

| Método y ruta | Auth | Respuesta |
|---|---|---|
| `POST /api/v1/paper/accounts/:holder/reset` | operador | `200 {"holder", "balance_usd"}`; `403 OPERATOR_REQUIRED` con token de agente |

El titular de la cuenta de papel de un token de agente es su `sub` (`agent:<jti>`).

## Códigos de error de `tools/call`

Errores JSON-RPC con `error.data.code`:

| `data.code` | `error.code` | Cuándo |
|---|---|---|
| `PERMISSION_DENIED` | -32003 | El perfil no permite la clase de la llamada |
| `PAPER_UNAVAILABLE` | -32004 | Perfil `paper` sin broker disponible |
| `DATA_UNAVAILABLE` | -32005 | No hay dato real |
| `LICENSE_REQUIRED` | -32006 | `trade_increase` o `control` con perfil `live` sin licencia `valid`/`grace` (edición de producto) |
| `LIVE_TRADING_DISABLED` | -32007 | Orden real con `trading.live_enabled = false` |
| `PAPER_UNSUPPORTED` | -32008 | Operación que el simulador no admite (p. ej. `execute_scalping_trade`, `start_scalping_bot`, stop-limit, trailing) |
| `PAPER_ORDER_REJECTED` | -32009 | El simulador rechaza la orden (cantidad o precio inválidos) |
| `ORDER_CAP_EXCEEDED`, `ORDER_NOTIONAL_UNKNOWN`, `RISK_REJECTED` | -32010 | Rechazo del centinela de riesgo, en real y en papel. `error.message` es el mismo código y `error.data.message` explica el nocional y el tope. `RISK_REJECTED` cubre los demás rechazos del centinela. |

## Salud

- `GET /health` (:10600, público): solo `{status, version, commit, edition}`.
- `GET /health/detail` (:10600, requiere token): el contenido anterior de `/health` (`gct_connected`, `engine_real_money`, etc.).
- `GET /api/v1/product/status` (:8081, requiere token): edición, build, modo de autenticación, `live_enabled` y estado de la licencia. Ver `product-api.md`.
