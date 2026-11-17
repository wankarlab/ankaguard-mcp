package storage

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/rantipay/trading-mcp/internal/ai"
	"github.com/sirupsen/logrus"
	"gopkg.in/yaml.v3"
)

// TradingStyleRepository handles persistence of trading styles
type TradingStyleRepository struct {
	db  *sql.DB
	log *logrus.Logger
}

// NewTradingStyleRepository creates a new trading style repository
func NewTradingStyleRepository(db *sql.DB, log *logrus.Logger) *TradingStyleRepository {
	return &TradingStyleRepository{
		db:  db,
		log: log,
	}
}

// Save persists a trading style to the database
func (r *TradingStyleRepository) Save(style *ai.Strategy) error {
	// Serialize to YAML
	yamlData, err := yaml.Marshal(style)
	if err != nil {
		return fmt.Errorf("failed to marshal style to YAML: %w", err)
	}

	// Get primary timeframe (first in list)
	primaryTF := ""
	if len(style.Timeframes) > 0 {
		primaryTF = style.Timeframes[0].Interval
	}

	// Get secondary timeframes
	var secondaryTFs []string
	if len(style.Timeframes) > 1 {
		for i := 1; i < len(style.Timeframes); i++ {
			secondaryTFs = append(secondaryTFs, style.Timeframes[i].Interval)
		}
	}
	secondaryTFJSON, _ := json.Marshal(secondaryTFs)

	// Serialize required timeframes
	requiredTFJSON, _ := json.Marshal(style.RequiredTimeframes)

	// Check if exists
	var exists bool
	err = r.db.QueryRow("SELECT EXISTS(SELECT 1 FROM trading_styles WHERE name = ?)", style.Name).Scan(&exists)
	if err != nil {
		return fmt.Errorf("failed to check if style exists: %w", err)
	}

	now := time.Now()

	if exists {
		// Update existing
		_, err = r.db.Exec(`
			UPDATE trading_styles 
			SET type = ?, config_yaml = ?, primary_timeframe = ?, 
			    secondary_timeframes = ?, required_timeframes = ?, updated_at = ?
			WHERE name = ?
		`, style.Type, string(yamlData), primaryTF,
			string(secondaryTFJSON), string(requiredTFJSON), now, style.Name)

		if err != nil {
			return fmt.Errorf("failed to update style: %w", err)
		}

		r.log.WithField("name", style.Name).Info("Trading style updated")
	} else {
		// Insert new
		_, err = r.db.Exec(`
			INSERT INTO trading_styles 
			(name, type, config_yaml, primary_timeframe, secondary_timeframes, required_timeframes, created_at, updated_at, active)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, style.Name, style.Type, string(yamlData), primaryTF,
			string(secondaryTFJSON), string(requiredTFJSON), now, now, true)

		if err != nil {
			return fmt.Errorf("failed to insert style: %w", err)
		}

		r.log.WithField("name", style.Name).Info("Trading style created")
	}

	return nil
}

// Load retrieves a trading style by name
func (r *TradingStyleRepository) Load(name string) (*ai.Strategy, error) {
	var record TradingStyleRecord

	err := r.db.QueryRow(`
		SELECT id, name, type, config_yaml, primary_timeframe, secondary_timeframes, 
		       required_timeframes, created_at, updated_at, active
		FROM trading_styles
		WHERE name = ? AND active = 1
	`, name).Scan(
		&record.ID, &record.Name, &record.Type, &record.ConfigYAML,
		&record.PrimaryTimeframe, &record.SecondaryTimeframes, &record.RequiredTimeframes,
		&record.CreatedAt, &record.UpdatedAt, &record.Active,
	)

	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("trading style not found: %s", name)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to load style: %w", err)
	}

	// Deserialize YAML
	var style ai.Strategy
	if err := yaml.Unmarshal([]byte(record.ConfigYAML), &style); err != nil {
		return nil, fmt.Errorf("failed to unmarshal YAML: %w", err)
	}

	return &style, nil
}

// List returns all active trading styles
func (r *TradingStyleRepository) List() ([]*ai.Strategy, error) {
	rows, err := r.db.Query(`
		SELECT id, name, type, config_yaml, primary_timeframe, secondary_timeframes, 
		       required_timeframes, created_at, updated_at, active
		FROM trading_styles
		WHERE active = 1
		ORDER BY name
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to query styles: %w", err)
	}
	defer rows.Close()

	var styles []*ai.Strategy

	for rows.Next() {
		var record TradingStyleRecord
		err := rows.Scan(
			&record.ID, &record.Name, &record.Type, &record.ConfigYAML,
			&record.PrimaryTimeframe, &record.SecondaryTimeframes, &record.RequiredTimeframes,
			&record.CreatedAt, &record.UpdatedAt, &record.Active,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}

		// Deserialize YAML
		var style ai.Strategy
		if err := yaml.Unmarshal([]byte(record.ConfigYAML), &style); err != nil {
			r.log.WithError(err).WithField("name", record.Name).Warn("Failed to unmarshal style, skipping")
			continue
		}

		styles = append(styles, &style)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating rows: %w", err)
	}

	return styles, nil
}

// Delete soft-deletes a trading style (sets active = false)
func (r *TradingStyleRepository) Delete(name string) error {
	result, err := r.db.Exec(`
		UPDATE trading_styles 
		SET active = 0, updated_at = ?
		WHERE name = ?
	`, time.Now(), name)

	if err != nil {
		return fmt.Errorf("failed to delete style: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("trading style not found: %s", name)
	}

	r.log.WithField("name", name).Info("Trading style deleted")
	return nil
}

// GetByType returns all styles of a specific type
func (r *TradingStyleRepository) GetByType(styleType string) ([]*ai.Strategy, error) {
	rows, err := r.db.Query(`
		SELECT id, name, type, config_yaml, primary_timeframe, secondary_timeframes, 
		       required_timeframes, created_at, updated_at, active
		FROM trading_styles
		WHERE type = ? AND active = 1
		ORDER BY name
	`, styleType)
	if err != nil {
		return nil, fmt.Errorf("failed to query styles by type: %w", err)
	}
	defer rows.Close()

	var styles []*ai.Strategy

	for rows.Next() {
		var record TradingStyleRecord
		err := rows.Scan(
			&record.ID, &record.Name, &record.Type, &record.ConfigYAML,
			&record.PrimaryTimeframe, &record.SecondaryTimeframes, &record.RequiredTimeframes,
			&record.CreatedAt, &record.UpdatedAt, &record.Active,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}

		// Deserialize YAML
		var style ai.Strategy
		if err := yaml.Unmarshal([]byte(record.ConfigYAML), &style); err != nil {
			r.log.WithError(err).WithField("name", record.Name).Warn("Failed to unmarshal style, skipping")
			continue
		}

		styles = append(styles, &style)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating rows: %w", err)
	}

	return styles, nil
}
