# Modelo de protecciones

Todas las protecciones se aplican en el servidor, no en el cliente: un agente mal configurado o malicioso no puede saltárselas.

## Clases de tools

Cada tool pertenece a exactamente una clase (fuente de verdad: `internal/mcp-trading/toolpolicy`; tabla completa en [../tool-classification.md](../tool-classification.md) y [../tools.md](../tools.md)):

| Clase | Qué hace |
|---|---|
| `read` | Consulta y cálculo sin efectos |
| `trade_increase` | Abre o amplía exposición, o arranca un bucle de trading |
| `trade_reduce` | Cancela, cierra o reduce exposición |
| `control` | Cambia configuración, apalancamiento, modo de margen o notificaciones |
| `safety` | Activa una parada de emergencia |

Una tool sin clase no se lista ni se ejecuta. Algunas tools dependen de los argumentos: `submit_futures_order_direct` es `trade_reduce` solo si `reduce_only` o `close_order` es el booleano `true`; en cualquier otro caso es `trade_increase`.

## Perfiles de token

| Clase | `read` | `paper` | `live` |
|---|---|---|---|
| `read` | permitido | permitido | permitido |
| `trade_increase` | denegado | broker de papel | exchange real |
| `trade_reduce` | denegado | broker de papel | exchange real |
| `control` | denegado | denegado | permitido |
| `safety` | denegado | permitido | permitido |

Un token sin perfil explícito se trata como `live` solo si su nivel de usuario es 4 o superior; en otro caso, `read`. Un perfil desconocido se trata como `read`.

## Orden de las comprobaciones en `tools/call`

1. Autenticación.
2. Clase de la llamada.
3. Permiso por perfil (`PERMISSION_DENIED`).
4. Parada de emergencia.
5. Licencia y dinero real habilitado, solo para `trade_increase` y `control` con perfil `live` (`LICENSE_REQUIRED`, `LIVE_TRADING_DISABLED`).
6. Ejecución, donde actúa el centinela de riesgo (tope por orden y demás validaciones).

## Tope por orden

Si hay un tope configurado, toda orden que aumenta riesgo se evalúa contra él usando su **nocional en USD**, **incluidas las órdenes a mercado**. El tope efectivo es el menor entre el tope absoluto en USD y el relativo al capital, ignorando el que valga cero; si ambos valen cero no hay tope.

- El nocional se calcula según las unidades reales: en spot, cantidad por precio; en futuros, según el tamaño del contrato y su multiplicador. Una orden sin precio usa un precio real reciente (antigüedad máxima `risk.reference_price_max_age`, 10 s por defecto).
- **Falla cerrado**: si hay tope y no se puede determinar el nocional de una orden que aumenta riesgo, se rechaza con `ORDER_NOTIONAL_UNKNOWN`. Si lo supera, `ORDER_CAP_EXCEEDED`, con el nocional y el tope.
- La aplicación a órdenes sin precio se controla con `risk.enforce_cap_on_market_orders` (recargable en caliente). En el producto viene en `true`; en despliegues existentes viene en `false` hasta que el operador lo active.

- **Las marcas `reduce_only`/`close_order` solo cuentan en futuros.** La ruta spot no envía esas marcas al exchange, así que una compra spot marcada `reduce_only` se evalúa como una orden que aumenta riesgo (con tope). En spot solo una venta de un par spot cuenta como reducción.
- **El tope no se puede desactivar en el producto**: `risk.max_order_size_usd` debe ser mayor que 0. Un valor 0 o negativo se rechaza en toda vía de escritura (parámetro suelto, lote, preajustes y reversiones).

## Quién puede cambiar los límites

Las claves sensibles (`risk.*`, `bot.max_daily_loss_percent` y `trading.live_enabled`) exigen nivel de operador (4) en todas las vías de escritura de la configuración: `PUT/DELETE /params/:key`, `POST /params/bulk`, crear, editar y aplicar preajustes, y revertir un cambio o un preajuste. Un token de nivel inferior recibe 403 y no se escribe nada.

## Parada de emergencia

La parada global rechaza exactamente las llamadas de clase `trade_increase` (la clase viene de la misma clasificación que los permisos). Con la parada activa siguen disponibles `read`, `trade_reduce` y `safety`. Activarla (`activate_kill_switch`, clase `safety`) está permitido a perfiles `paper` y `live`; levantarla (`deactivate_kill_switch`) es `control`, solo `live`.

## Cerrar nunca se bloquea

- Una orden que solo reduce o cierra exposición **no se rechaza por el tope** ni por no poder determinar su nocional.
- Las clases `trade_reduce` y `safety` no dependen de la licencia ni de la parada de emergencia.
- Una **licencia caducada nunca impide reducir o cerrar posiciones**: las llamadas `read`, `trade_reduce`, `safety` y todo el perfil `paper` siguen funcionando. Solo se pierde la capacidad de abrir con dinero real.

## Dinero real con tres llaves

Una orden real que aumenta riesgo exige a la vez: token `live`, licencia `valid` o `grace` y `trading.live_enabled: true`. En la configuración del producto `live_enabled` es `false`.

La licencia y `trading.live_enabled` se comprueban dos veces: en el despachador de `/mcp` y en el cliente del exchange, por donde pasa toda orden real (también `POST /api/v1/orders` y `/api/v1/orders/direct/futures`). Así ninguna ruta REST abre exposición real sin licencia. Reducir y cerrar posiciones de futuros nunca se bloquea.

## Sesiones y tokens

- Un token de agente solo sirve en los endpoints MCP (`/mcp`, `/jsonrpc`, `/`) y en el WebSocket de solo lectura; en la API REST recibe 403 `OPERATOR_REQUIRED`.
- Inicio de sesión: 5 fallos en 15 minutos desde una misma dirección bloquean esa dirección 15 minutos. Los fallos desde muchas direcciones a la vez no bloquean al operador: solo hacen que cada intento espere un segundo.

## Otras defensas por defecto

Sin endpoints de depuración; superficie sin autenticar mínima (`/health` solo da estado, versión, commit y edición); la identidad se toma solo del token (las cabeceras `X-Tenant-ID` y `X-Company-ID` del cliente no la pisan); escucha local por defecto; orígenes permitidos explícitos.
