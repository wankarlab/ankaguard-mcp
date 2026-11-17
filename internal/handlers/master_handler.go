package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"

	"github.com/rantipay/trading-mcp/internal/killswitch"
	"github.com/rantipay/trading-mcp/internal/master"
	"github.com/rantipay/trading-mcp/internal/master/adapters"
)

// APIResponse is the standard JSON envelope for master/bots endpoints.
type APIResponse struct {
	Success bool        `json:"success"`
	Data    interface{} `json:"data,omitempty"`
	Error   string      `json:"error,omitempty"`
	Message string      `json:"message,omitempty"`
}

// FleetBotView is a backend-agnostic per-bot snapshot for the control panel.
// It decouples MasterHandler from the concrete fleet implementation (the AI
// orchestrator) so this package does not import internal/services. The wiring in
// main.go adapts the orchestrator's view type into this one.
type FleetBotView struct {
	Pair          string    `json:"pair"`
	Exchange      string    `json:"exchange"`
	Status        string    `json:"status"`
	PaperMode     bool      `json:"paper_mode"`
	OpenPositions int       `json:"active_positions"`
	CapitalUSD    float64   `json:"capital_usd"`
	TradesToday   int       `json:"trades_today"`
	TotalTrades   int       `json:"total_trades"`
	PnLToday      float64   `json:"pnl_today"`
	StartedAt     time.Time `json:"started_at"`
}

// FleetController is the canonical multi-pair control surface (the AI
// orchestrator). When wired, the /api/v1/bots* endpoints serve from it instead
// of the legacy scalping spawner. Kept minimal: list / kill / per-bot paper mode.
type FleetController interface {
	ListManagedBots() []FleetBotView
	KillBot(pair string, reason string) error
	SetBotPaperMode(pair string, enabled bool) error
}

// MasterHandler exposes /api/v1/master/* and /api/v1/bots* HTTP endpoints.
// masterAgent is optional — endpoints return 503 if it has not been wired in.
type MasterHandler struct {
	log         *logrus.Logger
	mu          sync.RWMutex
	masterAgent *master.MasterAgent
	spawner     *adapters.BotManagerSpawner
	fleet       FleetController
}

// NewMasterHandler constructs the handler. agent or spawner may be nil — they
// can be set later via SetMasterAgent / SetSpawner.
func NewMasterHandler(log *logrus.Logger, agent *master.MasterAgent, spawner *adapters.BotManagerSpawner) *MasterHandler {
	if log == nil {
		log = logrus.New()
	}
	return &MasterHandler{log: log, masterAgent: agent, spawner: spawner}
}

// SetMasterAgent allows lazy wiring of the agent after construction.
func (h *MasterHandler) SetMasterAgent(agent *master.MasterAgent) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.masterAgent = agent
}

// SetSpawner allows lazy wiring of the spawner adapter.
func (h *MasterHandler) SetSpawner(spawner *adapters.BotManagerSpawner) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.spawner = spawner
}

// SetFleet wires the canonical multi-pair fleet controller (the AI orchestrator).
// When set, the /api/v1/bots* endpoints serve from it in preference to the legacy
// scalping spawner.
func (h *MasterHandler) SetFleet(fleet FleetController) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.fleet = fleet
}

