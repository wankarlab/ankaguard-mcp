package gct

import (
	"context"
	"encoding/json"
	"math"
	"time"
)

// PortfolioOverview is the unified portfolio snapshot returned by GetPortfolioOverview.
type PortfolioOverview struct {
	Timestamp  time.Time        `json:"timestamp"`
	Exchange   string           `json:"exchange"`
	Summary    OverviewSummary  `json:"summary"`
	Spot       SpotWallet       `json:"spot"`
	Futures    FuturesWallet    `json:"futures"`
	Positions  []PositionDetail `json:"positions"`
	Risk       RiskMetrics      `json:"risk"`
	BotContext BotContext        `json:"bot_context"`
	Paper      bool              `json:"paper,omitempty"` // served by the paper broker
}

// OverviewSummary contains top-level aggregated figures.
type OverviewSummary struct {
	TotalEquityUSD      float64 `json:"total_equity_usd"`
	SpotEquityUSD       float64 `json:"spot_equity_usd"`
	FuturesEquityUSD    float64 `json:"futures_equity_usd"`
	UnrealizedPnLUSD    float64 `json:"unrealized_pnl_usd"`
	AvailableForTrading float64 `json:"available_for_trading_usd"`
	TotalExposureUSD    float64 `json:"total_exposure_usd"`
}

// SpotWallet holds spot account balances.
type SpotWallet struct {
	TotalUSD     float64       `json:"total_usd"`
	AvailableUSD float64       `json:"available_usd"`
	LockedUSD    float64       `json:"locked_usd"`
	Holdings     []SpotHolding `json:"holdings"`
}

// SpotHolding represents one currency in the spot wallet.
type SpotHolding struct {
	Currency  string  `json:"currency"`
	Total     float64 `json:"total"`
	Available float64 `json:"available"`
	Locked    float64 `json:"locked"`
	ValueUSD  float64 `json:"value_usd"`
}

// FuturesWallet holds futures account details.
type FuturesWallet struct {
	Currency         string  `json:"currency"`
	AccountEquity    float64 `json:"account_equity"`
	AvailableBalance float64 `json:"available_balance"`
	PositionMargin   float64 `json:"position_margin"`
	OrderMargin      float64 `json:"order_margin"`
	FrozenFunds      float64 `json:"frozen_funds"`
	UnrealizedPnL    float64 `json:"unrealized_pnl"`
	RealizedPnL      float64 `json:"realized_pnl"`
	MarginRatioPct   float64 `json:"margin_ratio_pct"`
}

// PositionDetail is an enriched single futures position.
type PositionDetail struct {
	Symbol            string  `json:"symbol"`
	Side              string  `json:"side"`
	Size              float64 `json:"size"`
	EntryPrice        float64 `json:"entry_price"`
	CurrentPrice      float64 `json:"current_price"`
	Leverage          float64 `json:"leverage"`
	MarginMode        string  `json:"margin_mode"`
	MarginUSD         float64 `json:"margin_usd"`
	NotionalUSD       float64 `json:"notional_usd"`
	UnrealizedPnL     float64 `json:"unrealized_pnl"`
	UnrealizedPnLPct  float64 `json:"unrealized_pnl_pct"`
	RealizedPnL       float64 `json:"realized_pnl"`
	LiquidationPrice  float64 `json:"liquidation_price"`
	LiqDistancePct    float64 `json:"liq_distance_pct"`
	EffectiveLeverage float64 `json:"effective_leverage"`
	ROE               float64 `json:"roe_pct"`
}

