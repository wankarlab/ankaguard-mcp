# Inventario de tools MCP

Generado por `internal/mcp-trading/toolinventory` (`UPDATE_TOOL_INVENTORY=1 go test ./internal/mcp-trading/toolinventory/ -run TestToolInventory`). No editar a mano.

**Total de tools registradas: 105**

El registro se construye con dependencias nulas (`BuildFullRegistry`) e incluye los grupos condicionales de `main.go` (LLM, AnkaLaunch, pares del universo, modo de trading, scalping). En un despliegue real el número puede ser menor: las tools LLM dependen de `config/ai.yaml` (`llm.enabled`), y las de universo y modo de `storageManager != nil`. Los paquetes `tools/config` (`config_get`, `config_set`, `config_apply_preset`) y `tools/specs` (`specs.*`) nunca se cablean en `main.go` y por eso no forman parte del registro.

| # | Tool | Paquete | Descripción |
|---|---|---|---|
| 1 | `activate_kill_switch` | `internal/mcp-trading/tools/risk` | Activate the emergency kill switch to immediately stop all trading operations |
| 2 | `analyze_scalping_opportunity` | `internal/mcp-trading/tools/scalping` | Analyzes if a trading pair is suitable for scalping based on volatility, spread, and volume. Returns a score (0-100) and recommendation (excellent/good/fair/poor) with detailed metrics. |
| 3 | `backtest_strategy` | `internal/mcp-trading/tools/backtest` | Estimate how a simple strategy (moving_average, rsi, bollinger_bands) would have performed over the most recent real exchange candles. Returns an estimate with its assumptions (period, candle source, fees, slippage, funding not modeled); past results do not predict future ones. |
| 4 | `batch_convert_trading_pairs` | `internal/mcp-trading/tools/pairs` | Convert multiple trading pairs between different exchange formats in a single operation. Efficient for bulk conversions. |
| 5 | `calculate_adx` | `internal/mcp-trading/tools/technical` | Calculate Average Directional Index (ADX, Wilder) with +DI and -DI - trend strength |
| 6 | `calculate_atr` | `internal/mcp-trading/tools/technical` | Calculate Average True Range (ATR, Wilder) - volatility indicator |
| 7 | `calculate_bollinger` | `internal/mcp-trading/tools/technical` | Calculate Bollinger Bands - volatility indicator with upper, middle, and lower bands |
| 8 | `calculate_cci` | `internal/mcp-trading/tools/technical` | Calculate Commodity Channel Index (CCI) - momentum oscillator |
| 9 | `calculate_drawdown` | `internal/mcp-trading/tools/portfolio` | Calculate portfolio drawdown metrics including current drawdown, maximum drawdown, and recovery factor based on historical P&L data |
| 10 | `calculate_ema` | `internal/mcp-trading/tools/technical` | Calculate Exponential Moving Average (EMA) - weighted moving average giving more weight to recent prices |
| 11 | `calculate_fibonacci` | `internal/mcp-trading/tools/technical` | Calculate Fibonacci retracement levels - key support/resistance levels |
| 12 | `calculate_funding_costs` | `internal/mcp-trading/tools/futures` | Calculate the funding cost of a futures position from the real current funding rate of the exchange, with projections that assume the rate stays constant (estimate) and the realized rates of the last 7 days when available |
| 13 | `calculate_ichimoku` | `internal/mcp-trading/tools/technical` | Calculate Ichimoku lines (9/26/52): tenkan-sen, kijun-sen and the cloud spans projected 26 periods ahead |
| 14 | `calculate_liquidation_price` | `internal/mcp-trading/tools/futures` | Calculate liquidation price for futures positions with different leverage scenarios and margin adjustments |
| 15 | `calculate_macd` | `internal/mcp-trading/tools/technical` | Calculate MACD (Moving Average Convergence Divergence) - trend-following momentum indicator |
| 16 | `calculate_macd_futures` | `internal/mcp-trading/tools/technical` | Calculate MACD for Futures contracts - optimized for futures trading with direct symbol usage |
| 17 | `calculate_mfi` | `internal/mcp-trading/tools/technical` | Calculate Money Flow Index (MFI) - volume-weighted RSI |
| 18 | `calculate_obv` | `internal/mcp-trading/tools/technical` | Calculate On Balance Volume (OBV) over the fetched candle window; its level depends on the window start, read the trend |
| 19 | `calculate_position_size` | `internal/mcp-trading/tools/risk` | Calculate optimal position size based on account balance, risk percentage, and stop loss |
| 20 | `calculate_rsi` | `internal/mcp-trading/tools/technical` | Calculate Relative Strength Index (RSI) - momentum oscillator measuring speed and magnitude of price changes |
| 21 | `calculate_rsi_futures` | `internal/mcp-trading/tools/technical` | Calculate RSI for Futures contracts - optimized for futures trading with direct symbol usage (e.g., 'DOGEUSDTM', 'BTCUSDTM') |
| 22 | `calculate_sma` | `internal/mcp-trading/tools/technical` | Calculate Simple Moving Average (SMA) - arithmetic mean of prices over a period |
| 23 | `calculate_stochastic` | `internal/mcp-trading/tools/technical` | Calculate Stochastic Oscillator (%K and %D) - compares the close with the high-low range |
| 24 | `calculate_stop_loss` | `internal/mcp-trading/tools/risk` | Calculate optimal stop loss based on ATR, support levels, or percentage |
| 25 | `calculate_take_profit` | `internal/mcp-trading/tools/risk` | Calculate optimal take profit based on risk-reward ratio or resistance levels |
| 26 | `calculate_volatility` | `internal/mcp-trading/tools/technical` | Calculate price volatility: sample standard deviation of the simple returns of the last `period` candles (per candle, not annualized) |
| 27 | `calculate_vwap` | `internal/mcp-trading/tools/technical` | Calculate Volume Weighted Average Price (VWAP) of the typical price over the fetched candle window |
| 28 | `calculate_williams_r` | `internal/mcp-trading/tools/technical` | Calculate Williams %R (-100..0) - overbought/oversold oscillator |
| 29 | `cancel_all_orders` | `internal/mcp-trading/tools/orders` | Cancel all open orders for a trading pair or entire exchange |
| 30 | `cancel_order` | `internal/mcp-trading/tools/orders` | Cancel a specific order by order ID |
| 31 | `check_risk_limits` | `internal/mcp-trading/tools/risk` | Check if a proposed order would exceed configured risk limits (order size, position size, daily loss) |
| 32 | `close_futures_position` | `internal/mcp-trading/tools/scalping` | Closes an open futures position by placing an opposite market order with reduce-only flag. Automatically determines the correct side and size to fully close the position. Returns confirmation with final PnL and execution details. |
| 33 | `close_position_direct` | `internal/mcp-trading/tools/orders` | Close an existing futures position at market via the DIRECT KuCoin REST client (bypasses GoCryptoTrader's enabled-currency restriction). Works for any contract, including positions opened manually or outside the bot. Uses the position's exact size. |
| 34 | `configure_notifications` | `internal/mcp-trading/tools/notifications` | Configure push notification settings including FCM and APNS |
| 35 | `convert_trading_pair` | `internal/mcp-trading/tools/pairs` | Convert trading pairs between different exchange formats (e.g., DOGEUSDTM ↔ DOGE-USDT ↔ DOGEUSDT). Supports KuCoin (spot/futures), Binance, Bybit, OKX formats. |
| 36 | `create_funding_rate_alert` | `internal/mcp-trading/tools/notifications` | Create an alert for funding rate thresholds |
| 37 | `create_trailing_stop` | `internal/mcp-trading/tools/orders` | Create a trailing stop order that follows price movements |
| 38 | `deactivate_kill_switch` | `internal/mcp-trading/tools/risk` | Deactivate the emergency kill switch to resume trading operations |
| 39 | `execute_scalping_trade` | `internal/mcp-trading/tools/scalping` | Executes a scalping trade with automatic risk management and futures support. Features: - Automatic order type detection (spot for leverage=1, futures for leverage>1) - Automatic symbol conversion for futures (BTC-USDT → BTCUSDTM) - Automatic Stop Loss and Take Profit order creation for futures positions - Risk limit validation and position size calculation - Market order execution with low latency Returns complete order details including main order ID, SL/TP order IDs, execution price, latency, and fees. |
| 40 | `get_24h_stats` | `internal/mcp-trading/tools/marketdata` | Get 24-hour trading statistics including volume, price change, high, and low |
| 41 | `get_account_info` | `internal/mcp-trading/tools/portfolio` | Get complete account information including all balances, account type, permissions, and trading limits |
| 42 | `get_active_subscriptions` | `internal/mcp-trading/tools/websocket` | Get all active WebSocket subscriptions |
| 43 | `get_advanced_portfolio_metrics` | `internal/mcp-trading/tools/portfolio` | Get Sharpe, Sortino, Calmar, volatility, max drawdown and historical VaR computed from the recorded account equity history. Returns DATA_UNAVAILABLE (with how many daily returns are missing) when there is not enough history |
| 44 | `get_balance` | `internal/mcp-trading/tools/portfolio` | Get balance for a specific currency on an exchange (available, locked, and total) |
| 45 | `get_candles` | `internal/mcp-trading/tools/marketdata` | Get historical OHLCV (Open, High, Low, Close, Volume) candle data for technical analysis |
| 46 | `get_correlation` | `internal/mcp-trading/tools/technical` | Calculate Pearson correlation matrix between multiple trading pairs. Useful for portfolio diversification analysis, identifying correlated assets, and finding pairs for statistical arbitrage strategies. |
| 47 | `get_exchange_info` | `internal/mcp-trading/tools/marketdata` | Get exchange information including status, trading fees, and supported features |
| 48 | `get_exposure` | `internal/mcp-trading/tools/risk` | Get current exposure (total position value) per pair and total across all positions |
| 49 | `get_funding_rate` | `internal/mcp-trading/tools/marketdata` | Get current funding rate for perpetual futures contracts |
| 50 | `get_futures_positions` | `internal/mcp-trading/tools/scalping` | Retrieves all open futures positions with real-time PnL, leverage, and liquidation information. Returns detailed position data including entry price, current price, unrealized PnL, margin mode, and risk metrics. |
| 51 | `get_index_price` | `internal/mcp-trading/tools/marketdata` | Get index price for futures contracts. Index price is the weighted average price from multiple spot exchanges. |
| 52 | `get_mark_price` | `internal/mcp-trading/tools/marketdata` | Get mark price for futures contracts. Mark price is used for liquidation calculations and unrealized PnL. |
| 53 | `get_market_types` | `internal/mcp-trading/tools/marketdata` | Get list of supported market types (spot, futures, margin, options) |
| 54 | `get_markets` | `internal/mcp-trading/tools/marketdata` | Get list of available trading pairs on an exchange. Supports optional filtering by market type (spot, futures, margin, options). Returns detailed pair information with market features. |
| 55 | `get_notification_preferences` | `internal/mcp-trading/tools/notifications` | Get user notification preferences |
| 56 | `get_open_orders` | `internal/mcp-trading/tools/orders` | Get all open orders for a trading pair or entire exchange |
| 57 | `get_order_history` | `internal/mcp-trading/tools/orders` | Get order history (completed, cancelled, and failed orders) with optional time filtering |
| 58 | `get_order_status` | `internal/mcp-trading/tools/orders` | Get the current status of a specific order |
| 59 | `get_orderbook` | `internal/mcp-trading/tools/marketdata` | Get orderbook data with bids and asks for a trading pair. Optionally specify depth to limit results. |
| 60 | `get_pair_selection` | `internal/mcp-trading/tools/pairs` | Get the persisted user pair selection (mode + pairs) for an exchange. |
| 61 | `get_pnl` | `internal/mcp-trading/tools/portfolio` | Calculate profit and loss (total and per position) with percentage returns |
| 62 | `get_portfolio` | `internal/mcp-trading/tools/portfolio` | Get complete portfolio with all holdings, total value in USD, and P&L |
| 63 | `get_portfolio_history` | `internal/mcp-trading/tools/portfolio` | Get historical portfolio value data for performance charts and analysis |
| 64 | `get_portfolio_overview` | `internal/mcp-trading/tools/portfolio` | Get comprehensive portfolio overview: spot wallet, futures wallet, open positions with risk metrics, account health, and bot decision context. Use this for any balance, capital, or risk-related analysis. |
| 65 | `get_portfolio_risk_metrics` | `internal/mcp-trading/tools/portfolio` | Get portfolio risk: concentration of the live holdings, max drawdown and VaR from the recorded equity history, and optional stress tests. Returns DATA_UNAVAILABLE when there is not enough equity history |
| 66 | `get_positions` | `internal/mcp-trading/tools/portfolio` | Get all open positions with entry price, current price, and unrealized P&L |
| 67 | `get_rebalance_recommendations` | `internal/mcp-trading/tools/portfolio` | Generate portfolio rebalancing recommendations based on selected strategy |
| 68 | `get_risk_state` | `internal/mcp-trading/tools/risk` | Get current risk management state |
| 69 | `get_scalping_bot_performance` | `internal/mcp-trading/tools/scalping` | Gets historical performance data of a scalping bot for chart visualization. Returns time-series data of cumulative P&L, win rate, and trade count over time. Supports multiple timeframes: 1h, 4h, 24h, 7d, 30d. |
| 70 | `get_scalping_bot_status` | `internal/mcp-trading/tools/scalping` | Gets the current status and performance metrics of a scalping bot. Returns detailed information including trades executed, PnL, win rate, and recent signals. |
| 71 | `get_scalping_bot_trades` | `internal/mcp-trading/tools/scalping` | Gets the trade history of a scalping bot. Returns a list of executed trades with entry/exit prices, PnL, and duration. Supports pagination with limit and offset parameters. |
| 72 | `get_scalping_signals` | `internal/mcp-trading/tools/scalping` | Gets real-time scalping signals for multiple trading pairs using various strategies. Analyzes market conditions and returns buy/sell signals with confidence scores. Supports RSI, MACD, Bollinger Bands, and Volume strategies. |
| 73 | `get_supported_pair_formats` | `internal/mcp-trading/tools/pairs` | Get all supported trading pair formats and market types with examples. Useful for understanding available conversion options. |
| 74 | `get_ticker` | `internal/mcp-trading/tools/marketdata` | Get current ticker data including last price, bid, ask, volume, and 24h statistics for a trading pair |
| 75 | `get_tracked_positions` | `internal/mcp-trading/tools/risk` | Get positions tracked by the risk PositionTracker with details including P&L, SL/TP, and monitoring status |
| 76 | `get_trade_history` | `internal/mcp-trading/tools/portfolio` | Get trade history with timestamps, prices, amounts, and fees |
| 77 | `get_trade_history_v2` | `internal/mcp-trading/tools/portfolio` | Get executed trade history with time range filters, including trade ID, pair, side, price, amount, fee, timestamp, and calculated P&L |
| 78 | `get_trades` | `internal/mcp-trading/tools/marketdata` | Get recent trades executed on the exchange for a trading pair |
| 79 | `get_trading_mode` | `internal/mcp-trading/tools/mode` | Get the effective trading mode (market_type, mode_selection, leverage). If pair is supplied and a per-pair override exists, that override is returned; otherwise the global mode is returned. |
| 80 | `get_websocket_status` | `internal/mcp-trading/tools/websocket` | Get WebSocket connection status and statistics |
| 81 | `launch_gate_verdict` | `internal/mcp-trading/tools/launch` | Current AnkaLaunch edge-gate verdict (INSUFFICIENT_DATA / NO_EDGE / EDGE) with its metrics, requirements and recent history (read-only). |
| 82 | `launch_get_position` | `internal/mcp-trading/tools/launch` | Get one AnkaLaunch shadow position with costs, PnL and orders (read-only). |
| 83 | `launch_list_opportunities` | `internal/mcp-trading/tools/launch` | List AnkaLaunch opportunities (read-only), newest first, with score breakdown and decision. Filter by band and status. |
| 84 | `launch_status` | `internal/mcp-trading/tools/launch` | AnkaLaunch status (read-only): service heartbeat, sources, circuit breaker, current edge-gate verdict, shadow exposure/PnL and 24h counters. Shadow mode only, no real money. |
| 85 | `list_tradable_pairs` | `internal/mcp-trading/tools/pairs` | List live tradable pairs for an exchange. Useful to discover valid values before calling set_pair_selection. |
| 86 | `llm_analyze_market` | `internal/mcp-trading/tools/llm` | Analyze market conditions using LLM (AI) and provide trading recommendations. Supports multiple providers: Gemini, OpenAI, Anthropic, OpenRouter. |
| 87 | `llm_get_provider_status` | `internal/mcp-trading/tools/llm` | Get health status and statistics for all configured LLM providers (Gemini, OpenAI, Anthropic, OpenRouter) |
| 88 | `manage_user_subscriptions` | `internal/mcp-trading/tools/websocket` | Manage user-specific market data subscriptions and preferences |
| 89 | `modify_order` | `internal/mcp-trading/tools/orders` | Modify an existing order (price and/or amount) |
| 90 | `register_fcm_token` | `internal/mcp-trading/tools/notifications` | Register a Flutter FCM device token for push notifications |
| 91 | `set_notification_preferences` | `internal/mcp-trading/tools/notifications` | Set user notification preferences and device tokens |
| 92 | `set_pair_selection` | `internal/mcp-trading/tools/pairs` | Persist a user pair selection (mode + pairs) for an exchange. Mode must be auto, manual, or hybrid. For manual/hybrid, pairs are validated against live tradable pairs. |
| 93 | `set_trading_mode` | `internal/mcp-trading/tools/mode` | Set the trading mode. mode=auto resets to automatic selection (mode_selection=auto). mode=spot\|futures\|margin sets market_type explicitly (mode_selection=manual). mode=hedge is rejected (not supported). scope=global applies to all pairs; scope=pair targets a specific pair (pair required). leverage is optional; spot enforces leverage<=1. |
| 94 | `simulate_order` | `internal/mcp-trading/tools/orders` | Simulate an order execution and return an estimate with its assumptions (price source, slippage, fees). Calculates execution price with slippage, trading fees, total cost/proceeds, and validates against risk limits WITHOUT executing a real order. |
| 95 | `start_scalping_bot` | `internal/mcp-trading/tools/scalping` | Starts an autonomous scalping bot that monitors specified pairs and executes trades automatically. The bot uses configured strategies and respects risk limits. Returns a bot_id that can be used to monitor or stop the bot. |
| 96 | `stop_scalping_bot` | `internal/mcp-trading/tools/scalping` | Stops a running scalping bot. Optionally closes all open positions. Returns final statistics including total trades, PnL, and win rate. |
| 97 | `submit_futures_order_direct` | `internal/mcp-trading/tools/orders` | Submit a futures order (market/limit/stop) via the DIRECT KuCoin REST client, bypassing GoCryptoTrader's enabled-currency restriction. Supports protective stops (type='stop' + stop_price + reduce_only) and take-profit/entry orders on ANY contract. |
| 98 | `submit_limit_order` | `internal/mcp-trading/tools/orders` | Submit a limit order that executes only at specified price or better |
| 99 | `submit_market_order` | `internal/mcp-trading/tools/orders` | Submit a market order that executes immediately at current market price |
| 100 | `submit_order` | `internal/mcp-trading/tools/orders` | Submit a trading order (market, limit, or stop) to an exchange |
| 101 | `submit_stop_order` | `internal/mcp-trading/tools/orders` | Submit a stop order that triggers when price reaches stop price |
| 102 | `subscribe_market_data` | `internal/mcp-trading/tools/websocket` | Subscribe to real-time market data via WebSocket |
| 103 | `test_notification` | `internal/mcp-trading/tools/notifications` | Send a test notification to verify configuration |
| 104 | `update_sltp` | `internal/mcp-trading/tools/risk` | Update stop-loss and take-profit levels for an open position |
| 105 | `validate_trade` | `internal/mcp-trading/tools/risk` | Validate a trade before execution. Checks order parameters, balance sufficiency, position size limits, daily loss limits, leverage limits, pair availability, market hours, and order size constraints. |
