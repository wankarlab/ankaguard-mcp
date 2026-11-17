# Rutas de órdenes hacia el exchange

Inventario (tarea 1.2 de `guarded-mcp-product`) de todo el código que **envía, modifica, cancela o cierra** una orden, o cambia apalancamiento o modo de margen, con archivo y línea. Estado del código: rama `work-local`, commit base `72770dd9`. Las líneas son de la fecha del inventario; si el código cambia hay que revisarlas.

Se usa para dos cosas: (a) el selector de papel del grupo 5 tiene que cubrir **todas** las rutas de la sección 4 que puede alcanzar un token MCP, y (b) saber qué rutas pasan por el punto único de riesgo `evaluateOrderRisk` y cuáles no.

Método: se buscaron todas las llamadas a `SubmitOrder`, `SubmitFuturesOrder`, `SubmitSpotOrder`, `PlaceOrder`, `PlaceFuturesOrder`, `CancelOrder`, `CancelFuturesOrder`, `CancelAllFuturesOrders`, `CloseFuturesPosition`, `SetLeverage`, `SetMarginMode`, `ModifyOrder` y los constructores `SubmitOrderRequest{`, `FuturesOrderRequest{`, `SpotOrderRequest{` en `internal/`, `cmd/` y `pkg/` (excluidas pruebas), y se siguió cada cadena hasta la llamada HTTP/gRPC.

## 1. Capa hoja: donde la orden sale hacia el exchange

### 1.1 gRPC a GoCryptoTrader (`internal/gct/client.go`)

| Operación | Archivo:línea | Notas |
|---|---|---|
| `SubmitOrder` (spot) | `internal/gct/client.go:975` (`rpcClient.SubmitOrder`, función en `:875`) | Tras `evaluateOrderRisk` (`:939`). El ramal `MarketType == margin` (`:885-893`) va a `MarginManager.PlaceMarginOrder` **antes** del chequeo de riesgo, pero hoy devuelve `ErrMarginNotImplemented`. |
| `CancelOrder` | `:1052` (`rpcClient.CancelOrder`, función `:1045`) | Sin chequeo de riesgo (reduce exposición). |
| `CancelFuturesOrder` | `:1070` → `kucoinClient.CancelFuturesOrder` (`:1072`) | Cae a `CancelOrder` si no es futuros (`:1074`). |
| `PlaceOrder` | `:2054` | Alias: llama a `SubmitOrder` (`:2055`). |
| `SubmitFuturesOrder` | `:2122` | `evaluateOrderRisk` en `:2140`; delega en `FuturesManager.PlaceFuturesOrder`. |
| `CloseFuturesPosition` | `:2209` → `futuresManager.ClosePosition` (`:2225`) | Sin chequeo de riesgo. |
| `SetLeverage` | `:2424` → `kucoinClient.SetLeverage` (`:2427`) | Solo KuCoin. |
| `evaluateOrderRisk` | `:256` | Punto único de riesgo: solo lo consultan `SubmitOrder` spot (`:939`) y `SubmitFuturesOrder` (`:2140`). |

### 1.2 Cliente REST directo de KuCoin (`internal/gct/kucoin_rest_client.go`)

| Operación | Archivo:línea (función → petición) | Endpoint |
|---|---|---|
| `SubmitFuturesOrder` | `:1080` → `POST` en `:1197` | `/api/v1/orders` (futuros, incluye órdenes `stop`) |
| `SubmitSpotOrder` | `:1285` → `POST` en `:1324` | `/api/v1/orders` (spot) |
| `CancelFuturesOrder` | `:1971` → `DELETE` en `:1987` | `/api/v1/orders/{id}` |
| `CancelAllFuturesOrders` | `internal/gct/kucoin_cancel_all.go:41`, `deleteAndCount` `:69` | `DELETE /api/v1/orders` y `/api/v1/stopOrders` |
| `SetLeverage` | `:806` → `POST` en `:826` | `/api/v1/position/risk-limit-level/change` |
| `SetMarginMode` | `:889` → `POST` en `:908` | modo de margen de futuros |
| `ClosePosition` | no existe como llamada propia: `FuturesManager.ClosePosition` (`internal/gct/futures_manager.go:327`) envía una orden de mercado reduce-only con `SubmitFuturesOrder` (`futures_manager.go:383`) |

