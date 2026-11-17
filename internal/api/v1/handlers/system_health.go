package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"

	"github.com/rantipay/trading-mcp/internal/api/v1/models"
)

// healthProbeTimeout bounds every individual probe (spec: <= 2s).
const healthProbeTimeout = 2 * time.Second

// Component states and global statuses.
const (
	healthStateOK   = "ok"
	healthStateDown = "down"
)

// SystemHealthConfig holds the injectable probe targets. Empty URL means the
// component is not configured and is reported down with "not configured".
type SystemHealthConfig struct {
	// MCPHealthURL is the MCP server health endpoint (gct_connected,
	// engine_real_money), e.g. http://127.0.0.1:10600/health. Critical.
	MCPHealthURL string
	// MCPHealthFunc reads the same data in-process. When set it takes
	// precedence over MCPHealthURL: the public /health no longer carries these
	// fields (task 6.9), so the aggregator must not depend on it.
	MCPHealthFunc func() (gctConnected, engineRealMoney bool)
	// MLHealthURL is the full ML server health URL. Non-critical.
	MLHealthURL string
	// PushHealthURL is the full notificationpush health URL. Non-critical.
	PushHealthURL string
	// ConfigDBPing checks the dynamic-config Postgres connection. nil means
	// not configured. Critical.
	ConfigDBPing func(ctx context.Context) error
	// OmitAuxiliary drops the ML server and notificationpush components: the
	// product edition ships neither, so probing them would always report a
	// false "degraded".
	OmitAuxiliary bool
	// Timeout overrides the per-probe timeout (<= healthProbeTimeout).
	Timeout time.Duration
}

// SystemHealthHandler aggregates the health of the components trading depends on.
type SystemHealthHandler struct {
	cfg    SystemHealthConfig
	client *http.Client
	log    *logrus.Logger
}

// NewSystemHealthHandler builds the aggregator. The timeout is clamped to 2s.
func NewSystemHealthHandler(cfg SystemHealthConfig, log *logrus.Logger) *SystemHealthHandler {
	if cfg.Timeout <= 0 || cfg.Timeout > healthProbeTimeout {
		cfg.Timeout = healthProbeTimeout
	}
	if log == nil {
		log = logrus.New()
	}
	return &SystemHealthHandler{cfg: cfg, client: &http.Client{}, log: log}
}

// ComponentHealth is the per-component result.
type ComponentHealth struct {
	Name      string `json:"name"`
	Critical  bool   `json:"critical"`
	Status    string `json:"status"` // ok | down
	LatencyMS int64  `json:"latency_ms"`
	Message   string `json:"message"`
}

// SystemHealthResponse is the endpoint payload.
type SystemHealthResponse struct {
	Status          string            `json:"status"` // ok | degraded | down
	EngineRealMoney *bool             `json:"engine_real_money"`
	Components      []ComponentHealth `json:"components"`
	CheckedAt       time.Time         `json:"checked_at"`
}

// GetSystemHealth handles GET /api/v1/system/health (strict JWT group).
func (h *SystemHealthHandler) GetSystemHealth(c *gin.Context) {
	var (
		wg        sync.WaitGroup
		gct, mode ComponentHealth
		cfgDB     ComponentHealth
		ml, push  ComponentHealth
		realMoney *bool
	)
	run := func(fn func()) {
		wg.Add(1)
		go func() { defer wg.Done(); fn() }()
	}
	// The MCP probe feeds two components (GCT connection and real-money mode).
	run(func() { gct, mode, realMoney = h.probeMCP(c.Request.Context()) })
	run(func() { cfgDB = h.probeConfigDB(c.Request.Context()) })
	if !h.cfg.OmitAuxiliary {
		run(func() { ml = h.probeHTTP(c.Request.Context(), "ml_server", false, h.cfg.MLHealthURL) })
		run(func() { push = h.probeHTTP(c.Request.Context(), "notificationpush", false, h.cfg.PushHealthURL) })
	}
	wg.Wait()

	comps := []ComponentHealth{gct, mode, cfgDB}
	if !h.cfg.OmitAuxiliary {
		comps = append(comps, ml, push)
	}
	global := healthStateOK
	for _, comp := range comps {
		if comp.Status == healthStateOK {
			continue
		}
		if comp.Critical {
			global = healthStateDown
			break
		}
		global = "degraded"
	}
	c.JSON(http.StatusOK, models.NewSuccessResponse(SystemHealthResponse{
		Status:          global,
		EngineRealMoney: realMoney,
		Components:      comps,
		CheckedAt:       time.Now().UTC(),
	}))
}

