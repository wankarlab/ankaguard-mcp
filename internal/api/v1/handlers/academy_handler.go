// Package handlers — AnkaAI Console Academia endpoints.
//
// GET  /api/v1/academy/progress               — alumno→autor progression for the caller
// POST /api/v1/academy/courses/:id/complete   — mark a course step as completed
// GET  /api/v1/academy/cohorts/current        — aggregate cohort metrics for the current period
package handlers

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rantipay/trading-mcp/internal/api/v1/models"
	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

// AcademyProgress represents the alumno→autor journey for one user.
type AcademyProgress struct {
	UserID         string   `json:"user_id"`
	CurrentStep    int      `json:"current_step"` // 0=alumno, 1=aprendiz, 2=candidato, 3=autor
	CompletedSteps []string `json:"completed_steps"`
	NextStep       string   `json:"next_step"`
	CanGraduate    bool     `json:"can_graduate"` // true when ≥2 steps completed and not yet author
}

// CohortStats holds aggregate metrics for the current academy cohort.
type CohortStats struct {
	TotalAlumnos   int    `json:"total_alumnos"`
	TotalAutores   int    `json:"total_autores"`
	TotalCertified int    `json:"total_certified"` // certified specs
	Period         string `json:"period"`          // "2026-06"
}

// academyProgressRow is the raw DB row for academy_progress.
type academyProgressRow struct {
	UserID         string    `gorm:"column:user_id;primaryKey"`
	CurrentStep    int       `gorm:"column:current_step"`
	CompletedSteps string    `gorm:"column:completed_steps"` // JSON array stored as TEXT
	UpdatedAt      time.Time `gorm:"column:updated_at;autoUpdateTime"`
}

func (academyProgressRow) TableName() string { return "academy_progress" }

// stepLabel returns the human-readable label for a step index.
func stepLabel(step int) string {
	switch step {
	case 0:
		return "alumno"
	case 1:
		return "aprendiz"
	case 2:
		return "candidato"
	case 3:
		return "autor"
	default:
		return "desconocido"
	}
}

// nextStepLabel returns the label for the step above current, or "" when at the top.
func nextStepLabel(current int) string {
	if current >= 3 {
		return ""
	}
	return stepLabel(current + 1)
}

// AcademyHandler exposes academia progression endpoints.
type AcademyHandler struct {
	db  *gorm.DB
	log *logrus.Logger
}

// NewAcademyHandler constructs an AcademyHandler and ensures the academy_progress
// table exists (inline migration so no separate migration file is required).
func NewAcademyHandler(db *gorm.DB, log *logrus.Logger) *AcademyHandler {
	if log == nil {
		log = logrus.New()
	}
	if db != nil {
		if err := db.Exec(`CREATE TABLE IF NOT EXISTS academy_progress (
			user_id         TEXT PRIMARY KEY,
			current_step    INT  DEFAULT 0,
			completed_steps TEXT DEFAULT '[]',
			updated_at      DATETIME DEFAULT CURRENT_TIMESTAMP
		)`).Error; err != nil {
			log.WithError(err).Error("academy: failed to create academy_progress table")
		}
	}
	return &AcademyHandler{db: db, log: log}
}

// GetProgress handles GET /api/v1/academy/progress.
// Returns the caller's current step, completed steps, and graduation eligibility.
func (h *AcademyHandler) GetProgress(c *gin.Context) {
	if h.db == nil {
		c.JSON(http.StatusServiceUnavailable, models.NewErrorResponse("DB_UNAVAILABLE", "database not configured"))
		return
	}

	userID := userIDFromContext(c)

	var row academyProgressRow
	result := h.db.Where("user_id = ?", userID).First(&row)
	if result.Error != nil {
		if result.Error == gorm.ErrRecordNotFound {
			// First visit — return zero-state without persisting yet.
			c.JSON(http.StatusOK, models.NewSuccessResponse(AcademyProgress{
				UserID:         userID,
				CurrentStep:    0,
				CompletedSteps: []string{},
				NextStep:       nextStepLabel(0),
				CanGraduate:    false,
			}))
			return
		}
		h.log.WithError(result.Error).WithField("user_id", userID).Error("academy: GetProgress DB query failed")
		c.JSON(http.StatusInternalServerError, models.NewErrorResponse("INTERNAL_ERROR", result.Error.Error()))
		return
	}

	completed := parseCompletedSteps(row.CompletedSteps)
	progress := AcademyProgress{
		UserID:         userID,
		CurrentStep:    row.CurrentStep,
		CompletedSteps: completed,
		NextStep:       nextStepLabel(row.CurrentStep),
		CanGraduate:    len(completed) >= 2 && row.CurrentStep < 3,
	}
	c.JSON(http.StatusOK, models.NewSuccessResponse(progress))
}

