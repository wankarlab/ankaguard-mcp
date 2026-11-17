package dynamic

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// BackupEntry is one row in the periodic JSON backup file.
type BackupEntry struct {
	Key       string    `json:"key"`
	ScopeType string    `json:"scope_type"`
	ScopeID   string    `json:"scope_id"`
	Value     string    `json:"value"`
	UpdatedBy string    `json:"updated_by"`
	UpdatedAt time.Time `json:"updated_at"`
}

// RunBackupWorker starts a ticker that writes a JSON backup every interval and then
// cleans up old backup files. It blocks until ctx is cancelled.
// Intended to be launched as a goroutine:
//
//	go store.RunBackupWorker(ctx, "data/config_backup", time.Hour)
func (s *ConfigStore) RunBackupWorker(ctx context.Context, dir string, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.writeBackup(ctx, dir); err != nil {
				s.log.WithError(err).Error("config backup write failed")
			}
			if err := cleanupOldBackups(dir); err != nil {
				s.log.WithError(err).Warn("config backup cleanup failed")
			}
		}
	}
}

// writeBackup fetches all current values and writes them atomically to
// <dir>/config_YYYYMMDD_HH.json (write to .tmp, then rename).
func (s *ConfigStore) writeBackup(ctx context.Context, dir string) error {
	vals, err := s.repo.ListAllValues(ctx)
	if err != nil {
		return fmt.Errorf("list all values: %w", err)
	}

	entries := make([]BackupEntry, 0, len(vals))
	for _, v := range vals {
		entries = append(entries, BackupEntry{
			Key:       v.Key,
			ScopeType: string(v.ScopeType),
			ScopeID:   v.ScopeID,
			Value:     v.Value,
			UpdatedBy: v.UpdatedBy,
			UpdatedAt: v.UpdatedAt,
		})
	}

	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal backup: %w", err)
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}

	now := time.Now().UTC()
	filename := fmt.Sprintf("config_%s.json", now.Format("20060102_15"))
	dest := filepath.Join(dir, filename)
	tmp := dest + ".tmp"

	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("write tmp: %w", err)
	}
	if err := os.Rename(tmp, dest); err != nil {
		_ = os.Remove(tmp) // best-effort cleanup of orphaned tmp
		return fmt.Errorf("rename to dest: %w", err)
	}

	s.log.WithField("file", dest).Info("config backup written")
	return nil
}

// cleanupOldBackups removes files outside the retention window:
//   - Keep everything ≤ 7 days old untouched.
//   - For files between 7 and 30 days old, keep only one per day (the earliest hour).
//   - Delete files older than 30 days.
func cleanupOldBackups(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read dir %s: %w", dir, err)
	}

	now := time.Now().UTC()
	cutoff30 := now.AddDate(0, 0, -30)
	cutoff7 := now.AddDate(0, 0, -7)

	// Map day (YYYYMMDD) → slice of (hour, path) for the 7–30 day window.
	type fileInfo struct {
		hour int
		path string
	}
	dayFiles := make(map[string][]fileInfo)

	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasPrefix(name, "config_") || !strings.HasSuffix(name, ".json") {
			continue
		}
		// Expected format: config_YYYYMMDD_HH.json
		inner := strings.TrimPrefix(strings.TrimSuffix(name, ".json"), "config_")
		// inner should be YYYYMMDD_HH (11 chars)
		if len(inner) != 11 {
			continue
		}
		dateStr := inner[:8]  // YYYYMMDD
		hourStr := inner[9:]  // HH

		t, err := time.ParseInLocation("20060102", dateStr, time.UTC)
		if err != nil {
			continue
		}

		fullPath := filepath.Join(dir, name)

		if t.Before(cutoff30) {
			// Older than 30 days — delete unconditionally.
			_ = os.Remove(fullPath)
			continue
		}

		if t.Before(cutoff7) {
			// Between 7 and 30 days — collect for per-day dedup.
			hour := 0
			fmt.Sscanf(hourStr, "%d", &hour)
			dayFiles[dateStr] = append(dayFiles[dateStr], fileInfo{hour: hour, path: fullPath})
		}
		// Files ≤ 7 days old are kept as-is.
	}

	// For each day in the 7–30 day window, keep only the file with the smallest hour.
	for _, files := range dayFiles {
		if len(files) <= 1 {
			continue
		}
		sort.Slice(files, func(i, j int) bool {
			return files[i].hour < files[j].hour
		})
		// Keep files[0] (earliest hour), delete the rest.
		for _, f := range files[1:] {
			_ = os.Remove(f.path)
		}
	}

	return nil
}