// probeMCP reads the MCP /health payload once and derives two components.
func (h *SystemHealthHandler) probeMCP(parent context.Context) (gct, mode ComponentHealth, realMoney *bool) {
	gct = ComponentHealth{Name: "gocryptotrader", Critical: true, Status: healthStateDown}
	mode = ComponentHealth{Name: "engine_mode", Critical: false, Status: healthStateDown}
	if h.cfg.MCPHealthFunc != nil {
		connected, rm := h.cfg.MCPHealthFunc()
		realMoney = &rm
		mode.Status = healthStateOK
		if rm {
			mode.Message = "real money"
		} else {
			mode.Message = "sandbox"
		}
		if connected {
			gct.Status, gct.Message = healthStateOK, "connected"
		} else {
			gct.Message = "gct disconnected"
		}
		return
	}
	if h.cfg.MCPHealthURL == "" {
		gct.Message, mode.Message = "not configured", "not configured"
		return
	}
	start := time.Now()
	ctx, cancel := context.WithTimeout(parent, h.cfg.Timeout)
	defer cancel()
	var body struct {
		GCTConnected bool `json:"gct_connected"`
		Sandbox      struct {
			EngineRealMoney bool `json:"engine_real_money"`
		} `json:"sandbox"`
	}
	err := h.getJSON(ctx, h.cfg.MCPHealthURL, &body)
	lat := time.Since(start).Milliseconds()
	gct.LatencyMS, mode.LatencyMS = lat, lat
	if err != nil {
		// MCP is the same process family: unreachable means the trading engine is down.
		gct.Message = "mcp unreachable: " + probeReason(err)
		mode.Message = gct.Message
		return
	}
	rm := body.Sandbox.EngineRealMoney
	realMoney = &rm
	mode.Status = healthStateOK
	if rm {
		mode.Message = "real money"
	} else {
		mode.Message = "sandbox"
	}
	if body.GCTConnected {
		gct.Status, gct.Message = healthStateOK, "connected"
	} else {
		gct.Message = "gct disconnected"
	}
	return
}

func (h *SystemHealthHandler) probeConfigDB(parent context.Context) ComponentHealth {
	res := ComponentHealth{Name: "config_db", Critical: true, Status: healthStateDown}
	if h.cfg.ConfigDBPing == nil {
		res.Message = "not configured"
		return res
	}
	start := time.Now()
	ctx, cancel := context.WithTimeout(parent, h.cfg.Timeout)
	defer cancel()
	err := h.cfg.ConfigDBPing(ctx)
	res.LatencyMS = time.Since(start).Milliseconds()
	if err != nil {
		res.Message = probeReason(err)
		return res
	}
	res.Status, res.Message = healthStateOK, "reachable"
	return res
}

func (h *SystemHealthHandler) probeHTTP(parent context.Context, name string, critical bool, url string) ComponentHealth {
	res := ComponentHealth{Name: name, Critical: critical, Status: healthStateDown}
	if url == "" {
		res.Message = "not configured"
		return res
	}
	start := time.Now()
	ctx, cancel := context.WithTimeout(parent, h.cfg.Timeout)
	defer cancel()
	err := h.getJSON(ctx, url, nil)
	res.LatencyMS = time.Since(start).Milliseconds()
	if err != nil {
		res.Message = probeReason(err)
		return res
	}
	res.Status, res.Message = healthStateOK, "reachable"
	return res
}

// getJSON GETs url, requires 2xx and decodes the body into out when non-nil.
func (h *SystemHealthHandler) getJSON(ctx context.Context, url string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := h.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("http status %d", resp.StatusCode)
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// probeReason turns a probe error into a short, non-sensitive message.
func probeReason(err error) string {
	if errors.Is(err, context.DeadlineExceeded) || strings.Contains(err.Error(), "deadline exceeded") {
		return "timeout"
	}
	if errors.Is(err, context.Canceled) {
		return "canceled"
	}
	if strings.HasPrefix(err.Error(), "http status") {
		return err.Error()
	}
	return "unreachable"
}
