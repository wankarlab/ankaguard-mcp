package handlers

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/rantipay/trading-mcp/internal/storage"
	"github.com/sirupsen/logrus"
)

// TuningHandler handles bot tuning parameter endpoints.
type TuningHandler struct {
	store *storage.TuningStore
	log   *logrus.Logger
}

// NewTuningHandler creates a new TuningHandler.
func NewTuningHandler(store *storage.TuningStore, log *logrus.Logger) *TuningHandler {
	return &TuningHandler{store: store, log: log}
}

// tuningParamResponse is the JSON representation of a single tuning parameter.
type tuningParamResponse struct {
	Value       float64   `json:"value"`
	Description string    `json:"description"`
	UpdatedAt   time.Time `json:"updated_at"`
	UpdatedBy   string    `json:"updated_by"`
}

// GetAll handles GET /api/v1/bot/tuning
// Returns all tuning parameters with their metadata.
func (h *TuningHandler) GetAll(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		h.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	all := h.store.GetAll()
	params := make(map[string]tuningParamResponse, len(all))
	for k, p := range all {
		params[k] = tuningParamResponse{
			Value:       p.Value,
			Description: p.Description,
			UpdatedAt:   p.UpdatedAt,
			UpdatedBy:   p.UpdatedBy,
		}
	}

	h.writeJSON(w, http.StatusOK, map[string]interface{}{
		"params": params,
	})
}

// Update handles PATCH /api/v1/bot/tuning
// Accepts a body of the form:
//
//	{ "params": { "key": <float64>, ... }, "updated_by": "<who>" }
//
// Returns the list of updated keys and any per-key errors.
func (h *TuningHandler) Update(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPatch {
		h.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var body struct {
		Params    map[string]float64 `json:"params"`
		UpdatedBy string             `json:"updated_by"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(body.Params) == 0 {
		h.writeError(w, http.StatusBadRequest, "params must not be empty")
		return
	}

	updatedBy := body.UpdatedBy
	if updatedBy == "" {
		updatedBy = "api"
	}

	// Fetch current metadata to preserve descriptions.
	all := h.store.GetAll()

	updated := make([]string, 0, len(body.Params))
	errors := make(map[string]string)

	for key, value := range body.Params {
		description := ""
		if existing, ok := all[key]; ok {
			description = existing.Description
		}

		if err := h.store.Set(key, description, updatedBy, value); err != nil {
			h.log.WithError(err).WithField("key", key).Error("Failed to set tuning param")
			errors[key] = err.Error()
			continue
		}
		updated = append(updated, key)
	}

	status := http.StatusOK
	if len(updated) == 0 && len(errors) > 0 {
		status = http.StatusInternalServerError
	}

	h.writeJSON(w, status, map[string]interface{}{
		"updated": updated,
		"errors":  errors,
	})
}

// GetOne handles GET /api/v1/bot/tuning/{key}
// Returns a single tuning parameter by its key.
func (h *TuningHandler) GetOne(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		h.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	// Extract key from path: /api/v1/bot/tuning/{key}
	key := strings.TrimPrefix(r.URL.Path, "/api/v1/bot/tuning/")
	key = strings.TrimSpace(key)
	if key == "" {
		h.writeError(w, http.StatusBadRequest, "key is required")
		return
	}

	all := h.store.GetAll()
	p, ok := all[key]
	if !ok {
		h.writeError(w, http.StatusNotFound, "param not found")
		return
	}

	h.writeJSON(w, http.StatusOK, map[string]interface{}{
		"key": key,
		"param": tuningParamResponse{
			Value:       p.Value,
			Description: p.Description,
			UpdatedAt:   p.UpdatedAt,
			UpdatedBy:   p.UpdatedBy,
		},
	})
}

// --- private helpers ---------------------------------------------------------

func (h *TuningHandler) writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		h.log.WithError(err).Error("Failed to encode tuning response")
	}
}

func (h *TuningHandler) writeError(w http.ResponseWriter, status int, message string) {
	h.writeJSON(w, status, map[string]string{"error": message})
}
