# Rutas montadas y su requisito de autenticación

Regla: toda ruta de :10600 (mux), :8081 (gin) y :9090 (gateway) exige un token válido salvo las listadas en `internal/routepolicy/routepolicy.go` (tabla `Open`), que es la fuente de verdad. Las pruebas `routes_audit_test.go` (mcp-trading/server, gateway, api/v1) envían una petición sin token a cada ruta montada y fallan si una ruta responde sin token y no está en la tabla.

Clases: `public` (sin datos sensibles), `self` (se autentica dentro del manejador: WebSocket, sesión de operador, firma de webhook), `pending` (sin autenticación hoy, brecha conocida).

## :10600 (MCP)

| Ruta | Requisito |
|---|---|
| `/health` | pública, solo estado, versión, commit y edición |
| `/ws` | self: valida el token antes del upgrade |
| `/mcp`, `/health/detail`, `/metrics`, `/tools`, `/backtesting`, `/api/v1/backtesting/*`, `/api/v1/ws/{stats,channels,rooms}` | token |
| `/debug`, `/test`, `/simple-test` | eliminadas |

## :9090 (gateway)

Públicas: `/health`, `/api/health`, `/ready`, `/api/ready`, `/api/v1/health`. `/ws`: self. `/info`, `/api/info`, `/api/gateway/info`, `/metrics`, `/api/metrics/*`: token. CORS: solo los orígenes configurados (los del panel gin), sin comodín.

## :8081 (REST)

Públicas o self: las filas `rest` de la tabla (login proxy, webhooks, estadísticas de solo lectura, `/api/v1/auth/local/*`, `/api/v1/stream`). Todo lo demás de `/api/v1` pasa por el middleware JWT, incluidos `/api/v1/product/status` y el reinicio de papel.

`/admin/config/*` y `/admin/ml/*` (plano de `X-Admin-Token`): en la edición producto, si `ADMIN_API_TOKEN` está vacío, responden siempre 401 (fallo cerrado). En la compilación estándar se conserva el comportamiento anterior (abiertas con aviso salvo `RANTIPAY_ENV=production`).

## Tokens de agente (modo `standalone`)

Un token de agente (`token_use: agent`) solo se acepta en los endpoints JSON-RPC: `/mcp`, `/jsonrpc` y `/` en :10600, y sus alias `/` y `/api/mcp` en :9090. También sirve para el WebSocket de solo lectura `/ws`. En cualquier otra ruta de :10600, :8081 o :9090 (incluido lo que la consola reenvía) la respuesta es 403 con `{"code":"OPERATOR_REQUIRED"}`: la API REST exige una sesión de operador. En modo `external` no cambia nada.

## Solo en la compilación estándar

No se montan en la edición producto:

- `PATCH/GET /api/v1/bot/tuning` y `/api/v1/bot/tuning/*` (:10600): parámetros del bot de IA, que el producto no ejecuta.
- `POST /api/v1/user/fcm-token` (:10600): registro de dispositivos para avisos de RantiPay.
- Los manejadores heredados de la sección siguiente.

## Pendientes (decisión de producto)

Manejadores heredados registrados directamente en `main.go` sin autenticación. En la compilación estándar se dejaron como estaban para no cambiar el comportamiento de este despliegue: `analysis/*`, `monitoring/*`, `knowledge/*` (incluye `POST /knowledge/reload`), `validation/*`, `strategies` (POST, PUT, DELETE), `metrics/*`, `trades/*`, `storage/*` (incluye `POST /storage/cache/clean`), `fees/*` (incluye `POST /fees/accrue`) y `strategy-validation` (incluye `POST /strategy-validation/run`). La consola no usa `fees/*` ni `strategy-validation`.

En la edición producto los manejadores heredados no se montan, y `fees/*` y `strategy-validation` se montan detrás del middleware JWT. La prueba `TestMainMountedRESTRoutes` (cmd/trading-mcp-server) audita estas rutas con las mismas funciones que usa `main.go` (`mountLegacyRoutes`, `productGuarded`) en ambas ediciones.
