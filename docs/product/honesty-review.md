# Revisión de honestidad de las tools MCP

Resultado de la tarea 3.7 del cambio `guarded-mcp-product`. Criterio (D10): una tool devuelve datos reales, una estimación declarada (`estimate: true` con `assumptions`) o el error `DATA_UNAVAILABLE`. Nunca valores fijos, aleatorios o de relleno presentados como datos.

## Error común `DATA_UNAVAILABLE`

`common.DataUnavailable(reason, details)` (`internal/mcp-trading/tools/common/data_unavailable.go`) devuelve un `*models.MCPError` con código JSON-RPC `-32005`, mensaje `DATA_UNAVAILABLE` y `error.data = {code: "DATA_UNAVAILABLE", reason, ...detalles}` (por ejemplo `candles_missing` o `daily_returns_missing`). Se devuelve sin envolver para que el servidor lo entregue tal cual al cliente.

## Tools retiradas (14)

| Tool | Motivo |
|---|---|
| `get_open_interest`, `get_liquidations` | Sin fuente real conectada. |
| `get_trailing_stop_status`, `create_stop_loss_template`, `get_stop_loss_templates`, `modify_trailing_stop` | Estado y plantillas simulados. Se conserva `create_trailing_stop` (reduce-only, orden real). |
| `optimize_parameters`, `compare_strategies`, `get_backtest_metrics`, `walk_forward_analysis` | Backtest que simulaba datos. |
| `get_pairs_by_type` | Lista de pares fija. |
| `detect_patterns`, `detect_support_resistance`, `generate_signals` | Stubs sin implementación. |

Una prueba (`TestRetiredToolsAreNotRegistered`) comprueba que responden "Tool not found".

## Tools rehechas

| Tool | Cambio | Origen |
|---|---|---|
| `backtest_strategy` | Único backtest, sobre velas reales; declara periodo, fuente, comisión, deslizamiento y `funding_modeled: false`; `estimate: true`. | estimación |
| `get_advanced_portfolio_metrics`, `get_portfolio_risk_metrics` | Sobre `portfolio_snapshots` reales (equity diario). Sin historial suficiente: `DATA_UNAVAILABLE` con `daily_returns_missing`. | registro local / cálculo |
| `calculate_funding_costs` | Tasa e historial de funding reales; sin comparación entre exchanges. | estimación |
| `llm_analyze_market` | Sin análisis simulado: sin proveedor o con fallo devuelve `DATA_UNAVAILABLE`. | estimación |
| `simulate_order` | `estimate: true` y `assumptions` (precio, deslizamiento, comisiones). | estimación |
| `calculate_liquidation_price` | `estimate: true` y supuesto de margen de mantenimiento. | estimación |
| `get_rebalance_recommendations` | Quitada la estrategia "momentum" simulada; `estimate: true`. | estimación |
| Indicadores de `technical/register.go` (ATR, MFI, estocástico, ADX, CCI, Williams %R, OBV, VWAP, Ichimoku, volatilidad) | Cálculo real sobre velas; antes eran placeholders. | cálculo |
| `analyze_scalping_opportunity`, `get_scalping_signals` | ATR y precios de velas reales en lugar de estimaciones por spread; sin velas, `DATA_UNAVAILABLE`. | exchange |
| `check_risk_limits`, `calculate_stop_loss` | Sin datos, los controles fallan cerrados (`passed: false`, `data_available: false`); sin capital supuesto de 10000. `calculate_stop_loss` solo admite `percentage`. | registro local / cálculo |
| `get_markets`, `list_tradable_pairs` | Sin lista estática de respaldo: ante fallo de GCT, `DATA_UNAVAILABLE`. | exchange |
| `get_positions` | Un `market_type` no soportado devuelve error en vez de lista vacía. | exchange |
| `market_features` | Quitado el apalancamiento mínimo y máximo fijos. | exchange |

## Marcadores restantes y justificación

Los marcadores `TODO`, `mock`, `placeholder`, `simulated`, `hardcoded`, `fake` y `dummy` del área se revisaron uno a uno. Los que ya no existen desaparecieron con el código retirado o rehecho. Los que se conservan:

| Archivo | Marcador | Resolución |
|---|---|---|
| `tools/risk/validate_trade.go` | `TODO(TC-3.4)`: migrar límites a configuración dinámica | Justificado: son límites de seguridad genéricos y conservadores (tamaño por moneda); no devuelven datos. Pendiente de configuración dinámica. |
| `tools/risk/risk_tools.go` (`check_risk_limits`) | Apalancamiento máximo 1/5/20 | Justificado: es política, no dato de mercado; documentado en la descripción. |
| `tools/config/handlers.go` | `TODO` de cableado | Justificado: el paquete no se registra en `main.go`; no expone tools. |
| `tools/scalping/register.go`, `adapter.go` | Comentarios sobre valores por defecto de riesgo | Justificado: son parámetros de configuración, no datos. |
| `tools/marketdata` (`get_market_types`, `market_features`) | Constantes descriptivas | Justificado: describen tipos de mercado, no valores de mercado. |
| `tools/orders/simulate_order.go` | "simulate" | Justificado: es el propósito de la tool y se declara como estimación. |
| Comentarios en español con "todos" | Falsos positivos de la búsqueda | No aplica. |

## Fuera de alcance, anotado

`internal/handlers/backtesting_handler.go` aún llama a `get_backtest_metrics` y `optimize_parameters` (tolera el fallo con aviso), envía `start_date` y `end_date` que ahora se ignoran y contiene un texto fijo "4h 23m". Está fuera del área de tools; queda para la tarea 6.8 (rutas de backtesting).
