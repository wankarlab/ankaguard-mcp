# Conectar un cliente MCP

El servidor habla MCP sobre **JSON-RPC 2.0 por HTTP** en `POST /mcp`, puerto `10600` (por defecto solo en `127.0.0.1`). Cada llamada lleva un **token de agente** como Bearer.

## 1. Crear un token de agente

Con una sesión de operador (ver [install.md](install.md)):

```bash
# Iniciar sesión (devuelve {"token": "...", "expires_at": ...}); la sesión dura 12 horas
curl -s -X POST http://127.0.0.1:8081/api/v1/auth/local/login \
  -H 'Content-Type: application/json' -d '{"password": "<tu contraseña>"}'

# Crear un token de agente de perfil paper
curl -s -X POST http://127.0.0.1:8081/api/v1/auth/local/tokens \
  -H "Authorization: Bearer $OPERATOR_SESSION" -H 'Content-Type: application/json' \
  -d '{"name": "mi-agente", "profile": "paper", "expires_in_days": 30}'
```

El puerto de la API REST (`8081` en el contrato de `docs/product/api-contract.md`) puede ser distinto en tu instalación. El token de agente se muestra una sola vez al crearlo. Guárdalo como secreto y no lo pegues en repositorios. Se puede listar y revocar (`GET`/`DELETE /api/v1/auth/local/tokens`). Un token `live` no se puede crear sin licencia Pro válida.

## 2. Listar tools

```bash
export TOKEN='<token de agente>'

curl -s http://127.0.0.1:10600/mcp \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}'
```

`tools/list` devuelve solo las tools que el perfil del token puede ejecutar: con `read`, las de clase `read`; con `paper`, las de clase `read`, `trade_increase`, `trade_reduce` y `safety`.

## 3. Llamar a una tool

```bash
curl -s http://127.0.0.1:10600/mcp \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":2,"method":"tools/call",
       "params":{"name":"get_ticker","arguments":{"exchange":"kucoin","pair":"BTC-USDT"}}}'
```

Los nombres y esquemas de argumentos exactos de cada tool están en el catálogo [../tools.md](../tools.md) y en la respuesta de `tools/list` (campo `inputSchema`). El ejemplo de `get_ticker` usa nombres de argumento ilustrativos: consulta el esquema real antes de usarlo.

## Rechazos

Un rechazo es un error JSON-RPC con `error.data.code`. Los más habituales: `PERMISSION_DENIED` (-32003, el perfil no permite la clase de la tool), `LICENSE_REQUIRED` (-32006), `LIVE_TRADING_DISABLED` (-32007), `DATA_UNAVAILABLE` (-32005), `ORDER_CAP_EXCEEDED` y `ORDER_NOTIONAL_UNKNOWN` (-32010, el centinela de riesgo bloqueó la orden). Lista completa en [../api-contract.md](../api-contract.md).

## Configurar un cliente (forma genérica)

Los clientes MCP con transporte HTTP (por ejemplo Claude Desktop o Claude Code) se configuran con la URL del servidor y una cabecera de autorización. La forma general es:

```json
{
  "mcpServers": {
    "trading": {
      "type": "http",
      "url": "http://127.0.0.1:10600/mcp",
      "headers": { "Authorization": "Bearer <token de agente>" }
    }
  }
}
```

Los nombres exactos de las claves (`type`, `url`, `headers`) y el archivo donde van dependen del cliente y de su versión: consulta su documentación. Algunos clientes solo admiten transporte stdio y necesitan un puente HTTP; no se ha verificado ninguno concreto contra este servidor.

Empieza siempre con un token `paper` o `read`.