// CompleteStep handles POST /api/v1/academy/courses/:id/complete.
// Marks the given course ID as completed for the caller.
// Returns 403 if the course was already completed.
func (h *AcademyHandler) CompleteStep(c *gin.Context) {
	if h.db == nil {
		c.JSON(http.StatusServiceUnavailable, models.NewErrorResponse("DB_UNAVAILABLE", "database not configured"))
		return
	}

	courseID := c.Param("id")
	if courseID == "" {
		c.JSON(http.StatusBadRequest, models.NewErrorResponse("VALIDATION_FAILED", "course id is required"))
		return
	}

	userID := userIDFromContext(c)

	var row academyProgressRow
	result := h.db.Where("user_id = ?", userID).First(&row)
	if result.Error != nil && result.Error != gorm.ErrRecordNotFound {
		h.log.WithError(result.Error).WithField("user_id", userID).Error("academy: CompleteStep DB read failed")
		c.JSON(http.StatusInternalServerError, models.NewErrorResponse("INTERNAL_ERROR", result.Error.Error()))
		return
	}

	completed := parseCompletedSteps(row.CompletedSteps)

	// Guard: already completed.
	for _, s := range completed {
		if s == courseID {
			c.JSON(http.StatusForbidden, models.NewErrorResponse("ALREADY_COMPLETED", "course already marked as completed"))
			return
		}
	}

	completed = append(completed, courseID)
	completedJSON := marshalCompletedSteps(completed)

	// Advance step when thresholds are reached (every 2 courses, up to step 3).
	newStep := row.CurrentStep
	if len(completed) >= 2 && newStep < 1 {
		newStep = 1
	}
	if len(completed) >= 4 && newStep < 2 {
		newStep = 2
	}
	if len(completed) >= 6 && newStep < 3 {
		newStep = 3
	}

	if result.Error == gorm.ErrRecordNotFound {
		// Insert new row.
		insertErr := h.db.Exec(
			`INSERT INTO academy_progress (user_id, current_step, completed_steps, updated_at) VALUES (?, ?, ?, CURRENT_TIMESTAMP)`,
			userID, newStep, completedJSON,
		).Error
		if insertErr != nil {
			h.log.WithError(insertErr).WithField("user_id", userID).Error("academy: CompleteStep insert failed")
			c.JSON(http.StatusInternalServerError, models.NewErrorResponse("INTERNAL_ERROR", insertErr.Error()))
			return
		}
	} else {
		// Update existing row.
		updateErr := h.db.Exec(
			`UPDATE academy_progress SET current_step = ?, completed_steps = ?, updated_at = CURRENT_TIMESTAMP WHERE user_id = ?`,
			newStep, completedJSON, userID,
		).Error
		if updateErr != nil {
			h.log.WithError(updateErr).WithField("user_id", userID).Error("academy: CompleteStep update failed")
			c.JSON(http.StatusInternalServerError, models.NewErrorResponse("INTERNAL_ERROR", updateErr.Error()))
			return
		}
	}

	progress := AcademyProgress{
		UserID:         userID,
		CurrentStep:    newStep,
		CompletedSteps: completed,
		NextStep:       nextStepLabel(newStep),
		CanGraduate:    len(completed) >= 2 && newStep < 3,
	}
	c.JSON(http.StatusOK, models.NewSuccessResponse(progress))
}

// GetCohorts handles GET /api/v1/academy/cohorts/current.
// Returns aggregate counts for all users in the academy_progress table.
func (h *AcademyHandler) GetCohorts(c *gin.Context) {
	if h.db == nil {
		c.JSON(http.StatusServiceUnavailable, models.NewErrorResponse("DB_UNAVAILABLE", "database not configured"))
		return
	}

	var totalAlumnos, totalAutores int64

	if err := h.db.Model(&academyProgressRow{}).Count(&totalAlumnos).Error; err != nil {
		h.log.WithError(err).Error("academy: GetCohorts count all failed")
		c.JSON(http.StatusInternalServerError, models.NewErrorResponse("INTERNAL_ERROR", err.Error()))
		return
	}

	if err := h.db.Model(&academyProgressRow{}).Where("current_step = 3").Count(&totalAutores).Error; err != nil {
		h.log.WithError(err).Error("academy: GetCohorts count autores failed")
		c.JSON(http.StatusInternalServerError, models.NewErrorResponse("INTERNAL_ERROR", err.Error()))
		return
	}

	// Certified = users at candidato or above (step ≥ 2) who have ≥2 completed steps.
	// Approximated as users with current_step >= 2.
	var totalCertified int64
	if err := h.db.Model(&academyProgressRow{}).Where("current_step >= 2").Count(&totalCertified).Error; err != nil {
		h.log.WithError(err).Error("academy: GetCohorts count certified failed")
		c.JSON(http.StatusInternalServerError, models.NewErrorResponse("INTERNAL_ERROR", err.Error()))
		return
	}

	stats := CohortStats{
		TotalAlumnos:   int(totalAlumnos),
		TotalAutores:   int(totalAutores),
		TotalCertified: int(totalCertified),
		Period:         time.Now().UTC().Format("2006-01"),
	}
	c.JSON(http.StatusOK, models.NewSuccessResponse(stats))
}

// parseCompletedSteps deserializes the JSON TEXT column into a string slice.
// Returns an empty slice on any parse error.
func parseCompletedSteps(raw string) []string {
	if raw == "" || raw == "null" {
		return []string{}
	}
	var out []string
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return []string{}
	}
	return out
}

// marshalCompletedSteps serializes a string slice to a JSON TEXT value.
func marshalCompletedSteps(steps []string) string {
	if steps == nil {
		steps = []string{}
	}
	b, _ := json.Marshal(steps)
	return string(b)
}

// ROUTE_MANIFEST: GET /api/v1/academy/progress -> academyHandler.GetProgress | POST /api/v1/academy/courses/:id/complete -> academyHandler.CompleteStep | GET /api/v1/academy/cohorts/current -> academyHandler.GetCohorts
