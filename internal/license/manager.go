package license

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/rantipay/trading-mcp/internal/edition"
	"github.com/sirupsen/logrus"
)

// License states.
const (
	StateValid   = "valid"
	StateGrace   = "grace"
	StateExpired = "expired"
	StateInvalid = "invalid"
)

// Status warnings.
const (
	WarningExpiringSoon = "expiring_soon"
	WarningInGrace      = "in_grace"
	WarningClockSkew    = "clock_skew"
)

const (
	// GracePeriod is how long after expiry the Pro features keep working.
	GracePeriod = 14 * 24 * time.Hour
	// ExpiryWarning is how far before expiry the status starts warning.
	ExpiryWarning = 14 * 24 * time.Hour
	// ClockSkewTolerance is the backwards clock jump tolerated before the
	// license is treated as expired.
	ClockSkewTolerance = 24 * time.Hour
	// ReverifyInterval is how often Run re-evaluates the license.
	ReverifyInterval = time.Hour

	licenseFileName = "license.key"
	clockFileName   = "license_clock.json"
)

// LicenseStatus is what operators see. It never contains the document.
type LicenseStatus struct {
	Edition            string  `json:"edition"` // effective: "community" or "pro"
	State              string  `json:"state"`
	Holder             *string `json:"holder"`
	LicenseID          *string `json:"license_id"`
	ExpiresAt          *string `json:"expires_at"`
	DaysRemaining      int     `json:"days_remaining"`
	GraceDaysRemaining int     `json:"grace_days_remaining"`
	Warning            *string `json:"warning"`
}

// Checker is what the MCP server consumes to gate real-money calls.
type Checker interface {
	Status() LicenseStatus
	// AllowsLive reports whether the license currently permits live
	// trade_increase and control calls (state valid or grace, Pro license).
	AllowsLive() bool
}

// Options configures a Manager.
type Options struct {
	Verifier *Verifier // defaults to NewVerifier()
	DataDir  string    // where the license and the clock record are kept; "" = memory only
	// LicenseKey and LicenseFile are the LICENSE_KEY / LICENSE_FILE values.
	LicenseKey  string
	LicenseFile string
	Now         func() time.Time // defaults to time.Now
	ProBuild    func() bool      // defaults to edition.IsPro
	Logger      *logrus.Logger
}

// Manager holds the installed license and evaluates its state.
type Manager struct {
	opt Options
	log *logrus.Logger

	mu           sync.RWMutex
	payload      *Payload
	lastVerified time.Time
}

var _ Checker = (*Manager)(nil)

// NewManager builds a Manager and loads the license from the configured
// sources. It never fails: with no usable license the state is "invalid".
func NewManager(opt Options) *Manager {
	if opt.Verifier == nil {
		opt.Verifier = NewVerifier()
	}
	if opt.Now == nil {
		opt.Now = time.Now
	}
	if opt.ProBuild == nil {
		opt.ProBuild = edition.IsPro
	}
	log := opt.Logger
	if log == nil {
		log = logrus.New()
		log.SetLevel(logrus.PanicLevel)
	}
	m := &Manager{opt: opt, log: log}
	m.loadClock()
	m.loadInitial()
	m.Reverify()
	return m
}

// ManagerFromEnv builds a Manager from LICENSE_KEY / LICENSE_FILE and dataDir.
func ManagerFromEnv(dataDir string, logger *logrus.Logger) *Manager {
	return NewManager(Options{
		DataDir:     dataDir,
		LicenseKey:  os.Getenv("LICENSE_KEY"),
		LicenseFile: os.Getenv("LICENSE_FILE"),
		Logger:      logger,
	})
}

// loadInitial picks, among the env value, the env file and the stored
// document, the parseable one that expires last (so a renewal installed via
// the API wins over a stale environment variable after a restart).
func (m *Manager) loadInitial() {
	var candidates []string
	if k := strings.TrimSpace(m.opt.LicenseKey); k != "" {
		candidates = append(candidates, k)
	}
	if f := m.opt.LicenseFile; f != "" {
		if b, err := os.ReadFile(f); err == nil {
			candidates = append(candidates, string(b))
		} else {
			m.log.WithError(err).Warn("license: LICENSE_FILE unreadable")
		}
	}
	if m.opt.DataDir != "" {
		if b, err := os.ReadFile(filepath.Join(m.opt.DataDir, licenseFileName)); err == nil {
			candidates = append(candidates, string(b))
		}
	}
	for _, doc := range candidates {
		p, err := m.opt.Verifier.Parse(doc)
		if err != nil {
			m.log.WithField("reason", Reason(err)).Warn("license: ignoring unusable license source")
			continue
		}
		if m.payload == nil || p.ExpiresAt.After(m.payload.ExpiresAt) {
			pp := p
			m.payload = &pp
		}
	}
}

