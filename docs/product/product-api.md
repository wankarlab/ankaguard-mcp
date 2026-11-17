# API de producto

Rutas montadas en :8081 detrás del middleware JWT (`internal/productapi`).

| Ruta | Auth | Descripción |
|---|---|---|
| `GET /api/v1/product/status` | token | Edición, build, `auth_mode`, `live_enabled` y estado de licencia |
| `POST /api/v1/paper/accounts/:holder/reset` | sesión de operador | Reinicia una cuenta de papel; `403 OPERATOR_REQUIRED` con token de agente |

## Punto de enlace con la licencia

`productapi.LicenseSource` es una interfaz de un solo método, `Status(ctx) LicenseStatus`. `main.go` la implementa con `licenseStatusSource` (`cmd/trading-mcp-server/product_wiring.go`), que adapta `license.Manager` (licencia leída de `LICENSE_KEY`, `LICENSE_FILE` o del documento guardado en `auth.data_dir`). La misma fuente alimenta `GET /health` (`edition`). Las rutas `GET`/`PUT /api/v1/license` se montan con `operatorOnly`. En la compilación estándar la licencia solo se informa; con la etiqueta `product`, `tools/call` (`LICENSE_REQUIRED`, -32006) y la creación de tokens `live` (402) exigen compilación `pro` y licencia Pro en estado `valid` o `grace`. La edición Community (`product` sin `pro`) no permite dinero real con ninguna licencia.

## `trading.live_enabled`

Entrada del manifiesto (booleano, por defecto `true`). Con `false`, `/mcp` rechaza con `LIVE_TRADING_DISABLED` (-32007) las llamadas `trade_increase` y `control` del perfil `live`.

Decisión documentada: NO detiene el bot de IA, el scalping interno ni XSEC. Esos motores no pasan por `/mcp` y tienen sus propios topes y su interruptor; detenerlos por este parámetro cambiaría el comportamiento del despliegue actual. El arranque rechaza `ENABLE_JWT_AUTH=false` solo si hay dinero real habilitado (exchange con `sandbox=false` y `trading.live_enabled=true`); en otro caso avisa.

## Configuración nueva

| Clave | Env | Por defecto | Efecto |
|---|---|---|---|
| `auth.mode` | `AUTH_MODE` | `external` | `standalone` usa el secreto de `internal/localauth` en todas las capas JWT |
| `auth.data_dir` | `AUTH_DATA_DIR` | `data/auth` | Dónde guarda `localauth` el secreto |
| `server.bind_address` | `MCP_BIND_ADDRESS` | vacío (comportamiento actual) | Interfaz de :10600, :9090 y :8081; avisa por puerto si no es local |

Subcomando: `trading-mcp-server operator set-password` y `operator rotate-secret`, ejecutados antes de conectar nada.