// RiskMetrics contains account-level risk figures.
type RiskMetrics struct {
	MarginUsedUSD         float64 `json:"margin_used_usd"`
	MarginAvailableUSD    float64 `json:"margin_available_usd"`
	MarginRatioPct        float64 `json:"margin_ratio_pct"`
	TotalExposureUSD      float64 `json:"total_exposure_usd"`
	OpenPositions         int     `json:"open_positions"`
	AccountHealth         string  `json:"account_health"`
	CanOpenPosition       bool    `json:"can_open_position"`
	MaxNewPositionUSD     float64 `json:"max_new_position_usd"`
	DailyLossUSD          float64 `json:"daily_loss_usd"`
	DailyLossLimitUSD     float64 `json:"daily_loss_limit_usd"`
	DailyLossRemainingUSD float64 `json:"daily_loss_remaining_usd"`
}

// BotContext gives the AI bot the fields it needs to decide whether to open a trade.
type BotContext struct {
	ConfiguredTradeSizeUSD float64 `json:"configured_trade_size_usd"`
	ConfiguredLeverage     int     `json:"configured_leverage"`
	MaxDailyLossUSD        float64 `json:"max_daily_loss_usd"`
	CapitalForBotUSD       float64 `json:"capital_for_bot_usd"`
	PositionsUsed          int     `json:"positions_used"`
	PositionsMax           int     `json:"positions_max"`
	CanEnterTrade          bool    `json:"can_enter_trade"`
	CannotEnterReason      string  `json:"cannot_enter_reason,omitempty"`
	ConfidenceThreshold    float64 `json:"confidence_threshold"`
}

// calcAccountHealth returns "healthy", "warning", or "critical" based on margin ratio.
func calcAccountHealth(marginRatioPct float64) string {
	switch {
	case marginRatioPct < 20:
		return "healthy"
	case marginRatioPct < 50:
		return "warning"
	default:
		return "critical"
	}
}

// calcLiqDistancePct calculates the percentage distance from current price to liquidation.
// Returns 999 when liquidation price is zero or a placeholder value (≤ 0.001).
func calcLiqDistancePct(currentPrice, liqPrice float64) float64 {
	if liqPrice <= 0.001 || currentPrice == 0 {
		return 999
	}
	return math.Abs(currentPrice-liqPrice) / currentPrice * 100
}

// kuCoinFuturesAccount is the raw response from /api/v1/account-overview.
type kuCoinFuturesAccount struct {
	Currency         string  `json:"currency"`
	AccountEquity    float64 `json:"accountEquity"`
	AvailableBalance float64 `json:"availableBalance"`
	PositionMargin   float64 `json:"positionMargin"`
	OrderMargin      float64 `json:"orderMargin"`
	FrozenFunds      float64 `json:"frozenFunds"`
	UnrealisedPnl    float64 `json:"unrealisedPnl"`
	RealisedPnl      float64 `json:"realisedPnl"`
}

// kuCoinRawPosition is the raw position object from /api/v1/positions.
type kuCoinRawPosition struct {
	Symbol         string  `json:"symbol"`
	CurrentQty     float64 `json:"currentQty"`
	AvgEntryPrice  float64 `json:"avgEntryPrice"`
	MarkPrice      float64 `json:"markPrice"`
	UnrealisedPnl  float64 `json:"unrealisedPnl"`
	RealisedPnl    float64 `json:"realisedPnl"`
	MaintMarginReq float64 `json:"maintMarginReq"`
	PosMargin      float64 `json:"posMargin"`
	LiqPrice       float64 `json:"liqPrice"`
	MarginMode     string  `json:"marginMode"` // "CROSS" or "ISOLATED"
	Leverage       float64 `json:"leverage"`
	IsOpen         bool    `json:"isOpen"`
}

