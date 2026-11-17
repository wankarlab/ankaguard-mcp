# Operar en papel (simulador)

El perfil `paper` permite practicar con las mismas tools de órdenes, posiciones y saldo sin tocar dinero real. Las órdenes se ejecutan en un broker simulado, persistente en SQLite, que usa **precios reales** de mercado. Código: `internal/paper`.

## Qué es real y qué no

| Aspecto | Comportamiento |
|---|---|
| Precios | Último precio real del exchange, con antigüedad máxima de 10 s. Sin precio reciente la orden se rechaza con `DATA_UNAVAILABLE`. |
| Datos de mercado (tickers, velas, libro, indicadores) | Se piden al exchange real; son de solo lectura. |
| Órdenes, cancelaciones, cierres, apalancamiento, saldo, posiciones, historial | Se sirven desde SQLite. Nunca llegan al exchange. Un fallo del simulador devuelve un error y la orden no se reenvía. |
| Cuenta | Una por titular (sujeto del token). Saldo inicial `paper.initial_balance_usd` (1000 USD por defecto). Dos titulares no ven ni modifican nada del otro. Sin titular, la llamada falla: no existe cuenta compartida. |
| Protecciones | Las órdenes pasan por el mismo centinela de riesgo que las reales (parada de emergencia, tope por orden, validaciones), con el mismo cálculo de nocional. |

## Cómo se ejecuta una orden

- **Mercado**: al último precio real más (compra) o menos (venta) `paper.slippage_pct` (0,05 % por defecto), más la comisión `paper.taker_fee_pct` (0,06 % por defecto) sobre el nocional.
- **Límite**: queda pendiente hasta que el precio real la cruza (compra: precio menor o igual al límite; venta: mayor o igual) y se ejecuta entonces **a su precio límite**. Una límite que ya cruza al enviarla se ejecuta de inmediato a su límite. Reserva el saldo mientras está pendiente.
- **Stop**: se activa cuando el precio real alcanza el disparador (la dirección se fija al enviarla: disparador por encima del precio actual se activa al subir, por debajo al bajar) y se ejecuta como mercado, con deslizamiento.
- Un proceso periódico (`Broker.Run`, cada 5 s por defecto) revisa las órdenes pendientes y las posiciones apalancadas. Todo el estado está en SQLite: tras reiniciar el servidor se conservan saldo, posiciones y órdenes pendientes.

## Unidades

Igual que en real:

- **Spot**: `amount` son unidades del activo base. Solo pares cotizados en USDT, USDC o USD. Una venta exige tener esas unidades (no hay venta en corto en spot).
- **Futuros**: `amount` es un objetivo en USD que se convierte a contratos enteros con el multiplicador del contrato (`max(1, round(USD / (precio × multiplicador)))`). El nocional real puede superar el objetivo si un contrato vale más. Solo contratos lineales USDT (`...USDTM`).

## Futuros y liquidación

Margen aislado: `margen = nocional / apalancamiento`. Cuando la pérdida no realizada iguala el margen, la posición se cierra de forma forzosa: se pierde el margen, no se devuelve nada, se cancelan las órdenes pendientes del contrato y el historial marca la ejecución como `liquidation`. Un cierre normal nunca pierde más que el margen.

## Límites declarados (no son errores)

- **Sin funding**: no se cobra ni se paga.
- **Sin ejecuciones parciales**: una orden se ejecuta entera o no se ejecuta.
- **Sin profundidad de libro**: el deslizamiento es fijo, no depende del tamaño. Una orden grande se ejecuta igual que una pequeña.
- **Sin comisión de maker**: toda ejecución paga la comisión de taker, también las límite.
- **Precio de referencia = último precio**, no bid/ask ni precio de marca; la liquidación real del exchange usa otras reglas (margen de mantenimiento, precio de marca).
- **Una posición neta por contrato**: una orden contraria mayor que la posición se rechaza (`PAPER_UNSUPPORTED`); hay que cerrar y abrir.
- **Margen aislado** únicamente; no hay margen cruzado.
- **Tipos de orden no soportados** se rechazan con `PAPER_UNSUPPORTED` y no se envían al exchange: stop-limit, trailing, IOC/FOK, margin y opciones.
- El simulador puede dar una confianza que el mercado no confirma: no garantiza resultados reales.

## Cómo se marca la respuesta como papel

Todo `Order`, `Position`, `Portfolio`, `PortfolioOverview` y `AccountInfo` del broker lleva `paper: true`. Las órdenes llevan además `paper_assumptions` (deslizamiento, comisión, comisión cobrada, `funding_modeled: false`, `partial_fills: false`). Los resultados en mapa (`GetBalance`, `GetPositions`, `SimulateOrder`) incluyen `"paper": true` y `"assumptions"`. `Client.Assumptions()` devuelve el mismo mapa para las tools que lo necesiten por separado.

## Parámetros (configuración dinámica)

| Clave | Por defecto | Efecto |
|---|---|---|
| `paper.initial_balance_usd` | 1000 | Saldo de cuentas nuevas o reiniciadas |
| `paper.slippage_pct` | 0,05 | Deslizamiento adverso en mercado y stop |
| `paper.taker_fee_pct` | 0,06 | Comisión por ejecución |

## Códigos de error

`PAPER_UNSUPPORTED`, `DATA_UNAVAILABLE`, `INSUFFICIENT_FUNDS` (indica disponible y requerido), `PAPER_NO_HOLDER`, `PAPER_ORDER_REJECTED`, `PAPER_NOT_FOUND`.

## Reinicio de la cuenta

`paper.Reset` (o `Broker.ResetAccount`) cierra las posiciones, cancela las órdenes pendientes, deja el saldo inicial y conserva el historial anterior marcado `closed_by_reset`. Solo lo invoca el operador; el servidor debe rechazar la petición de un token de agente antes de llamarlo.

## Cableado en el servidor

- `cmd/trading-mcp-server/product_wiring.go` construye el broker con la base SQLite existente, el cliente GCT real como fuente de precios, el centinela de riesgo real y el multiplicador de contrato de KuCoin; arranca `Broker.Run`. Con el broker construido, `paperBrokerReady()` pasa a verdadero y el perfil `paper` deja de responder `PAPER_UNAVAILABLE`.
- Selector por llamada: `gctclient.Client.ClientFor(ctx)` devuelve el broker cuando el contexto trae el perfil `paper` (titular = `sub` del token) y el cliente real en otro caso. Sin broker, falla cerrado (`PAPER_UNAVAILABLE`), nunca cae al cliente real. Lo usan `GetBaseClient()`, las tools directas, el trailing stop, scalping y las variantes `*ForUser` (que con papel devuelven `PAPER_UNSUPPORTED`).
- `execute_scalping_trade` y `start_scalping_bot` responden `PAPER_UNSUPPORTED` con perfil `paper`.
- Prueba de aislamiento: `internal/mcp-trading/toolinventory/paper_isolation_test.go` recorre todas las tools `trade_increase` y `trade_reduce` del registro real contra un GCT falso que hace `t.Fatal` ante cualquier llamada de órdenes o cuenta, más un cliente REST de KuCoin falso.
- Reinicio: `POST /api/v1/paper/accounts/:holder/reset`, solo sesión de operador.
