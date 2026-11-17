package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"runtime/debug"
	"slices"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"

	"github.com/rantipay/trading-mcp/internal/config/dynamic"
	"github.com/rantipay/trading-mcp/internal/edition"
	"github.com/rantipay/trading-mcp/internal/gct"
	"github.com/rantipay/trading-mcp/internal/license"
	"github.com/rantipay/trading-mcp/internal/localauth"
	"github.com/rantipay/trading-mcp/internal/mcp-trading/config"
	"github.com/rantipay/trading-mcp/internal/mcp-trading/gctclient"
	"github.com/rantipay/trading-mcp/internal/mcp-trading/server"
	"github.com/rantipay/trading-mcp/internal/paper"
	"github.com/rantipay/trading-mcp/internal/productapi"
	"github.com/rantipay/trading-mcp/internal/risk"
	buildversion "github.com/rantipay/trading-mcp/internal/version"
)

// dashboardCORSOrigins is the list of origins allowed by the gin dashboard
// (:8081) and, since task 6.14, by the gateway (:9090) as well. The product
// edition only allows the local console (localhost / 127.0.0.1); the LAN
// address of this deployment stays in the standard build only.
//
// The product also accepts the origins listed in CONSOLE_ORIGINS (comma
// separated): the browser sends Origin on the console's same-origin POSTs and
// the Next rewrite forwards it, so a console published on another port
// (CONSOLE_PORT) or host would otherwise get 403 on login.
var dashboardCORSOrigins = corsOriginsFor(edition.IsProduct(), os.Getenv("CONSOLE_ORIGINS"))

// corsOriginsFor returns the dashboard CORS origins of an edition. extra is
// only honoured by the product; the standard build keeps its fixed list.
func corsOriginsFor(product bool, extra string) []string {
	if !product {
		return []string{"http://localhost:3002", "http://127.0.0.1:3002"}
	}
	origins := []string{"http://localhost:3002", "http://127.0.0.1:3002"}
	for _, o := range strings.Split(extra, ",") {
		o = strings.TrimRight(strings.TrimSpace(o), "/")
		if o == "" || o == "*" || !(strings.HasPrefix(o, "http://") || strings.HasPrefix(o, "https://")) {
			continue
		}
		if !slices.Contains(origins, o) {
			origins = append(origins, o)
		}
	}
	return origins
}

// runOperatorSubcommand implements `trading-mcp-server operator <set-password|rotate-secret>`.
// It runs before anything is connected: it only touches the auth data directory.
func runOperatorSubcommand(configPath string, args []string) error {
	st := config.LoadAuthSettings(configPath)
	return localauth.RunOperatorCommand(st.DataDir, args, os.Stdin, os.Stdout, os.Stderr)
}

// paperParams reads the paper.* dynamic parameters on every call (hot reload),
// falling back to the broker defaults when the store is not available.
func paperParams() paper.Params {
	p := paper.DefaultParams()
	snap := dynamic.SnapshotFromContext(context.Background())
	if snap == nil {
		return p
	}
	if v, ok := snap.FloatOk("paper.initial_balance_usd"); ok && v > 0 {
		p.InitialBalanceUSD = v
	}
	if v, ok := snap.FloatOk("paper.slippage_pct"); ok && v >= 0 {
		p.SlippagePct = v
	}
	if v, ok := snap.FloatOk("paper.taker_fee_pct"); ok && v >= 0 {
		p.TakerFeePct = v
	}
	return p
}

// liveEnabledFunc resolves trading.live_enabled on every call: the dynamic
// value when set, else the YAML fallback, else true (this deployment's
// registry default, so nothing changes unless it is turned off explicitly).
func liveEnabledFunc(cfg *config.Config) func() bool {
	return func() bool {
		if snap := dynamic.SnapshotFromContext(context.Background()); snap != nil {
			if v, ok := snap.BoolOk("trading.live_enabled"); ok {
				return v
			}
		}
		if cfg != nil && cfg.Trading.LiveEnabled != nil {
			return *cfg.Trading.LiveEnabled
		}
		return true
	}
}

// buildPaperBroker builds the paper broker on the existing SQLite database and
// wires it into the client selector and the server dispatcher.
//
//   - Inner is the real GCT client: only market data is ever forwarded to it.
//   - Sentinel is the same risk gate the real orders pass through.
//   - ContractMultiplier reads the public KuCoin contract specification.
func buildPaperBroker(
	ctx context.Context,
	log *logrus.Logger,
	db *sql.DB,
	client *gctclient.Client,
	inner gct.GCTClient,
	sentinel risk.RiskSentinel,
	mcpServer *server.MCPServer,
) (*paper.Client, error) {
	if db == nil {
		return nil, fmt.Errorf("sqlite database is not available")
	}
	kucoin := gct.NewKuCoinRESTClient() // public endpoint, no credentials
	pc, err := paper.NewClient(paper.Config{
		DB:       db,
		Inner:    inner,
		Sentinel: sentinel,
		Params:   paperParams,
		ContractMultiplier: func(ctx context.Context, symbol string) (float64, error) {
			m, err := kucoin.CachedContractMultiplier(ctx, symbol)
			if err != nil {
				return 0, err
			}
			return m, nil
		},
	})
	if err != nil {
		return nil, err
	}
	client.SetPaperClient(pc)
	mcpServer.SetPaperBroker(pc)
	go pc.Broker().Run(ctx)
	log.Info("Paper broker ready (paper-profile tokens are served by the simulator, never by the exchange)")
	return pc, nil
}