// GetFullPortfolioOverview fetches spot balances, the USDT futures account, and all open
// futures positions in three API calls and returns the raw data.
func (k *KuCoinRESTClient) GetFullPortfolioOverview(ctx context.Context) (*PortfolioOverview, error) {
	if !k.HasCredentials() {
		return nil, ErrInvalidCredentials
	}
	if ctx == nil {
		ctx = context.Background()
	}

	// --- 1. Spot balances ---
	spotBalances, err := k.GetAllSpotBalances(ctx)
	if err != nil {
		return nil, err
	}

	// --- 2. Futures account overview ---
	futuresAcc, _ := k.fetchFuturesAccountOverview(ctx, "USDT")

	// --- 3. Open futures positions (non-fatal) ---
	rawPositions, _ := k.fetchRawPositions(ctx)

	// --- Build SpotWallet ---
	spot := SpotWallet{Holdings: make([]SpotHolding, 0)}
	for currency, bal := range spotBalances {
		total := bal["total"].(float64)
		if total <= 0 {
			continue
		}
		available := bal["available"].(float64)
		locked := bal["in_order"].(float64)
		valueUSD := 0.0
		if currency == "USDT" {
			valueUSD = total
		}
		spot.Holdings = append(spot.Holdings, SpotHolding{
			Currency:  currency,
			Total:     total,
			Available: available,
			Locked:    locked,
			ValueUSD:  valueUSD,
		})
		if currency == "USDT" {
			spot.TotalUSD += total
			spot.AvailableUSD += available
			spot.LockedUSD += locked
		}
	}

	// --- Build FuturesWallet ---
	futuresMarginRatio := 0.0
	if futuresAcc.AccountEquity > 0 {
		futuresMarginRatio = futuresAcc.PositionMargin / futuresAcc.AccountEquity * 100
	}
	futures := FuturesWallet{
		Currency:         futuresAcc.Currency,
		AccountEquity:    futuresAcc.AccountEquity,
		AvailableBalance: futuresAcc.AvailableBalance,
		PositionMargin:   futuresAcc.PositionMargin,
		OrderMargin:      futuresAcc.OrderMargin,
		FrozenFunds:      futuresAcc.FrozenFunds,
		UnrealizedPnL:    futuresAcc.UnrealisedPnl,
		RealizedPnL:      futuresAcc.RealisedPnl,
		MarginRatioPct:   futuresMarginRatio,
	}

	// --- Build PositionDetail slice ---
	positions := make([]PositionDetail, 0, len(rawPositions))
	totalExposure := 0.0
	totalUnrealized := 0.0
	for _, rp := range rawPositions {
		size := math.Abs(rp.CurrentQty)
		side := "long"
		if rp.CurrentQty < 0 {
			side = "short"
		}
		// Contract multiplier (base units per contract): 1.0 for NEARUSDTM, 0.01
		// for QNTUSDTM, 0.001 for BTC... Notional MUST apply it or the exposure
		// is overstated by 1/multiplier. Falls back to 1.0 if the lookup fails.
		contractMultiplier := 1.0
		if m, merr := k.CachedContractMultiplier(ctx, rp.Symbol); merr == nil && m > 0 {
			contractMultiplier = m
		}
		notional := size * rp.MarkPrice * contractMultiplier
		totalExposure += notional

		unrealizedPct := 0.0
		if rp.PosMargin > 0 {
			unrealizedPct = rp.UnrealisedPnl / rp.PosMargin * 100
		}
		effectiveLev := 0.0
		if rp.PosMargin > 0 {
			effectiveLev = notional / rp.PosMargin
		}
		roe := 0.0
		if rp.PosMargin > 0 {
			roe = rp.UnrealisedPnl / rp.PosMargin * 100
		}
		marginMode := rp.MarginMode
		if marginMode == "" {
			marginMode = "CROSS"
		}

		totalUnrealized += rp.UnrealisedPnl

		positions = append(positions, PositionDetail{
			Symbol:            rp.Symbol,
			Side:              side,
			Size:              size,
			EntryPrice:        rp.AvgEntryPrice,
			CurrentPrice:      rp.MarkPrice,
			Leverage:          rp.Leverage,
			MarginMode:        marginMode,
			MarginUSD:         rp.PosMargin,
			NotionalUSD:       notional,
			UnrealizedPnL:     rp.UnrealisedPnl,
			UnrealizedPnLPct:  unrealizedPct,
			RealizedPnL:       rp.RealisedPnl,
			LiquidationPrice:  rp.LiqPrice,
			LiqDistancePct:    calcLiqDistancePct(rp.MarkPrice, rp.LiqPrice),
			EffectiveLeverage: effectiveLev,
			ROE:               roe,
		})
	}

	// --- Summary ---
	spotEquity := spot.TotalUSD
	futuresEquity := futuresAcc.AccountEquity
	summary := OverviewSummary{
		TotalEquityUSD:      spotEquity + futuresEquity,
		SpotEquityUSD:       spotEquity,
		FuturesEquityUSD:    futuresEquity,
		UnrealizedPnLUSD:    totalUnrealized,
		AvailableForTrading: futuresAcc.AvailableBalance,
		TotalExposureUSD:    totalExposure,
	}

	// --- Risk ---
	marginRatio := futuresMarginRatio
	health := calcAccountHealth(marginRatio)
	marginUsed := futuresAcc.PositionMargin
	marginAvailable := futuresAcc.AvailableBalance
	canOpen := health != "critical" && marginAvailable > 0
	maxNew := marginAvailable * 0.5 // conservative: use at most 50% of available for new trades

	risk := RiskMetrics{
		MarginUsedUSD:         marginUsed,
		MarginAvailableUSD:    marginAvailable,
		MarginRatioPct:        marginRatio,
		TotalExposureUSD:      totalExposure,
		OpenPositions:         len(positions),
		AccountHealth:         health,
		CanOpenPosition:       canOpen,
		MaxNewPositionUSD:     maxNew,
		DailyLossUSD:          0, // not available without trade history; callers can overlay
		DailyLossLimitUSD:     20.0,
		DailyLossRemainingUSD: 20.0,
	}

	return &PortfolioOverview{
		Timestamp: time.Now(),
		Exchange:  "kucoin",
		Summary:   summary,
		Spot:      spot,
		Futures:   futures,
		Positions: positions,
		Risk:      risk,
		// BotContext is filled in by the higher-level client.GetPortfolioOverview
	}, nil
}

