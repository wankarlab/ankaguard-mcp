package localauth

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// ErrTokenNotFound is returned when revoking an unknown jti.
var ErrTokenNotFound = errors.New("agent token not found")

// ErrInvalidTokenRequest wraps validation errors of CreateAgentToken.
var ErrInvalidTokenRequest = errors.New("invalid agent token request")

// ErrLicenseRequired is returned when a live token is requested without a
// license that allows real-money trading.
var ErrLicenseRequired = errors.New("a valid Pro license is required to create a live token")

// AgentTokenInfo is the listable metadata of an agent token (never its value).
type AgentTokenInfo struct {
	ID         string     `json:"id"` // jti
	Name       string     `json:"name"`
	Profile    string     `json:"profile"`
	CreatedAt  time.Time  `json:"created_at"`
	ExpiresAt  time.Time  `json:"expires_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
}

func validProfile(p string) bool {
	return p == ProfileRead || p == ProfilePaper || p == ProfileLive
}

// CreateAgentToken stores the metadata and returns the signed token value, which
// is the only time it is available. days 0 = DefaultTokenDays; > MaxTokenDays is rejected.
func (s *Service) CreateAgentToken(name, profile string, days int) (string, AgentTokenInfo, error) {
	var info AgentTokenInfo
	if !s.Standalone() {
		return "", info, ErrNotAvailable
	}
	name = strings.TrimSpace(name)
	if name == "" || len(name) > maxTokenNameLength {
		return "", info, fmt.Errorf("%w: name must have 1-%d characters", ErrInvalidTokenRequest, maxTokenNameLength)
	}
	if !validProfile(profile) {
		return "", info, fmt.Errorf("%w: profile must be read, paper or live", ErrInvalidTokenRequest)
	}
	s.mu.RLock()
	liveAllowed := s.liveAllowed
	s.mu.RUnlock()
	if profile == ProfileLive && liveAllowed != nil && !liveAllowed() {
		return "", info, ErrLicenseRequired
	}
	if days == 0 {
		days = DefaultTokenDays
	}
	if days < 0 || days > MaxTokenDays {
		return "", info, fmt.Errorf("%w: expiry must be between 1 and %d days", ErrInvalidTokenRequest, MaxTokenDays)
	}
	now := s.now().UTC()
	exp := now.Add(time.Duration(days) * 24 * time.Hour)
	jti := uuid.NewString()
	tok, err := s.sign(Claims{
		UserID: "agent:" + jti, UserLevel: float64(AgentLevel(profile)), TenantID: localTenant, CompanyID: localTenant,
		MCPProfile: profile, TokenUse: TokenUseAgent,
		RegisteredClaims: jwt.RegisteredClaims{ID: jti, IssuedAt: jwt.NewNumericDate(now), ExpiresAt: jwt.NewNumericDate(exp)},
	})
	if err != nil {
		return "", info, err
	}
	if _, err := s.db.Exec(`INSERT INTO agent_tokens (jti, name, profile, created_at, expires_at) VALUES (?, ?, ?, ?, ?)`,
		jti, name, profile, now, exp); err != nil {
		return "", info, err
	}
	info = AgentTokenInfo{ID: jti, Name: name, Profile: profile, CreatedAt: now, ExpiresAt: exp}
	s.log.WithFields(map[string]interface{}{"jti": jti, "name": name, "profile": profile}).Info("localauth: agent token created")
	return tok, info, nil
}

// ListAgentTokens returns every token's metadata, newest first, without values.
func (s *Service) ListAgentTokens() ([]AgentTokenInfo, error) {
	if !s.Standalone() {
		return nil, ErrNotAvailable
	}
	rows, err := s.db.Query(`SELECT jti, name, profile, created_at, expires_at, last_used_at, revoked_at
		FROM agent_tokens ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AgentTokenInfo{}
	for rows.Next() {
		var t AgentTokenInfo
		var last, rev sql.NullTime
		if err := rows.Scan(&t.ID, &t.Name, &t.Profile, &t.CreatedAt, &t.ExpiresAt, &last, &rev); err != nil {
			return nil, err
		}
		if last.Valid {
			t.LastUsedAt = &last.Time
		}
		if rev.Valid {
			t.RevokedAt = &rev.Time
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// RevokeAgentToken marks the token revoked and updates the local cache at once;
// other processes pick it up on their next refresh (<= 2 s). Idempotent.
func (s *Service) RevokeAgentToken(jti string) error {
	if !s.Standalone() {
		return ErrNotAvailable
	}
	now := s.now().UTC()
	res, err := s.db.Exec(`UPDATE agent_tokens SET revoked_at = COALESCE(revoked_at, ?) WHERE jti = ?`, now, jti)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrTokenNotFound
	}
	s.mu.Lock()
	s.revoked[jti] = struct{}{}
	s.mu.Unlock()
	s.log.WithField("jti", jti).Info("localauth: agent token revoked")
	return nil
}

func (s *Service) loadRevoked() (map[string]struct{}, error) {
	rows, err := s.db.Query(`SELECT jti FROM agent_tokens WHERE revoked_at IS NOT NULL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	m := map[string]struct{}{}
	for rows.Next() {
		var j string
		if err := rows.Scan(&j); err != nil {
			return nil, err
		}
		m[j] = struct{}{}
	}
	return m, rows.Err()
}

// touchLastUsed persists last_used_at at most once per LastUsedMinInterval per token.
func (s *Service) touchLastUsed(jti string) {
	now := s.now().UTC()
	s.mu.Lock()
	if last, ok := s.lastSeen[jti]; ok && now.Sub(last) < LastUsedMinInterval {
		s.mu.Unlock()
		return
	}
	s.lastSeen[jti] = now
	s.mu.Unlock()
	if _, err := s.db.Exec(`UPDATE agent_tokens SET last_used_at = ? WHERE jti = ?`, now, jti); err != nil {
		s.log.WithError(err).Warn("localauth: could not update last_used_at")
	}
}

// SetLiveAllowed wires the license check for live tokens after construction
// (the license manager is built later in the boot sequence). nil = allowed.
func (s *Service) SetLiveAllowed(f func() bool) {
	s.mu.Lock()
	s.liveAllowed = f
	s.mu.Unlock()
}