// Error codes of the client-level live order gate (same codes as the /mcp
// dispatcher gates, so every surface reports the same reason).
const (
	liveGateLicenseRequired = "LICENSE_REQUIRED"
	liveGateLiveDisabled    = "LIVE_TRADING_DISABLED"
)

// liveOrderGate builds the gate the product edition installs on the real GCT
// client (gct client SetLiveOrderGate). It covers every real order path, not
// only /mcp: REST POST /api/v1/orders and /orders/direct/futures, the HFT
// wrapper and the direct-REST tools all end in the same client. Reductions
// and closes never reach the gate (the client exempts them).
//
// allowed = Pro build && license allows live && trading.live_enabled.
func liveOrderGate(isPro bool, allowsLive, liveEnabled func() bool) func() error {
	return func() error {
		if !isPro || allowsLive == nil || !allowsLive() {
			return fmt.Errorf("%s: a valid Pro license is required for real-money orders that increase risk; reducing and closing positions stays available", liveGateLicenseRequired)
		}
		if liveEnabled != nil && !liveEnabled() {
			return fmt.Errorf("%s: real-money trading is not enabled (trading.live_enabled=false)", liveGateLiveDisabled)
		}
		return nil
	}
}

// anyRealMoney reports whether an enabled exchange is configured with sandbox=false.
func anyRealMoney(cfg *config.Config) bool {
	for _, ex := range cfg.GetEnabledExchanges() {
		if !ex.Sandbox {
			return true
		}
	}
	return false
}

// jwtAuthGuard implements task 6.13. It returns fatal=true when JWT auth is
// disabled while real money can be moved, and a warning message when it is
// disabled in any other case.
func jwtAuthGuard(jwtEnabled, realMoney, liveEnabled bool) (fatal bool, msg string) {
	if jwtEnabled {
		return false, ""
	}
	if realMoney && liveEnabled {
		return true, "refusing to start with ENABLE_JWT_AUTH=false: an exchange is enabled with sandbox=false and trading.live_enabled=true, so unauthenticated callers could move real money"
	}
	return false, "JWT authentication is DISABLED (ENABLE_JWT_AUTH=false): every HTTP surface is open; use this only for local development"
}

// publicHealthInfo feeds the public GET /health with version, commit and edition.
func publicHealthInfo(lic productapi.LicenseSource) func() (string, string, string) {
	return func() (string, string, string) {
		commit := buildversion.GitCommit
		if commit == "" || commit == "unknown" {
			commit = vcsRevision()
		}
		edition := "community"
		if lic != nil {
			if st := lic.Status(context.Background()); st.Edition != "" {
				edition = st.Edition
			}
		}
		return buildversion.Version, commit, edition
	}
}

func vcsRevision() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	for _, s := range info.Settings {
		if s.Key == "vcs.revision" && s.Value != "" {
			if len(s.Value) > 12 {
				return s.Value[:12]
			}
			return s.Value
		}
	}
	return "unknown"
}

// warnIfExposed logs one warning naming the port when bind is not loopback.
func warnIfExposed(log *logrus.Logger, label string, port string, bind string) {
	if config.IsLocalBind(bind) {
		return
	}
	shown := bind
	if shown == "" {
		shown = "0.0.0.0"
	}
	log.WithFields(logrus.Fields{"listener": label, "port": port, "bind": shown}).
		Warn("port is exposed beyond this machine (bind address is not loopback); set server.bind_address: 127.0.0.1 to restrict it")
}

// httpListenAddr joins bind and port; an empty bind keeps all interfaces.
func httpListenAddr(bind, port string) string {
	if bind == "" {
		bind = "0.0.0.0"
	}
	return bind + ":" + port
}

// licenseStatusSource adapts the license manager to productapi.LicenseSource.
type licenseStatusSource struct{ m *license.Manager }

func (l licenseStatusSource) Status(context.Context) productapi.LicenseStatus {
	st := l.m.Status()
	out := productapi.LicenseStatus{
		Edition:            st.Edition,
		State:              st.State,
		Holder:             st.Holder,
		LicenseID:          st.LicenseID,
		ExpiresAt:          st.ExpiresAt,
		GraceDaysRemaining: st.GraceDaysRemaining,
		Warning:            st.Warning,
	}
	if st.ExpiresAt != nil {
		d := st.DaysRemaining
		out.DaysRemaining = &d
	}
	return out
}

// operatorOnly guards the license endpoints: a valid token is required (401)
// and it must be an operator session (403), as in docs/product/api-contract.md.
func operatorOnly(standalone bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		if _, ok := c.Get("user_id"); !ok {
			c.AbortWithStatusJSON(401, gin.H{"error": "authentication required", "code": "UNAUTHORIZED"})
			return
		}
		if !productapi.IsOperator(c, standalone) {
			c.AbortWithStatusJSON(403, gin.H{"error": "only an operator session can manage the license", "code": "OPERATOR_REQUIRED"})
			return
		}
		c.Next()
	}
}
