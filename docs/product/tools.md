# Catálogo de tools MCP

Generado desde el registro real (`internal/mcp-trading/toolinventory`) con `UPDATE_TOOL_INVENTORY=1 go test ./internal/mcp-trading/toolinventory/`. No editar a mano: la clase se declara en `toolpolicy/classify.go` y el origen de los datos en `toolpolicy/origin.go`. Una prueba falla si este archivo difiere del generado.

**Total: 105 tools.**

El registro se construye con dependencias nulas e incluye los grupos condicionales de `main.go` (LLM, AnkaLaunch, pares del universo, modo de trading, scalping). En un despliegue concreto pueden faltar algunos: las tools LLM dependen de `config/ai.yaml` y las de universo y modo de `storageManager`.

## Origen de los datos

- **exchange**: valores leídos del exchange (o una acción ejecutada en él).
- **registro local**: hechos registrados localmente (instantáneas de capital, bots, estado de riesgo, configuración, suscripciones).
- **cálculo**: cálculo determinista sobre datos reales o sobre los valores que pasa quien llama (indicadores, tamaños, conversión de formatos).
- **estimación**: proyección, simulación o salida de un modelo. La respuesta lleva `estimate: true` y la lista de supuestos cuando aplica.

Ninguna tool devuelve valores generados, fijos o aleatorios como si fueran datos: si la fuente no responde, devuelve el error `DATA_UNAVAILABLE`.

| Clase | Tools |
|---|---|
| `read` | 79 |
| `trade_increase` | 8 |
| `trade_reduce` | 6 |
| `control` | 11 |
| `safety` | 1 |

| Origen | Tools |
|---|---|
| exchange | 35 |
| registro local | 34 |
| cálculo | 30 |
| estimación | 6 |

## Tools