`FuturesManager.PlaceFuturesOrder` (`futures_manager.go:95`) llama a `kucoinClient.SubmitFuturesOrder` en `:265`, y hace también `SetMarginMode`/`SetLeverage` vía `LeverageController` (`internal/gct/leverage_controller.go:113`, `:237`). `MarginManager.PlaceMarginOrder` (`internal/gct/margin_manager.go:161`) existe pero no envía nada todavía.

### 1.3 Sistema HFT (`internal/hft`, cableado en `gctclient`)

No es un exchange propio: es una capa de cola, reintentos y circuit breaker **delante** de la capa 1.1.

| Pieza | Archivo:línea |
|---|---|
| Creación del sistema HFT | `internal/mcp-trading/gctclient/client.go:79` (`hft.NewHFTTradingSystem`) |
| Desvío de `SubmitOrder` por HFT cuando está en marcha | `internal/mcp-trading/gctclient/client.go:420-424` (si no, `gctClient.SubmitOrder` directo) |
| Desvío de `SubmitFuturesOrder` por HFT | `internal/mcp-trading/gctclient/client.go:468-471` |
| Adaptador a `gct.GCTClient` | `internal/mcp-trading/gctclient/hft_adapter.go:20` (`SubmitOrder`), `:29` (`SubmitFuturesOrder`) |
| Envío con reintentos | `internal/hft/gct_wrapper.go:210` (`SubmitOrder`, llamadas en `:240`, `:244`), `:294` (`SubmitFuturesOrder`, llamadas en `:322`, `:326`) |
| Ejecutor optimizado | `internal/hft/executor.go:197` (`e.gctClient.SubmitOrder`) |

El camino HFT termina en `gct.client.SubmitOrder/SubmitFuturesOrder`, así que sí pasa por `evaluateOrderRisk`. Riesgo a vigilar en el grupo 5: el selector de papel debe ir por debajo o por encima del HFT, no al lado, o el HFT reenviaría una orden de papel al cliente real (los reintentos están activos por defecto, `DefaultHFTConfig`).

### 1.4 Envoltorios de `gctclient.Client` (`internal/mcp-trading/gctclient/client.go`)

| Método | Línea | Va a |
|---|---|---|
| `SubmitOrder` | `:393` | HFT (`:421`) o `gctClient.SubmitOrder` (`:424`) |
| `SubmitFuturesOrder` | `:458` | HFT (`:468`) o `gctClient.SubmitFuturesOrder` (`:471`) |
| `CancelOrder` | `:488` | `gctClient.CancelOrder` (`:494`) |
| `CancelFuturesOrder` | `:510` | `CancelFuturesOrder` (`:515`) o `CancelOrder` (`:517`) |
| `SetLeverage` | `:967` | `gctClient.SetLeverage` (`:972`) |
| `CloseFuturesPosition` | `:976` | `gctClient.CloseFuturesPosition` (`:981`) |
| `GetBaseClient` | `:177` | Devuelve el `gct.GCTClient` sin envolver: lo usan las tools "direct" (sección 2). |

### 1.5 Variantes por usuario (sesiones aisladas, multiusuario)

Van **directamente** a `FuturesManager`/`KuCoinRESTClient` de la sesión y **no pasan por `evaluateOrderRisk`**:

