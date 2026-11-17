package config

import (
	"encoding/json"
	"time"
)

// TradingProfile represents a saved configuration preset
type TradingProfile struct {
	ID          string               `json:"id" db:"id"`
	Name        string               `json:"name" db:"name" validate:"required"`
	Description string               `json:"description" db:"description"`
	Config      DynamicTradingConfig `json:"config" db:"config"`
	CreatedAt   time.Time            `json:"created_at" db:"created_at"`
	UpdatedAt   time.Time            `json:"updated_at" db:"updated_at"`
	IsDefault   bool                 `json:"is_default" db:"is_default"`
	Tags        []string             `json:"tags" db:"tags"`
}

// ToJSON converts profile to JSON string
func (p *TradingProfile) ToJSON() (string, error) {
	data, err := json.Marshal(p)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// FromJSON creates profile from JSON string
func FromJSON(jsonStr string) (*TradingProfile, error) {
	var profile TradingProfile
	if err := json.Unmarshal([]byte(jsonStr), &profile); err != nil {
		return nil, err
	}
	return &profile, nil
}

// Clone creates a deep copy of the profile
func (p *TradingProfile) Clone() *TradingProfile {
	clone := *p
	clone.Tags = make([]string, len(p.Tags))
	copy(clone.Tags, p.Tags)
	return &clone
}