// Install verifies doc and, if acceptable, makes it the active license and
// stores it in the data directory. On any error the previous license stays.
// Documents expired beyond the grace period are rejected with reason
// "expired".
func (m *Manager) Install(doc string) (LicenseStatus, error) {
	p, err := m.opt.Verifier.Parse(doc)
	if err != nil {
		return LicenseStatus{}, err
	}
	now := m.opt.Now().UTC()
	if now.After(p.ExpiresAt.Add(GracePeriod)) {
		return LicenseStatus{}, &InvalidError{ReasonExpired}
	}
	if m.opt.DataDir != "" {
		if err := writeFileAtomic(filepath.Join(m.opt.DataDir, licenseFileName), []byte(strings.TrimSpace(doc)+"\n")); err != nil {
			return LicenseStatus{}, fmt.Errorf("license: storing document: %w", err)
		}
	}
	m.mu.Lock()
	m.payload = &p
	m.mu.Unlock()
	m.Reverify()
	m.log.WithFields(logrus.Fields{"license_id": p.ID, "edition": p.Edition}).Info("license installed")
	return m.Status(), nil
}

// Reverify re-evaluates the license and records the verification time unless
// the clock went back more than the tolerance.
func (m *Manager) Reverify() {
	now := m.opt.Now().UTC()
	m.mu.Lock()
	skew := m.skewLocked(now)
	if !skew && now.After(m.lastVerified) {
		m.lastVerified = now
		m.saveClockLocked()
	}
	m.mu.Unlock()
	if skew {
		m.log.Warn("license: system clock is behind the last verification, treating license as expired")
	}
}

// Run re-verifies every ReverifyInterval until ctx is cancelled.
func (m *Manager) Run(ctx context.Context) {
	t := time.NewTicker(ReverifyInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.Reverify()
		}
	}
}

func (m *Manager) skewLocked(now time.Time) bool {
	return !m.lastVerified.IsZero() && now.Before(m.lastVerified.Add(-ClockSkewTolerance))
}

// Status evaluates the license right now. It does not include the document.
func (m *Manager) Status() LicenseStatus {
	now := m.opt.Now().UTC()
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.statusLocked(now)
}

func (m *Manager) statusLocked(now time.Time) LicenseStatus {
	st := LicenseStatus{Edition: EditionCommunity, State: StateInvalid}
	p := m.payload
	if p == nil {
		return st
	}
	holder, id, exp := p.Holder, p.ID, p.ExpiresAt.UTC().Format(time.RFC3339)
	st.Holder, st.LicenseID, st.ExpiresAt = &holder, &id, &exp
	setWarn := func(w string) { st.Warning = &w }

	switch {
	case m.skewLocked(now):
		st.State = StateExpired
		setWarn(WarningClockSkew)
	case now.Before(p.ExpiresAt):
		st.State = StateValid
		st.DaysRemaining = ceilDays(p.ExpiresAt.Sub(now))
		if p.ExpiresAt.Sub(now) <= ExpiryWarning {
			setWarn(WarningExpiringSoon)
		}
	case !now.After(p.ExpiresAt.Add(GracePeriod)):
		st.State = StateGrace
		st.GraceDaysRemaining = ceilDays(p.ExpiresAt.Add(GracePeriod).Sub(now))
		setWarn(WarningInGrace)
	default:
		st.State = StateExpired
	}
	if (st.State == StateValid || st.State == StateGrace) && p.Edition == EditionPro && m.opt.ProBuild() {
		st.Edition = EditionPro
	}
	return st
}

// AllowsLive reports whether the license is Pro and in state valid or grace.
// It does not look at the build edition: the server combines it with
// edition.IsProduct() as D8 prescribes.
func (m *Manager) AllowsLive() bool {
	now := m.opt.Now().UTC()
	m.mu.RLock()
	defer m.mu.RUnlock()
	st := m.statusLocked(now)
	return (st.State == StateValid || st.State == StateGrace) && m.payload != nil && m.payload.Edition == EditionPro
}

func ceilDays(d time.Duration) int {
	if d <= 0 {
		return 0
	}
	day := 24 * time.Hour
	return int((d + day - 1) / day)
}

type clockRecord struct {
	LastVerified time.Time `json:"last_verified"`
}

func (m *Manager) loadClock() {
	if m.opt.DataDir == "" {
		return
	}
	b, err := os.ReadFile(filepath.Join(m.opt.DataDir, clockFileName))
	if err != nil {
		return
	}
	var rec clockRecord
	if json.Unmarshal(b, &rec) == nil {
		m.lastVerified = rec.LastVerified.UTC()
	}
}

func (m *Manager) saveClockLocked() {
	if m.opt.DataDir == "" {
		return
	}
	b, _ := json.Marshal(clockRecord{LastVerified: m.lastVerified})
	if err := writeFileAtomic(filepath.Join(m.opt.DataDir, clockFileName), b); err != nil {
		m.log.WithError(err).Warn("license: could not store last verification time")
	}
}

func writeFileAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-license-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Rename(name, path); err != nil {
		os.Remove(name)
		return errors.New("license: atomic rename failed: " + err.Error())
	}
	return nil
}