| Método | Archivo:línea | Llamada de hoja |
|---|---|---|
| `SubmitOrderForUser` | `gctclient/client.go:1237` | `SubmitSpotOrder` (`:1262`) o `FuturesManager().PlaceFuturesOrder` (`:1296`) |
| `SubmitFuturesOrderForUser` | `gctclient/client.go:1316` | `FuturesManager().PlaceFuturesOrder` (`:1324`) |
| `SetMarginModeForUser` | `gctclient/client.go:1380` | `KuCoinClient().SetMarginMode` (`:1385`) |
| `SetLeverageForUser` | `gctclient/client.go:1395` | `KuCoinClient().SetLeverage` (`:1400`) |
| `CancelOrderForUser` | `gctclient/client.go:1411` | `gctClient.CancelOrder` (`:1426`) |
| `CancelFuturesOrderForUser` | `gctclient/client.go:1437` | → `CancelOrderForUser` (`:1438`) |
| `CloseFuturesPositionForUser` | `gctclient/client.go:1442` | `FuturesManager().ClosePosition` (`:1447`) |
| `UserClient.SubmitOrderForUser` / `SubmitFuturesOrderForUser` / `CancelOrderForUser` / `SetLeverage` / `CloseFuturesPosition` | `gctclient/user_client.go:368`, `:384`, `:480` (y los dos últimos justo antes de `:559` y `:570`) | Delegan en `Client.*` (no `ForUser`), con credenciales del usuario ya configuradas: llamadas en `:373`, `:389`, `:485`, `:559`, `:570`; esas sí pasan por 1.4 y por `evaluateOrderRisk` |
| `AuthenticatedGCTClient.*WithCredentials` | `internal/credentials/authenticated_client.go:219`, `:235`, `:315`, `:347`, `:363` | `baseClient.SubmitOrder/SubmitFuturesOrder/CancelOrder/CloseFuturesPosition/SetLeverage` (`:224`, `:240`, `:320`, `:352`, `:368`) |

Estas rutas solo están activas con `multi_user.enabled=true` (hoy `false`).

## 2. Tools MCP que llegan al exchange

| Tool | Clase | Archivo:línea | Ruta |
|---|---|---|---|
| `submit_order` | trade_increase | `tools/orders/submit_orders.go:166` | `client.SubmitOrder` → 1.4 |
| `submit_market_order` | trade_increase | `submit_orders.go:245` | ídem |
| `submit_limit_order` | trade_increase | `submit_orders.go:328` | ídem |
| `submit_stop_order` | trade_increase | `submit_orders.go:418` | ídem |
| `modify_order` | trade_increase | `tools/orders/manage_orders.go:224` (cancela), `:256` (reenvía) | `CancelOrder` + `SubmitOrder`: no es atómico |
| `cancel_order` | trade_reduce | `manage_orders.go:52` | `client.CancelOrder` |
| `cancel_all_orders` | trade_reduce | `manage_orders.go:115` | lista órdenes abiertas y cancela una a una |
| `submit_futures_order_direct` | trade_increase / trade_reduce según argumentos | `tools/orders/direct_orders.go:212` | `GetBaseClient().SubmitFuturesOrder` (**salta HFT y el envoltorio**, pero pasa por `evaluateOrderRisk`) |
| `close_position_direct` | trade_reduce | `direct_orders.go:99` | `GetBaseClient().CloseFuturesPosition` |
| `create_trailing_stop` | trade_reduce | `tools/orders/trailing_stop_tools.go:178` | `GetBaseClient().SubmitFuturesOrder` con `ReduceOnly: true` |
| `close_futures_position` | trade_reduce | `tools/scalping/close_futures_position.go:95` | `adapter.GetGCTClient().CloseFuturesPosition` |
| `execute_scalping_trade` | trade_increase | `tools/scalping/execute_trade.go:221` | `adapter.GetExecutor().ExecuteOrderWithLeverage` → 3.1 |
| `start_scalping_bot` | trade_increase | `tools/scalping/start_bot.go:126` | `BotManager.StartBot` → 3.2 (el bot envía órdenes por su cuenta) |
| `stop_scalping_bot` | trade_reduce | `tools/scalping/stop_bot.go` | `BotManager.StopBot(…, closePositions)` → 3.2 |
| `update_sltp` | control | `tools/risk/update_sltp_tool.go` | Solo edita el seguimiento local (`internal/trading/sltp_executor.go:863`); no toca órdenes del exchange |
| `activate_kill_switch` / `deactivate_kill_switch` | safety / control | `tools/risk/*kill_switch_tool.go` | Estado del centinela; no envía órdenes |
| `set_trading_mode`, `set_pair_selection` | control | `tools/mode`, `tools/pairs` | Configuración persistida; no envía órdenes |

