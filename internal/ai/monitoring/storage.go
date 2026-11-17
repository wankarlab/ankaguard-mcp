package monitoring

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// DecisionStorage stores trading decision records
type DecisionStorage struct {
	storagePath string
	records     map[string]*DecisionRecord
	mu          sync.RWMutex
}

// NewDecisionStorage creates a new decision storage
func NewDecisionStorage(storagePath string) *DecisionStorage {
	return &DecisionStorage{
		storagePath: storagePath,
		records:     make(map[string]*DecisionRecord),
	}
}

// Initialize sets up storage
func (ds *DecisionStorage) Initialize(ctx context.Context) error {
	ds.mu.Lock()
	defer ds.mu.Unlock()

	// Create storage directory if it doesn't exist
	if err := os.MkdirAll(ds.storagePath, 0755); err != nil {
		return fmt.Errorf("failed to create storage directory: %w", err)
	}

	// Load existing records
	return ds.loadRecords()
}

// loadRecords loads existing records from disk
func (ds *DecisionStorage) loadRecords() error {
	files, err := filepath.Glob(filepath.Join(ds.storagePath, "*.json"))
	if err != nil {
		return fmt.Errorf("failed to list records: %w", err)
	}

	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			continue // Skip files that can't be read
		}

		var record DecisionRecord
		if err := json.Unmarshal(data, &record); err != nil {
			continue // Skip invalid records
		}

		ds.records[record.ID] = &record
	}

	return nil
}

// Save stores a decision record
func (ds *DecisionStorage) Save(ctx context.Context, record *DecisionRecord) error {
	ds.mu.Lock()
	defer ds.mu.Unlock()

	// Store in memory
	ds.records[record.ID] = record

	// Persist to disk
	return ds.persistRecord(record)
}

// persistRecord saves a record to disk
func (ds *DecisionStorage) persistRecord(record *DecisionRecord) error {
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal record: %w", err)
	}

	filename := filepath.Join(ds.storagePath, fmt.Sprintf("%s.json", record.ID))
	if err := os.WriteFile(filename, data, 0644); err != nil {
		return fmt.Errorf("failed to write record: %w", err)
	}

	return nil
}

// UpdateOutcome updates a trade outcome
func (ds *DecisionStorage) UpdateOutcome(
	ctx context.Context,
	decisionID string,
	outcome string,
	pnl float64,
) error {
	ds.mu.Lock()
	defer ds.mu.Unlock()

	record, exists := ds.records[decisionID]
	if !exists {
		return fmt.Errorf("decision not found: %s", decisionID)
	}

	record.Outcome = outcome
	record.ActualPnL = pnl

	return ds.persistRecord(record)
}

// GetByPeriod retrieves decisions for a time period
func (ds *DecisionStorage) GetByPeriod(ctx context.Context, period string) ([]*DecisionRecord, error) {
	ds.mu.RLock()
	defer ds.mu.RUnlock()

	// Parse period
	startTime, err := parsePeriod(period)
	if err != nil {
		return nil, fmt.Errorf("invalid period: %w", err)
	}

	// Filter records
	var filtered []*DecisionRecord
	for _, record := range ds.records {
		if record.Timestamp.After(startTime) {
			filtered = append(filtered, record)
		}
	}

	// Sort by timestamp (newest first)
	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].Timestamp.After(filtered[j].Timestamp)
	})

	return filtered, nil
}

// GetRecent retrieves recent decisions
func (ds *DecisionStorage) GetRecent(ctx context.Context, limit int) ([]*DecisionRecord, error) {
	ds.mu.RLock()
	defer ds.mu.RUnlock()

	// Convert map to slice
	records := make([]*DecisionRecord, 0, len(ds.records))
	for _, record := range ds.records {
		records = append(records, record)
	}

	// Sort by timestamp (newest first)
	sort.Slice(records, func(i, j int) bool {
		return records[i].Timestamp.After(records[j].Timestamp)
	})

	// Limit results
	if limit > 0 && len(records) > limit {
		records = records[:limit]
	}

	return records, nil
}

// parsePeriod converts period string to start time
func parsePeriod(period string) (time.Time, error) {
	now := time.Now()

	switch period {
	case "1h", "hour":
		return now.Add(-1 * time.Hour), nil
	case "24h", "day":
		return now.Add(-24 * time.Hour), nil
	case "7d", "week":
		return now.Add(-7 * 24 * time.Hour), nil
	case "30d", "month":
		return now.Add(-30 * 24 * time.Hour), nil
	default:
		return time.Time{}, fmt.Errorf("unsupported period: %s", period)
	}
}

// Count returns the total number of records
func (ds *DecisionStorage) Count() int {
	ds.mu.RLock()
	defer ds.mu.RUnlock()
	return len(ds.records)
}

// Clear removes all records (use with caution)
func (ds *DecisionStorage) Clear() error {
	ds.mu.Lock()
	defer ds.mu.Unlock()

	ds.records = make(map[string]*DecisionRecord)

	// Remove all files
	files, err := filepath.Glob(filepath.Join(ds.storagePath, "*.json"))
	if err != nil {
		return err
	}

	for _, file := range files {
		os.Remove(file)
	}

	return nil
}