// fetchFuturesAccountOverview calls /api/v1/account-overview and returns typed data.
func (k *KuCoinRESTClient) fetchFuturesAccountOverview(ctx context.Context, currency string) (kuCoinFuturesAccount, error) {
	endpoint := "/api/v1/account-overview?currency=" + currency
	req, err := k.createAuthenticatedRequest(ctx, "GET", endpoint, nil)
	if err != nil {
		return kuCoinFuturesAccount{}, err
	}

	resp, err := k.httpClient.Do(req)
	if err != nil {
		return kuCoinFuturesAccount{}, err
	}
	defer resp.Body.Close()

	var result struct {
		Code string               `json:"code"`
		Data kuCoinFuturesAccount `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return kuCoinFuturesAccount{}, err
	}
	if result.Code != "200000" {
		return kuCoinFuturesAccount{}, MapKuCoinError(result.Code, "account-overview")
	}
	result.Data.Currency = currency
	return result.Data, nil
}

// fetchRawPositions calls /api/v1/positions and returns only open positions.
func (k *KuCoinRESTClient) fetchRawPositions(ctx context.Context) ([]kuCoinRawPosition, error) {
	endpoint := "/api/v1/positions"
	req, err := k.createAuthenticatedRequest(ctx, "GET", endpoint, nil)
	if err != nil {
		return nil, err
	}

	resp, err := k.doRequestWithRetry(ctx, req, 3)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result struct {
		Code string              `json:"code"`
		Msg  string              `json:"msg"`
		Data []kuCoinRawPosition `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	if result.Code != "200000" {
		return nil, MapKuCoinError(result.Code, result.Msg)
	}

	open := make([]kuCoinRawPosition, 0, len(result.Data))
	for _, p := range result.Data {
		if p.IsOpen || p.CurrentQty != 0 {
			open = append(open, p)
		}
	}
	return open, nil
}