`modify_order` y las 4 tools `submit_*` pasan por `gctclient.Client` (con HFT). Las tools "direct" y el trailing stop usan `GetBaseClient()`: el selector de papel del grupo 5 debe interceptar también `GetBaseClient()` o esas tres rutas seguirían yendo al exchange real con un token `paper`.

Las tools `modify_trailing_stop` (siempre devuelve "No implementado") y `create_stop_loss_template` (no persiste) no tocan el exchange.

## 3. Motores internos (no son tools, pero operan con dinero real)

### 3.1 Scalping (`internal/scalping`)

| Ruta | Archivo:línea |
|---|---|
| Orden de entrada con apalancamiento (futuros) | `internal/scalping/executor/executor.go:433` (`SubmitFuturesOrder`) |
| Entrada spot / reintento spot | `executor.go:464`, `:489` (`SubmitOrder`) |
| Stop-loss y take-profit de futuros | `executor.go:739` (SL), `:799` (TP) |
| Cierre de posición tras fallo | `executor.go:276` (`CloseFuturesPosition`) |
| Cancelar orden | `executor.go:677` (`CancelOrder`) |
| Motor de scalping (orden de mercado) | `internal/scalping/engine/engine.go:445` (`SubmitOrder`) |
| Gestor de bots: cierre por par (futuros y respaldo spot) | `internal/scalping/manager/bot_manager.go:390` (`CloseFuturesPosition`), `:398` (`SubmitOrder` de venta spot) |
| Gestor de bots: cierres en parada y por riesgo | `bot_manager.go:1072`, `:1398` |

### 3.2 Bot de IA (`internal/services/ai_trading_bot.go`)

| Ruta | Línea |
|---|---|
| Orden de entrada | `:4211` (`SubmitOrder`, petición construida en `:4184`) |
| Orden de cierre | `:5271` (petición en `:5259`) |
| Stop de protección en el exchange | `:5352` (petición en `:5322`) |
| Cancelar stop del exchange | `:5409` (`CancelFuturesOrder`), `:5411` (`CancelOrder`) |
| Cierre de posición | `:2514`, `:3970`, `:4023`, `:7588` (`CloseFuturesPosition`); `:2495` vía `session.FuturesManager().ClosePosition` |
| Entradas manuales por REST | `internal/handlers/ai_bot_handler.go` (`/api/v1/ai-bot/manual-entry`, `manual-close`, `close-all-positions` → `CloseAllPositionsHTTP` `:604`), todas con `requireMinLevel(4)` salvo `close-all-positions` (solo JWT) |

### 3.3 Cesta XSEC (`cmd/xsec-live`, proceso aparte)

Lo lanza como subproceso `internal/api/v1/handlers/xsec_job_runner.go`. Crea su propio cliente GCT y **no cablea `RiskSentinel` ni consulta el kill switch** dentro del subproceso (no hay `SetRiskSentinel` en `cmd/xsec-live`); la parada global actúa matando el subproceso.

| Ruta | Archivo:línea |
|---|---|
| `SubmitFuturesOrder` | `cmd/xsec-live/main.go:176`, `:627`, `:1440`, `:1473` |
| `CloseFuturesPosition` | `:216`, `:314`, `:347`, `:410`, `:1427` |
| `CancelFuturesOrder` | `:295`, `:1502` |

### 3.4 Otros binarios

| Ruta | Archivo:línea |
|---|---|
| `cmd/ai-trading-bot` (bot independiente, cablea `RiskSentinel` en `:187-199`) | `cmd/ai-trading-bot/main.go:557`, `:746` (`SubmitFuturesOrder`) |

### 3.5 API REST (puerto 8081 / gateway)

