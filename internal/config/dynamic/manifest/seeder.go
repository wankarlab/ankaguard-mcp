package manifest

import (
	"context"
	"fmt"

	"github.com/rantipay/trading-mcp/internal/config/dynamic/domain"
	"github.com/rantipay/trading-mcp/internal/config/dynamic/repository"
)

// Seed calls UpsertRegistry for every entry returned by All().
// It is idempotent: running it multiple times only updates existing rows.
// Call this in main() BEFORE ConfigStore.Load() so every key has a registry
// entry before the store tries to resolve defaults.
func Seed(ctx context.Context, repo repository.ConfigRepository) error {
	for _, e := range All() {
		reg := &domain.ParameterRegistry{
			Key:             e.Key,
			Type:            e.Type,
			GroupName:       e.Group,
			Description:     e.Description,
			DefaultValue:    e.Default,
			MinValue:        nullIfEmpty(e.Min),
			MaxValue:        nullIfEmpty(e.Max),
			EnumValues:      nilIfEmpty(e.Enum),
			RequiresRestart: e.RequiresRestart,
			ScopeLevels:     e.ScopeLevels,
		}
		if err := repo.UpsertRegistry(ctx, reg); err != nil {
			return fmt.Errorf("seed %s: %w", e.Key, err)
		}
	}
	return nil
}

// nullIfEmpty returns nil when s is an empty string, or a pointer to s otherwise.
// This is used to convert optional Min/Max strings into nullable DB fields.
func nullIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// nilIfEmpty returns nil when the slice is empty, preserving the distinction
// between "no enum values" and an empty list when storing to the DB.
func nilIfEmpty(ss []string) []string {
	if len(ss) == 0 {
		return nil
	}
	return ss
}