// RegisterRoutesGin mounts the master + bots endpoints on a gin router.
//
// authMiddleware (optional) is applied to every state-mutating control route
// (start/stop/tick/paper_mode/config writes and per-bot kill/paper_mode). These
// routes control a real-money bot, so they must not be reachable unauthenticated
// on the raw REST port. Pass the same JWT middleware the v1 group uses. Read-only
// GET endpoints stay public, consistent with the other public read-stats routes.
// When no middleware is supplied (e.g. unit tests) the routes register ungated.
func (h *MasterHandler) RegisterRoutesGin(r gin.IRouter, authMiddleware ...gin.HandlerFunc) {
	// Read-only endpoints — no auth (same class as /engine/stats, /xsec/status).
	r.GET("/api/v1/master/status", gin.WrapF(h.Status))
	r.GET("/api/v1/master/opportunities", gin.WrapF(h.Opportunities))
	r.GET("/api/v1/master/config", gin.WrapF(h.GetConfig))
	r.GET("/api/v1/master/paper_mode", gin.WrapF(h.GetPaperMode))
	r.GET("/api/v1/bots", gin.WrapF(h.ListBots))
	r.GET("/api/v1/bots/:pair", h.getBotGin)
	r.GET("/api/v1/bots/:pair/paper_mode", h.getBotPaperModeGin)

	// State-mutating control endpoints — require a valid JWT (authentication).
	// We gate on authentication only (no RequireLevel): RequireLevel(1) reads
	// user_level=0 for the single-operator token and would lock the operator out
	// (same rationale as the config-write group in the v1 router).
	writes := r.Group("")
	if len(authMiddleware) > 0 {
		writes.Use(authMiddleware...)
	}
	writes.POST("/api/v1/master/start", gin.WrapF(h.Start))
	writes.POST("/api/v1/master/stop", gin.WrapF(h.Stop))
	writes.POST("/api/v1/master/tick", gin.WrapF(h.ForceTick))
	writes.PUT("/api/v1/master/config", gin.WrapF(h.UpdateConfig))
	writes.POST("/api/v1/master/paper_mode", gin.WrapF(h.SetPaperMode))
	writes.POST("/api/v1/bots/:pair/kill", h.killBotGin)
	writes.POST("/api/v1/bots/:pair/paper_mode", h.setBotPaperModeGin)
}

// getBotPaperModeGin returns the paper mode status for a specific bot.
func (h *MasterHandler) getBotPaperModeGin(c *gin.Context) {
	pair := c.Param("pair")
	if fleet := h.getFleet(); fleet != nil {
		for _, b := range fleet.ListManagedBots() {
			if b.Pair == pair {
				mode := "live"
				if b.PaperMode {
					mode = "paper"
				}
				h.respondJSON(c.Writer, http.StatusOK, APIResponse{
					Success: true,
					Data: map[string]interface{}{
						"pair":       pair,
						"paper_mode": b.PaperMode,
						"mode":       mode,
					},
				})
				return
			}
		}
		h.respondError(c.Writer, http.StatusNotFound, "no active bot for pair "+pair)
		return
	}
	spawner := h.getSpawner()
	if spawner == nil {
		h.respondJSON(c.Writer, http.StatusServiceUnavailable, APIResponse{Success: false, Error: "bot manager not initialized"})
		return
	}
	bots := spawner.ListRawBots()
	for _, b := range bots {
		if b == nil {
			continue
		}
		matched := b.Pair == pair
		if !matched {
			for _, p := range b.Pairs {
				if p == pair {
					matched = true
					break
				}
			}
		}
		if matched {
			mode := "live"
			if b.PaperMode {
				mode = "paper"
			}
			h.respondJSON(c.Writer, http.StatusOK, APIResponse{
				Success: true,
				Data: map[string]interface{}{
					"pair":       pair,
					"bot_id":     b.BotID,
					"paper_mode": b.PaperMode,
					"mode":       mode,
				},
			})
			return
		}
	}
	h.respondError(c.Writer, http.StatusNotFound, "no active bot for pair "+pair)
}