| Endpoint | Archivo:línea | Llamada |
|---|---|---|
| `POST /orders` (spot) | `internal/api/v1/handlers/orders.go:96` (ruta en `router.go:756`, con `Ring0Guard`) | `gctClient.SubmitOrder` |
| `DELETE /orders/{id}` | `orders.go:256` | `gctClient.CancelOrder` |
| `POST /orders/direct/futures` | `internal/api/v1/handlers/direct_orders_handler.go:236` (ruta en `internal/api/v1/router.go:766`) | `base.SubmitFuturesOrder`; comprueba la parada global dentro del manejador (`:185`), no con `Ring0Guard` |
| `POST /orders/direct/close` | `direct_orders_handler.go:129` | `base.CloseFuturesPosition` |
| Parada global (anillo 0) | `internal/api/v1/handlers/killswitch_ring0.go:288` (`CancelAllOrders` → `kucoin_cancel_all.go:41`), `:324` (`ClosePosition`), `:474` (`CloseFuturesPosition`), `:482` | Cancela y cierra todo |

### 3.6 Otros que NO envían a un exchange (descartados)

- `internal/trading/position_manager.go:279`, `:361`: comentarios "In production, this would call gctClient.SubmitOrder"; solo contabilidad local. `internal/trading/sltp_executor.go:661`, `:716` cierran en el `positionManager` local, no en el exchange.
- `internal/launch` (AnkaLaunch): `Venue.Submit` (`internal/launch/venue.go:21`) es una interfaz sin implementación real en este código; la fase 1 es sombra (sin firmar ni enviar).
- `internal/exchange` (`VenueExecutor.Place/Cancel`, `internal/exchange/execution.go:42-58`; `BinanceVenueAdapter`, `binance.go:39`, `:56`): deshabilitado por defecto (`execEnabled=false`) y nadie lo habilita en `cmd/trading-mcp-server`; si se habilitara, delega en el `OrderSubmitter` (cliente GCT).
- `internal/arbitrage/*`: no contiene llamadas de envío (`Positions()` solo lee).
- `internal/hft/order_reconciliation.go`: solo consulta el estado de órdenes.
- `gocryptotrader/` (copia local): el código de los exchanges se alcanza únicamente a través del servidor gRPC de GCT (sección 1.1); `internal/mcp-trading/gctclient/funding.go:28` usa `GetGRPCConn()` solo para consultas de funding.

## 4. Consecuencias para el cambio

1. **Selector de papel (grupo 5).** Las rutas que un token MCP puede alcanzar son: las 4 tools `submit_*` + `modify_order` + `cancel_*` (vía `gctclient.Client`), las 3 "direct" y el trailing stop (vía `GetBaseClient()`), y las 5 tools de scalping (vía `adapter.GetGCTClient()`, `adapter.GetExecutor()` y `BotManager`, que construyen sus propios clientes). Un selector que solo envuelva `gctclient.Client` deja pasar las "direct" y todo el scalping. Mientras no exista, el servidor rechaza con `PAPER_UNAVAILABLE` (hook `paperBrokerReady` en `internal/mcp-trading/server/permission.go`).
2. **Rutas sin `evaluateOrderRisk`**: variantes `ForUser` (1.5), `cmd/xsec-live` (3.3), margen (1.1) y todas las de reducción (`Cancel*`, `Close*`, que es intencional). Relevante para el grupo 4 (tope por orden).
3. **Bots internos.** El bot de IA, el scalping y XSEC envían órdenes sin pasar por `/mcp`, así que los perfiles de token no los gobiernan; los controla la parada global y sus propios topes.

## 5. Estado tras el cableado del papel (grupo 5)

`gctclient.Client.ClientFor(ctx)` elige entre el cliente real y el broker de papel según el perfil del contexto. Con perfil `paper` pasan por el broker: los envoltorios de 1.4, `GetBaseClient()` (las tools "direct" y el trailing stop usan `ClientFor(ctx)`), `get_futures_positions`, `close_futures_position` y las variantes `ForUser` (que responden `PAPER_UNSUPPORTED`). `execute_scalping_trade` y `start_scalping_bot` responden `PAPER_UNSUPPORTED`. Sin broker el servidor falla cerrado (`PAPER_UNAVAILABLE`). La prueba `toolinventory/paper_isolation_test.go` lo verifica para todas las tools `trade_increase` y `trade_reduce` del registro real. El bot de IA, el scalping interno y XSEC siguen usando el cliente real (no pasan por `/mcp`).