| Tool | Clase | Origen | Descripción |
|---|---|---|---|
| `activate_kill_switch` | `safety` | registro local | Activate the emergency kill switch to immediately stop all trading operations |
| `analyze_scalping_opportunity` | `read` | cálculo | Analyzes if a trading pair is suitable for scalping based on volatility, spread, and volume. Returns a score (0-100) and recommendation (excellent/good/fair/poor) with detailed metrics. |
| `backtest_strategy` | `read` | estimación | Estimate how a simple strategy (moving_average, rsi, bollinger_bands) would have performed over the most recent real exchange candles. Returns an estimate with its assumptions (period, candle source, fees, slippage, funding not modeled); past results do not predict future ones. |
| `batch_convert_trading_pairs` | `read` | cálculo | Convert multiple trading pairs between different exchange formats in a single operation. Efficient for bulk conversions. |
| `calculate_adx` | `read` | cálculo | Calculate Average Directional Index (ADX, Wilder) with +DI and -DI - trend strength |
| `calculate_atr` | `read` | cálculo | Calculate Average True Range (ATR, Wilder) - volatility indicator |
| `calculate_bollinger` | `read` | cálculo | Calculate Bollinger Bands - volatility indicator with upper, middle, and lower bands |
| `calculate_cci` | `read` | cálculo | Calculate Commodity Channel Index (CCI) - momentum oscillator |
| `calculate_drawdown` | `read` | cálculo | Calculate portfolio drawdown metrics including current drawdown, maximum drawdown, and recovery factor based on historical P&L data |
| `calculate_ema` | `read` | cálculo | Calculate Exponential Moving Average (EMA) - weighted moving average giving more weight to recent prices |
| `calculate_fibonacci` | `read` | cálculo | Calculate Fibonacci retracement levels - key support/resistance levels |
| `calculate_funding_costs` | `read` | estimación | Calculate the funding cost of a futures position from the real current funding rate of the exchange, with projections that assume the rate stays constant (estimate) and the realized rates of the last 7 days when available |
| `calculate_ichimoku` | `read` | cálculo | Calculate Ichimoku lines (9/26/52): tenkan-sen, kijun-sen and the cloud spans projected 26 periods ahead |
| `calculate_liquidation_price` | `read` | estimación | Calculate liquidation price for futures positions with different leverage scenarios and margin adjustments |
| `calculate_macd` | `read` | cálculo | Calculate MACD (Moving Average Convergence Divergence) - trend-following momentum indicator |
| `calculate_macd_futures` | `read` | cálculo | Calculate MACD for Futures contracts - optimized for futures trading with direct symbol usage |
| `calculate_mfi` | `read` | cálculo | Calculate Money Flow Index (MFI) - volume-weighted RSI |
| `calculate_obv` | `read` | cálculo | Calculate On Balance Volume (OBV) over the fetched candle window; its level depends on the window start, read the trend |
| `calculate_position_size` | `read` | cálculo | Calculate optimal position size based on account balance, risk percentage, and stop loss |
| `calculate_rsi` | `read` | cálculo | Calculate Relative Strength Index (RSI) - momentum oscillator measuring speed and magnitude of price changes |
| `calculate_rsi_futures` | `read` | cálculo | Calculate RSI for Futures contracts - optimized for futures trading with direct symbol usage (e.g., 'DOGEUSDTM', 'BTCUSDTM') |
| `calculate_sma` | `read` | cálculo | Calculate Simple Moving Average (SMA) - arithmetic mean of prices over a period |
| `calculate_stochastic` | `read` | cálculo | Calculate Stochastic Oscillator (%K and %D) - compares the close with the high-low range |
| `calculate_stop_loss` | `read` | cálculo | Calculate optimal stop loss based on ATR, support levels, or percentage |
| `calculate_take_profit` | `read` | cálculo | Calculate optimal take profit based on risk-reward ratio or resistance levels |
| `calculate_volatility` | `read` | cálculo | Calculate price volatility: sample standard deviation of the simple returns of the last `period` candles (per candle, not annualized) |
| `calculate_vwap` | `read` | cálculo | Calculate Volume Weighted Average Price (VWAP) of the typical price over the fetched candle window |
| `calculate_williams_r` | `read` | cálculo | Calculate Williams %R (-100..0) - overbought/oversold oscillator |
| `cancel_all_orders` | `trade_reduce` | exchange | Cancel all open orders for a trading pair or entire exchange |
| `cancel_order` | `trade_reduce` | exchange | Cancel a specific order by order ID |
| `check_risk_limits` | `read` | registro local | Check if a proposed order would exceed configured risk limits (order size, position size, daily loss) |
| `close_futures_position` | `trade_reduce` | exchange | Closes an open futures position by placing an opposite market order with reduce-only flag. Automatically determines the correct side and size to fully close the position. Returns confirmation with final PnL and execution details. |
| `close_position_direct` | `trade_reduce` | exchange | Close an existing futures position at market via the DIRECT KuCoin REST client (bypasses GoCryptoTrader's enabled-currency restriction). Works for any contract, including positions opened manually or outside the bot. Uses the position's exact size. |
| `configure_notifications` | `control` | registro local | Configure push notification settings including FCM and APNS |
| `convert_trading_pair` | `read` | cálculo | Convert trading pairs between different exchange formats (e.g., DOGEUSDTM ↔ DOGE-USDT ↔ DOGEUSDT). Supports KuCoin (spot/futures), Binance, Bybit, OKX formats. |
| `create_funding_rate_alert` | `control` | registro local | Create an alert for funding rate thresholds |
| `create_trailing_stop` | `trade_reduce` | exchange | Create a trailing stop order that follows price movements |
| `deactivate_kill_switch` | `control` | registro local | Deactivate the emergency kill switch to resume trading operations |
| `execute_scalping_trade` | `trade_increase` | exchange | Executes a scalping trade with automatic risk management and futures support. Features: - Automatic order type detection (spot for leverage=1, futures for leverage>1) - Automatic symbol conversion for futures (BTC-USDT → BTCUSDTM) - Automatic Stop Loss and Take Profit order creation for futures positions - Risk limit validation and position size calculation - Market order execution with low latency Returns complete order details including main order ID, SL/TP order IDs, execution price, latency, and fees. |
| `get_24h_stats` | `read` | exchange | Get 24-hour trading statistics including volume, price change, high, and low |
| `get_account_info` | `read` | exchange | Get complete account information including all balances, account type, permissions, and trading limits |
| `get_active_subscriptions` | `read` | registro local | Get all active WebSocket subscriptions |
| `get_advanced_portfolio_metrics` | `read` | registro local | Get Sharpe, Sortino, Calmar, volatility, max drawdown and historical VaR computed from the recorded account equity history. Returns DATA_UNAVAILABLE (with how many daily returns are missing) when there is not enough history |
| `get_balance` | `read` | exchange | Get balance for a specific currency on an exchange (available, locked, and total) |
| `get_candles` | `read` | exchange | Get historical OHLCV (Open, High, Low, Close, Volume) candle data for technical analysis |
| `get_correlation` | `read` | cálculo | Calculate Pearson correlation matrix between multiple trading pairs. Useful for portfolio diversification analysis, identifying correlated assets, and finding pairs for statistical arbitrage strategies. |
| `get_exchange_info` | `read` | exchange | Get exchange information including status, trading fees, and supported features |
| `get_exposure` | `read` | registro local | Get current exposure (total position value) per pair and total across all positions |
| `get_funding_rate` | `read` | exchange | Get current funding rate for perpetual futures contracts |
| `get_futures_positions` | `read` | exchange | Retrieves all open futures positions with real-time PnL, leverage, and liquidation information. Returns detailed position data including entry price, current price, unrealized PnL, margin mode, and risk metrics. |
| `get_index_price` | `read` | exchange | Get index price for futures contracts. Index price is the weighted average price from multiple spot exchanges. |
| `get_mark_price` | `read` | exchange | Get mark price for futures contracts. Mark price is used for liquidation calculations and unrealized PnL. |
| `get_market_types` | `read` | cálculo | Get list of supported market types (spot, futures, margin, options) |
| `get_markets` | `read` | exchange | Get list of available trading pairs on an exchange. Supports optional filtering by market type (spot, futures, margin, options). Returns detailed pair information with market features. |
| `get_notification_preferences` | `read` | registro local | Get user notification preferences |
| `get_open_orders` | `read` | exchange | Get all open orders for a trading pair or entire exchange |
| `get_order_history` | `read` | exchange | Get order history (completed, cancelled, and failed orders) with optional time filtering |
| `get_order_status` | `read` | exchange | Get the current status of a specific order |
| `get_orderbook` | `read` | exchange | Get orderbook data with bids and asks for a trading pair. Optionally specify depth to limit results. |
| `get_pair_selection` | `read` | registro local | Get the persisted user pair selection (mode + pairs) for an exchange. |
| `get_pnl` | `read` | exchange | Calculate profit and loss (total and per position) with percentage returns |
| `get_portfolio` | `read` | exchange | Get complete portfolio with all holdings, total value in USD, and P&L |
| `get_portfolio_history` | `read` | registro local | Get historical portfolio value data for performance charts and analysis |
| `get_portfolio_overview` | `read` | exchange | Get comprehensive portfolio overview: spot wallet, futures wallet, open positions with risk metrics, account health, and bot decision context. Use this for any balance, capital, or risk-related analysis. |
| `get_portfolio_risk_metrics` | `read` | registro local | Get portfolio risk: concentration of the live holdings, max drawdown and VaR from the recorded equity history, and optional stress tests. Returns DATA_UNAVAILABLE when there is not enough equity history |
| `get_positions` | `read` | exchange | Get all open positions with entry price, current price, and unrealized P&L |
| `get_rebalance_recommendations` | `read` | estimación | Generate portfolio rebalancing recommendations based on selected strategy |
| `get_risk_state` | `read` | registro local | Get current risk management state |
| `get_scalping_bot_performance` | `read` | registro local | Gets historical performance data of a scalping bot for chart visualization. Returns time-series data of cumulative P&L, win rate, and trade count over time. Supports multiple timeframes: 1h, 4h, 24h, 7d, 30d. |
| `get_scalping_bot_status` | `read` | registro local | Gets the current status and performance metrics of a scalping bot. Returns detailed information including trades executed, PnL, win rate, and recent signals. |
| `get_scalping_bot_trades` | `read` | registro local | Gets the trade history of a scalping bot. Returns a list of executed trades with entry/exit prices, PnL, and duration. Supports pagination with limit and offset parameters. |
| `get_scalping_signals` | `read` | cálculo | Gets real-time scalping signals for multiple trading pairs using various strategies. Analyzes market conditions and returns buy/sell signals with confidence scores. Supports RSI, MACD, Bollinger Bands, and Volume strategies. |
| `get_supported_pair_formats` | `read` | cálculo | Get all supported trading pair formats and market types with examples. Useful for understanding available conversion options. |
| `get_ticker` | `read` | exchange | Get current ticker data including last price, bid, ask, volume, and 24h statistics for a trading pair |
| `get_tracked_positions` | `read` | registro local | Get positions tracked by the risk PositionTracker with details including P&L, SL/TP, and monitoring status |
| `get_trade_history` | `read` | exchange | Get trade history with timestamps, prices, amounts, and fees |
| `get_trade_history_v2` | `read` | exchange | Get executed trade history with time range filters, including trade ID, pair, side, price, amount, fee, timestamp, and calculated P&L |
| `get_trades` | `read` | exchange | Get recent trades executed on the exchange for a trading pair |
| `get_trading_mode` | `read` | registro local | Get the effective trading mode (market_type, mode_selection, leverage). If pair is supplied and a per-pair override exists, that override is returned; otherwise the global mode is returned. |
| `get_websocket_status` | `read` | registro local | Get WebSocket connection status and statistics |
| `launch_gate_verdict` | `read` | registro local | Current AnkaLaunch edge-gate verdict (INSUFFICIENT_DATA / NO_EDGE / EDGE) with its metrics, requirements and recent history (read-only). |
| `launch_get_position` | `read` | registro local | Get one AnkaLaunch shadow position with costs, PnL and orders (read-only). |
| `launch_list_opportunities` | `read` | registro local | List AnkaLaunch opportunities (read-only), newest first, with score breakdown and decision. Filter by band and status. |
| `launch_status` | `read` | registro local | AnkaLaunch status (read-only): service heartbeat, sources, circuit breaker, current edge-gate verdict, shadow exposure/PnL and 24h counters. Shadow mode only, no real money. |
| `list_tradable_pairs` | `read` | exchange | List live tradable pairs for an exchange. Useful to discover valid values before calling set_pair_selection. |
| `llm_analyze_market` | `read` | estimación | Analyze market conditions using LLM (AI) and provide trading recommendations. Supports multiple providers: Gemini, OpenAI, Anthropic, OpenRouter. |
| `llm_get_provider_status` | `read` | registro local | Get health status and statistics for all configured LLM providers (Gemini, OpenAI, Anthropic, OpenRouter) |
| `manage_user_subscriptions` | `control` | registro local | Manage user-specific market data subscriptions and preferences |
| `modify_order` | `trade_increase` | exchange | Modify an existing order (price and/or amount) |
| `register_fcm_token` | `control` | registro local | Register a Flutter FCM device token for push notifications |
| `set_notification_preferences` | `control` | registro local | Set user notification preferences and device tokens |
| `set_pair_selection` | `control` | registro local | Persist a user pair selection (mode + pairs) for an exchange. Mode must be auto, manual, or hybrid. For manual/hybrid, pairs are validated against live tradable pairs. |
| `set_trading_mode` | `control` | registro local | Set the trading mode. mode=auto resets to automatic selection (mode_selection=auto). mode=spot\|futures\|margin sets market_type explicitly (mode_selection=manual). mode=hedge is rejected (not supported). scope=global applies to all pairs; scope=pair targets a specific pair (pair required). leverage is optional; spot enforces leverage<=1. |
| `simulate_order` | `read` | estimación | Simulate an order execution and return an estimate with its assumptions (price source, slippage, fees). Calculates execution price with slippage, trading fees, total cost/proceeds, and validates against risk limits WITHOUT executing a real order. |
| `start_scalping_bot` | `trade_increase` | registro local | Starts an autonomous scalping bot that monitors specified pairs and executes trades automatically. The bot uses configured strategies and respects risk limits. Returns a bot_id that can be used to monitor or stop the bot. |
| `stop_scalping_bot` | `trade_reduce` | registro local | Stops a running scalping bot. Optionally closes all open positions. Returns final statistics including total trades, PnL, and win rate. |
| `submit_futures_order_direct` | `trade_increase` | exchange | Submit a futures order (market/limit/stop) via the DIRECT KuCoin REST client, bypassing GoCryptoTrader's enabled-currency restriction. Supports protective stops (type='stop' + stop_price + reduce_only) and take-profit/entry orders on ANY contract. |
| `submit_limit_order` | `trade_increase` | exchange | Submit a limit order that executes only at specified price or better |
| `submit_market_order` | `trade_increase` | exchange | Submit a market order that executes immediately at current market price |
| `submit_order` | `trade_increase` | exchange | Submit a trading order (market, limit, or stop) to an exchange |
| `submit_stop_order` | `trade_increase` | exchange | Submit a stop order that triggers when price reaches stop price |
| `subscribe_market_data` | `control` | registro local | Subscribe to real-time market data via WebSocket |
| `test_notification` | `control` | registro local | Send a test notification to verify configuration |
| `update_sltp` | `control` | registro local | Update stop-loss and take-profit levels for an open position |
| `validate_trade` | `read` | cálculo | Validate a trade before execution. Checks order parameters, balance sufficiency, position size limits, daily loss limits, leverage limits, pair availability, market hours, and order size constraints. |