// setBotPaperModeGin toggles paper mode on a specific running bot.
func (h *MasterHandler) setBotPaperModeGin(c *gin.Context) {
	pair := c.Param("pair")

	var body struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(c.Request.Body).Decode(&body); err != nil {
		h.respondError(c.Writer, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	if fleet := h.getFleet(); fleet != nil {
		if err := fleet.SetBotPaperMode(pair, body.Enabled); err != nil {
			h.log.WithError(err).WithField("pair", pair).Warn("master_handler: fleet set bot paper mode failed")
			if err.Error() == "bot not found" {
				h.respondError(c.Writer, http.StatusNotFound, err.Error())
				return
			}
			h.respondError(c.Writer, http.StatusInternalServerError, err.Error())
			return
		}
		mode := "live"
		msg := "LIVE MODE ENABLED for bot " + pair + " - will execute REAL orders on the exchange"
		if body.Enabled {
			mode = "paper"
			msg = "Paper mode enabled for bot " + pair + " - will simulate orders"
		}
		h.log.WithFields(logrus.Fields{"pair": pair, "paper_mode": body.Enabled}).Warn("master_handler: per-bot paper mode toggled via API (fleet)")
		h.respondJSON(c.Writer, http.StatusOK, APIResponse{
			Success: true,
			Message: msg,
			Data: map[string]interface{}{
				"pair":       pair,
				"paper_mode": body.Enabled,
				"mode":       mode,
				"warning":    "Change takes effect on next trade evaluation. Open positions are not affected.",
			},
		})
		return
	}

	spawner := h.getSpawner()
	if spawner == nil {
		h.respondJSON(c.Writer, http.StatusServiceUnavailable, APIResponse{Success: false, Error: "bot manager not initialized"})
		return
	}

	if err := spawner.SetBotPaperMode(pair, body.Enabled); err != nil {
		h.log.WithError(err).WithField("pair", pair).Warn("master_handler: set bot paper mode failed")
		if err.Error() == "bot not found" {
			h.respondError(c.Writer, http.StatusNotFound, err.Error())
			return
		}
		h.respondError(c.Writer, http.StatusInternalServerError, err.Error())
		return
	}

	mode := "live"
	msg := "LIVE MODE ENABLED for bot " + pair + " - will execute REAL orders on the exchange"
	if body.Enabled {
		mode = "paper"
		msg = "Paper mode enabled for bot " + pair + " - will simulate orders"
	}
	h.log.WithFields(logrus.Fields{"pair": pair, "paper_mode": body.Enabled}).Warn("master_handler: per-bot paper mode toggled via API")
	h.respondJSON(c.Writer, http.StatusOK, APIResponse{
		Success: true,
		Message: msg,
		Data: map[string]interface{}{
			"pair":       pair,
			"paper_mode": body.Enabled,
			"mode":       mode,
			"warning":    "Change takes effect on next trade evaluation. Open positions are not affected.",
		},
	})
}

// getBotGin is a gin-native adapter for GetBot that reads the pair from gin context.
func (h *MasterHandler) getBotGin(c *gin.Context) {
	pair := c.Param("pair")
	if fleet := h.getFleet(); fleet != nil {
		for _, b := range fleet.ListManagedBots() {
			if b.Pair == pair {
				h.respondJSON(c.Writer, http.StatusOK, APIResponse{Success: true, Data: b})
				return
			}
		}
		h.respondError(c.Writer, http.StatusNotFound, "no active bot for pair "+pair)
		return
	}
	spawner := h.getSpawner()
	if spawner == nil {
		h.respondJSON(c.Writer, http.StatusServiceUnavailable, APIResponse{Success: false, Error: "bot manager not initialized"})
		return
	}
	bots := spawner.ListRawBots()
	for _, b := range bots {
		if b == nil {
			continue
		}
		if b.Pair == pair {
			h.respondJSON(c.Writer, http.StatusOK, APIResponse{Success: true, Data: b})
			return
		}
		for _, p := range b.Pairs {
			if p == pair {
				h.respondJSON(c.Writer, http.StatusOK, APIResponse{Success: true, Data: b})
				return
			}
		}
	}
	h.respondError(c.Writer, http.StatusNotFound, "no active bot for pair "+pair)
}

// killBotGin is a gin-native adapter for KillBot that reads the pair from gin context.
func (h *MasterHandler) killBotGin(c *gin.Context) {
	pair := c.Param("pair")

	var body struct {
		Reason string `json:"reason"`
	}
	if c.Request.Body != nil {
		_ = json.NewDecoder(c.Request.Body).Decode(&body)
	}
	if body.Reason == "" {
		body.Reason = "manual kill via api"
	}

	if fleet := h.getFleet(); fleet != nil {
		if err := fleet.KillBot(pair, body.Reason); err != nil {
			h.log.WithError(err).WithField("pair", pair).Warn("master_handler: fleet kill bot failed")
			if err.Error() == "bot not found" {
				h.respondError(c.Writer, http.StatusNotFound, err.Error())
				return
			}
			h.respondError(c.Writer, http.StatusInternalServerError, err.Error())
			return
		}
		h.log.WithFields(logrus.Fields{"pair": pair, "reason": body.Reason}).Warn("master_handler: bot killed (fleet)")
		h.respondJSON(c.Writer, http.StatusOK, APIResponse{Success: true, Message: "bot killed"})
		return
	}

	spawner := h.getSpawner()
	if spawner == nil {
		h.respondJSON(c.Writer, http.StatusServiceUnavailable, APIResponse{Success: false, Error: "bot manager not initialized"})
		return
	}

	if err := spawner.KillBot(pair, body.Reason); err != nil {
		h.log.WithError(err).WithField("pair", pair).Warn("master_handler: kill bot failed")
		if err.Error() == "bot not found" {
			h.respondError(c.Writer, http.StatusNotFound, err.Error())
			return
		}
		h.respondError(c.Writer, http.StatusInternalServerError, err.Error())
		return
	}
	h.log.WithFields(logrus.Fields{"pair": pair, "reason": body.Reason}).Info("master_handler: bot killed")
	h.respondJSON(c.Writer, http.StatusOK, APIResponse{Success: true, Message: "bot killed"})
}

// Start launches the master agent loop.
func (h *MasterHandler) Start(w http.ResponseWriter, r *http.Request) {
	if killswitch.Ring0Active() {
		killswitch.WriteLocked(w)
		return
	}
	agent := h.getAgent()
	if agent == nil {
		h.respondError(w, http.StatusServiceUnavailable, "master agent not initialized")
		return
	}
	if agent.IsRunning() {
		h.respondError(w, http.StatusConflict, "master agent already running")
		return
	}

	if err := agent.Start(context.Background()); err != nil {
		h.log.WithError(err).Warn("master_handler: start failed")
		h.respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	h.log.Info("master_handler: master agent started")
	h.respondJSON(w, http.StatusOK, APIResponse{Success: true, Message: "master agent started"})
}

// Stop signals the master agent to stop.
func (h *MasterHandler) Stop(w http.ResponseWriter, r *http.Request) {
	agent := h.getAgent()
	if agent == nil {
		h.respondError(w, http.StatusServiceUnavailable, "master agent not initialized")
		return
	}
	if !agent.IsRunning() {
		h.respondError(w, http.StatusConflict, "master agent not running")
		return
	}
	if err := agent.Stop(); err != nil {
		h.log.WithError(err).Warn("master_handler: stop failed")
		h.respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	h.log.Info("master_handler: master agent stopped")
	h.respondJSON(w, http.StatusOK, APIResponse{Success: true, Message: "master agent stopped"})
}

// Status returns a snapshot of the master state.
func (h *MasterHandler) Status(w http.ResponseWriter, r *http.Request) {
	agent := h.getAgent()
	if agent == nil {
		h.respondError(w, http.StatusServiceUnavailable, "master agent not initialized")
		return
	}
	state := agent.GetState()
	// FleetActive tells the control panel that the canonical multi-pair fleet (AI
	// orchestrator) is wired, so it can render the fleet/paper controls. This is
	// distinct from MasterState.MultiPairEnabled, which reflects the legacy scalping
	// spawner (multi_pair.enabled, YAML). Embedding promotes all MasterState fields
	// to the top level alongside the added flag.
	resp := struct {
		*master.MasterState
		FleetActive bool `json:"FleetActive"`
	}{MasterState: state, FleetActive: h.getFleet() != nil}
	h.respondJSON(w, http.StatusOK, APIResponse{Success: true, Data: resp})
}

// ForceTick runs a single master cycle synchronously (with timeout).
func (h *MasterHandler) ForceTick(w http.ResponseWriter, r *http.Request) {
	agent := h.getAgent()
	if agent == nil {
		h.respondError(w, http.StatusServiceUnavailable, "master agent not initialized")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 120*time.Second)
	defer cancel()

	if err := agent.ForceTick(ctx); err != nil {
		h.log.WithError(err).Warn("master_handler: force tick failed")
		h.respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	h.respondJSON(w, http.StatusOK, APIResponse{Success: true, Message: "tick executed"})
}

// Opportunities returns the most recent scan opportunities.
func (h *MasterHandler) Opportunities(w http.ResponseWriter, r *http.Request) {
	agent := h.getAgent()
	if agent == nil {
		h.respondError(w, http.StatusServiceUnavailable, "master agent not initialized")
		return
	}
	state := agent.GetState()
	h.respondJSON(w, http.StatusOK, APIResponse{
		Success: true,
		Data: map[string]interface{}{
			"total":         len(state.Opportunities),
			"last_scan_at":  state.LastScanAt,
			"opportunities": state.Opportunities,
		},
	})
}

// ListBots returns active bots. Prefers the canonical AI-orchestrator fleet when
// wired; falls back to the legacy scalping spawner otherwise.
func (h *MasterHandler) ListBots(w http.ResponseWriter, r *http.Request) {
	if fleet := h.getFleet(); fleet != nil {
		bots := fleet.ListManagedBots()
		h.respondJSON(w, http.StatusOK, APIResponse{
			Success: true,
			Data: map[string]interface{}{
				"total": len(bots),
				"bots":  bots,
			},
		})
		return
	}
	spawner := h.getSpawner()
	if spawner == nil {
		h.respondError(w, http.StatusServiceUnavailable, "bot manager not initialized")
		return
	}
	bots := spawner.ListRawBots()
	h.respondJSON(w, http.StatusOK, APIResponse{
		Success: true,
		Data: map[string]interface{}{
			"total": len(bots),
			"bots":  bots,
		},
	})
}

// GetPaperMode returns the current paper-mode state of the bot spawner.
func (h *MasterHandler) GetPaperMode(w http.ResponseWriter, r *http.Request) {
	spawner := h.getSpawner()
	if spawner == nil {
		h.respondError(w, http.StatusServiceUnavailable, "bot manager not initialized")
		return
	}
	enabled := spawner.IsPaperMode()
	mode := "live"
	if enabled {
		mode = "paper"
	}
	h.respondJSON(w, http.StatusOK, APIResponse{
		Success: true,
		Data: map[string]interface{}{
			"paper_mode": enabled,
			"mode":       mode,
		},
	})
}

// SetPaperMode toggles paper mode on the bot spawner. Only affects newly spawned
// bots — existing bots keep their current mode until they restart.
func (h *MasterHandler) SetPaperMode(w http.ResponseWriter, r *http.Request) {
	spawner := h.getSpawner()
	if spawner == nil {
		h.respondError(w, http.StatusServiceUnavailable, "bot manager not initialized")
		return
	}
	var body struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		h.respondError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	spawner.SetPaperMode(body.Enabled)
	h.log.WithField("enabled", body.Enabled).Warn("master_handler: paper mode toggled via API")

	mode := "live"
	msg := "LIVE MODE ENABLED - new bots will execute REAL orders on the exchange"
	if body.Enabled {
		mode = "paper"
		msg = "Paper mode enabled - new bots will simulate orders"
	}
	h.respondJSON(w, http.StatusOK, APIResponse{
		Success: true,
		Message: msg,
		Data: map[string]interface{}{
			"paper_mode": body.Enabled,
			"mode":       mode,
			"warning":    "Only new bots are affected. Existing bots keep their current mode.",
		},
	})
}

// GetConfig returns the current scoring weights of the master agent scanner.
func (h *MasterHandler) GetConfig(w http.ResponseWriter, r *http.Request) {
	agent := h.getAgent()
	if agent == nil {
		h.respondError(w, http.StatusServiceUnavailable, "master agent not initialized")
		return
	}
	weights := agent.GetWeights()
	h.respondJSON(w, http.StatusOK, APIResponse{
		Success: true,
		Data:    map[string]interface{}{"scoring_weights": weights},
	})
}

// UpdateConfig updates the scoring weights of the master agent scanner at runtime.
func (h *MasterHandler) UpdateConfig(w http.ResponseWriter, r *http.Request) {
	agent := h.getAgent()
	if agent == nil {
		h.respondError(w, http.StatusServiceUnavailable, "master agent not initialized")
		return
	}
	var body struct {
		ScoringWeights *master.ScoringWeights `json:"scoring_weights"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.ScoringWeights == nil {
		h.respondError(w, http.StatusBadRequest, "invalid request body: scoring_weights required")
		return
	}
	agent.UpdateWeights(body.ScoringWeights)
	h.respondJSON(w, http.StatusOK, APIResponse{Success: true, Message: "config updated"})
}

func (h *MasterHandler) getAgent() *master.MasterAgent {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.masterAgent
}

func (h *MasterHandler) getSpawner() *adapters.BotManagerSpawner {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.spawner
}

func (h *MasterHandler) getFleet() FleetController {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.fleet
}

func (h *MasterHandler) respondJSON(w http.ResponseWriter, status int, payload interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func (h *MasterHandler) respondError(w http.ResponseWriter, status int, message string) {
	h.respondJSON(w, status, APIResponse{Success: false, Error: message})
}
