# Clasificación de tools MCP

Generado desde `internal/mcp-trading/toolpolicy` (fuente de verdad) con `UPDATE_TOOL_INVENTORY=1 go test ./internal/mcp-trading/toolinventory/`. No editar a mano: para cambiar una clase se edita la tabla en `toolpolicy/classify.go`.

**Total: 105 tools.**

| Clase | Tools |
|---|---|
| `read` | 79 |
| `trade_increase` | 8 |
| `trade_reduce` | 6 |
| `control` | 11 |
| `safety` | 1 |

Criterio: ante la duda, `control`. Una tool sin clase no se lista ni se ejecuta.

## Matriz de permisos

| Clase | `read` | `paper` | `live` |
|---|---|---|---|
| `read` | permitido | permitido | permitido |
| `trade_increase` | denegado | broker de papel | exchange real |
| `trade_reduce` | denegado | broker de papel | exchange real |
| `control` | denegado | denegado | permitido |
| `safety` | denegado | permitido | permitido |

Mientras no exista el broker de papel (grupo 5), las llamadas `trade_*` con perfil `paper` se rechazan con `PAPER_UNAVAILABLE` antes de ejecutar la tool.

Clase dependiente de argumentos: `submit_futures_order_direct` es `trade_reduce` si `reduce_only` o `close_order` es el booleano `true`; en cualquier otro caso (ausente, falso, texto o número) es `trade_increase`. La parada global rechaza exactamente las llamadas `trade_increase`.

## Tabla completa

| Tool | Clase | Nota |
|---|---|---|
| `activate_kill_switch` | `safety` | Activates the emergency stop. |
| `analyze_scalping_opportunity` | `read` |  |
| `backtest_strategy` | `read` | Replays a strategy over real candles in memory; no exchange, account or config effect. |
| `batch_convert_trading_pairs` | `read` |  |
| `calculate_adx` | `read` |  |
| `calculate_atr` | `read` |  |
| `calculate_bollinger` | `read` |  |
| `calculate_cci` | `read` |  |
| `calculate_drawdown` | `read` |  |
| `calculate_ema` | `read` |  |
| `calculate_fibonacci` | `read` |  |
| `calculate_funding_costs` | `read` |  |
| `calculate_ichimoku` | `read` |  |
| `calculate_liquidation_price` | `read` |  |
| `calculate_macd` | `read` |  |
| `calculate_macd_futures` | `read` |  |
| `calculate_mfi` | `read` |  |
| `calculate_obv` | `read` |  |
| `calculate_position_size` | `read` |  |
| `calculate_rsi` | `read` |  |
| `calculate_rsi_futures` | `read` |  |
| `calculate_sma` | `read` |  |
| `calculate_stochastic` | `read` |  |
| `calculate_stop_loss` | `read` |  |
| `calculate_take_profit` | `read` |  |
| `calculate_volatility` | `read` |  |
| `calculate_vwap` | `read` |  |
| `calculate_williams_r` | `read` |  |
| `cancel_all_orders` | `trade_reduce` | Cancels open orders. |
| `cancel_order` | `trade_reduce` | Cancels an open order. |
| `check_risk_limits` | `read` |  |
| `close_futures_position` | `trade_reduce` | Reduce-only market order that closes a position. |
| `close_position_direct` | `trade_reduce` | Closes a futures position through the direct KuCoin REST client. |
| `configure_notifications` | `control` | Changes push notification settings. |
| `convert_trading_pair` | `read` |  |
| `create_funding_rate_alert` | `control` | Creates an alert (configuration). |
| `create_trailing_stop` | `trade_reduce` | Places a reduce-only protective stop (ReduceOnly is forced to true in the tool). |
| `deactivate_kill_switch` | `control` | Lifts a safety stop: must not be available to read or paper profiles. |
| `execute_scalping_trade` | `trade_increase` | Opens a scalping trade. |
| `get_24h_stats` | `read` |  |
| `get_account_info` | `read` |  |
| `get_active_subscriptions` | `read` |  |
| `get_advanced_portfolio_metrics` | `read` |  |
| `get_balance` | `read` |  |
| `get_candles` | `read` |  |
| `get_correlation` | `read` |  |
| `get_exchange_info` | `read` |  |
| `get_exposure` | `read` |  |
| `get_funding_rate` | `read` |  |
| `get_futures_positions` | `read` |  |
| `get_index_price` | `read` |  |
| `get_mark_price` | `read` |  |
| `get_market_types` | `read` |  |
| `get_markets` | `read` |  |
| `get_notification_preferences` | `read` |  |
| `get_open_orders` | `read` |  |
| `get_order_history` | `read` |  |
| `get_order_status` | `read` |  |
| `get_orderbook` | `read` |  |
| `get_pair_selection` | `read` |  |
| `get_pnl` | `read` |  |
| `get_portfolio` | `read` |  |
| `get_portfolio_history` | `read` |  |
| `get_portfolio_overview` | `read` |  |
| `get_portfolio_risk_metrics` | `read` |  |
| `get_positions` | `read` |  |
| `get_rebalance_recommendations` | `read` | Recommendations only; it does not place orders. |
| `get_risk_state` | `read` |  |
| `get_scalping_bot_performance` | `read` |  |
| `get_scalping_bot_status` | `read` |  |
| `get_scalping_bot_trades` | `read` |  |
| `get_scalping_signals` | `read` |  |
| `get_supported_pair_formats` | `read` |  |
| `get_ticker` | `read` |  |
| `get_tracked_positions` | `read` |  |
| `get_trade_history` | `read` |  |
| `get_trade_history_v2` | `read` |  |
| `get_trades` | `read` |  |
| `get_trading_mode` | `read` |  |
| `get_websocket_status` | `read` |  |
| `launch_gate_verdict` | `read` |  |
| `launch_get_position` | `read` |  |
| `launch_list_opportunities` | `read` |  |
| `launch_status` | `read` |  |
| `list_tradable_pairs` | `read` |  |
| `llm_analyze_market` | `read` | Spends LLM credits but has no trading or configuration effect. |
| `llm_get_provider_status` | `read` |  |
| `manage_user_subscriptions` | `control` | Reads and writes per-user subscription preferences. |
| `modify_order` | `trade_increase` | Changing price or amount can enlarge exposure; already blocked by the global stop. |
| `register_fcm_token` | `control` | Registers a device for push notifications. |
| `set_notification_preferences` | `control` |  |
| `set_pair_selection` | `control` | Changes the persisted trading universe. |
| `set_trading_mode` | `control` | Changes market type / mode selection. |
| `simulate_order` | `read` | Hypothetical execution; stores nothing and never reaches the exchange. |
| `start_scalping_bot` | `trade_increase` | Starts an autonomous trading loop. |
| `stop_scalping_bot` | `trade_reduce` | Stops a bot and can close its positions. Doubtful: could also be safety. |
| `submit_futures_order_direct` | `trade_increase` | Class depends on arguments: reduce_only or close_order true makes the call trade_reduce. |
| `submit_limit_order` | `trade_increase` |  |
| `submit_market_order` | `trade_increase` |  |
| `submit_order` | `trade_increase` | Its reduce_only flag is NOT trusted for classification (kept as today: blocked by the global stop). |
| `submit_stop_order` | `trade_increase` |  |
| `subscribe_market_data` | `control` | Subscribe/unsubscribe changes server-side WebSocket state. |
| `test_notification` | `control` | Sends a notification. |
| `update_sltp` | `control` | Edits the SL/TP tracked by the SL/TP executor (local position and DB stop, no exchange order is modified), but a looser stop raises risk. Doubtful. |
| `validate_trade` | `read` |  |
