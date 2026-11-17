// Package localauth implements the standalone (local) authentication of the
// product: one operator with a bcrypt password, a self-generated HS256 signing
// secret and revocable agent tokens. It does not depend on any external auth
// service.
//
// Wiring (done outside this package):
//
//  1. Build the service once at startup:
//     svc, err := localauth.New(localauth.Config{Mode: cfg.Auth.Mode, DataDir: dataDir, DB: sqlDB, Logger: log})
//     svc.Start(ctx) // reloads revoked jti / secret / epoch every 2 s; svc.Close() on shutdown
//  2. Standalone mode: use svc.Secret() instead of KONG_JWT_SECRET for the existing
//     JWT validators (the tokens carry the same claims: sub, user_level, tenant_id,
//     company_id, exp). Because the secret can be rotated by the CLI while the
//     server runs, prefer reading svc.Secret() per request. After the existing
//     signature check, also call svc.CheckToken(rawToken) (or CheckClaims) to
//     enforce session epoch, revocation and last-use tracking, and read
//     Principal.Profile / Principal.IsOperator for authorization.
//  3. Mount the public/self-authenticating routes OUTSIDE any JWT middleware:
//     svc.RegisterRoutes(router.Group("/api/v1/auth/local"))   // gin
//     or mux.Handle("/api/v1/auth/local/", svc.HTTPHandler())  // net/http
//     Operator-only routes authenticate the bearer token themselves.
//  4. CLI: `operator set-password` / `operator rotate-secret` call
//     localauth.RunOperatorCommand(dataDir, args, os.Stdin, os.Stdout, os.Stderr).
//     These work directly on the data directory, so they function with the
//     server stopped or running (the running server picks up the change on its
//     next 2 s refresh).
//  5. The agent_tokens table comes from migration 030_agent_tokens.sql.
//
// Data directory files (all 0600, directory 0700): operator_password.hash,
// jwt_secret, session_epoch. The signing secret and passwords are never logged
// or returned.
package localauth

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
	"golang.org/x/crypto/bcrypt"
)

// Authentication modes.
const (
	ModeStandalone = "standalone"
	ModeExternal   = "external"
)

// MCP profiles carried in the mcp_profile claim.
const (
	ProfileRead  = "read"
	ProfilePaper = "paper"
	ProfileLive  = "live"
)

// token_use claim values.
const (
	TokenUseOperator = "operator_session"
	TokenUseAgent    = "agent"
)

// Limits.
const (
	MinPasswordLength   = 12
	maxPasswordBytes    = 72 // bcrypt limit
	OperatorSessionTTL  = 12 * time.Hour
	OperatorLevel       = 4
	DefaultTokenDays    = 90
	MaxTokenDays        = 365
	RefreshInterval     = 2 * time.Second
	LastUsedMinInterval = time.Minute
	maxTokenNameLength  = 64

	passwordFile = "operator_password.hash"
	secretFile   = "jwt_secret"
	epochFile    = "session_epoch"
	localTenant  = "local"
)

// Errors.
var (
	ErrPasswordTooShort = fmt.Errorf("password must have at least %d characters", MinPasswordLength)
	ErrPasswordTooLong  = fmt.Errorf("password must have at most %d bytes", maxPasswordBytes)
	ErrPasswordNotSet   = errors.New("operator password is not set")
	ErrInvalidToken     = errors.New("invalid token")
	ErrTokenRevoked     = errors.New("token revoked")
	ErrNotAvailable     = errors.New("local auth not available in external mode")
)

// Claims is the JWT payload. The first block matches what the existing
// validation (middleware.KongJWTClaims) reads.
type Claims struct {
	UserID    string  `json:"sub"`
	UserLevel float64 `json:"user_level"`
	TenantID  string  `json:"tenant_id"`
	CompanyID string  `json:"company_id"`
	// Local additions.
	MCPProfile   string `json:"mcp_profile"`
	TokenUse     string `json:"token_use"`
	SessionEpoch int64  `json:"sess_epoch,omitempty"` // operator sessions only
	jwt.RegisteredClaims
}

// Principal is the verified identity behind a token.
type Principal struct {
	Subject    string
	Level      int
	Profile    string
	TokenUse   string
	JTI        string
	ExpiresAt  time.Time
	IsOperator bool
}

// Config configures a Service.
type Config struct {
	Mode       string // "standalone" (default) or "external"
	DataDir    string
	DB         *sql.DB // required in standalone mode (agent_tokens table)
	Logger     *logrus.Logger
	Now        func() time.Time // test hook
	BcryptCost int              // 0 = 12
	// LiveAllowed reports whether a live agent token may be created (product
	// edition: a valid or grace Pro license). nil = always allowed.
	LiveAllowed func() bool
}

// Service is the local authentication service.
type Service struct {
	mode string
	dir  string
	db   *sql.DB
	log  *logrus.Logger
	now  func() time.Time
	cost int

	liveAllowed func() bool

	mu       sync.RWMutex
	secret   []byte
	epoch    int64
	hash     []byte
	revoked  map[string]struct{}
	lastSeen map[string]time.Time // jti -> last persisted last_used_at

	limiter *limiter
	stop    context.CancelFunc
	wg      sync.WaitGroup
}

// New builds the service. In standalone mode it creates the data directory and
// generates the signing secret on first start.
func New(cfg Config) (*Service, error) {
	s := &Service{
		mode:        cfg.Mode,
		dir:         cfg.DataDir,
		db:          cfg.DB,
		log:         cfg.Logger,
		now:         cfg.Now,
		cost:        cfg.BcryptCost,
		liveAllowed: cfg.LiveAllowed,
		revoked:     map[string]struct{}{},
		lastSeen:    map[string]time.Time{},
	}
	if s.mode == "" {
		s.mode = ModeStandalone
	}
	if s.mode != ModeStandalone && s.mode != ModeExternal {
		return nil, fmt.Errorf("localauth: unknown auth mode %q", s.mode)
	}
	if s.log == nil {
		s.log = logrus.New()
	}
	if s.now == nil {
		s.now = time.Now
	}
	if s.cost == 0 {
		s.cost = 12
	}
	s.limiter = newLimiter(s.now)
	if s.mode == ModeExternal {
		return s, nil
	}
	if s.dir == "" {
		return nil, errors.New("localauth: data directory is required in standalone mode")
	}
	if s.db == nil {
		return nil, errors.New("localauth: database is required in standalone mode")
	}
	if err := ensureDir(s.dir); err != nil {
		return nil, err
	}
	if _, err := loadOrCreateSecret(s.dir, s.log); err != nil {
		return nil, err
	}
	if err := s.Refresh(); err != nil {
		return nil, err
	}
	return s, nil
}

// Mode returns the active mode.
func (s *Service) Mode() string { return s.mode }

// Standalone reports whether local auth is active.
func (s *Service) Standalone() bool { return s.mode == ModeStandalone }

// PasswordSet reports whether the operator password has been established.
func (s *Service) PasswordSet() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.hash) > 0
}

// Secret returns the current signing secret for the existing HS256 validators.
// Callers must never log it.
func (s *Service) Secret() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return string(s.secret)
}

// Start launches the background refresh (revoked jti set, secret, epoch, password
// hash) every RefreshInterval. Safe to call once; Close stops it.
func (s *Service) Start(ctx context.Context) {
	if !s.Standalone() || s.stop != nil {
		return
	}
	ctx, s.stop = context.WithCancel(ctx)
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		t := time.NewTicker(RefreshInterval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if err := s.Refresh(); err != nil {
					s.log.WithError(err).Warn("localauth: refresh failed")
				}
			}
		}
	}()
}

// Close stops the background refresh.
func (s *Service) Close() {
	if s.stop != nil {
		s.stop()
		s.wg.Wait()
		s.stop = nil
	}
}

// Refresh reloads secret, session epoch, password hash and the revoked set.
func (s *Service) Refresh() error {
	if !s.Standalone() {
		return nil
	}
	secret, err := loadOrCreateSecret(s.dir, s.log)
	if err != nil {
		return err
	}
	epoch, err := readEpoch(s.dir)
	if err != nil {
		return err
	}
	hash, err := readHash(s.dir)
	if err != nil {
		return err
	}
	revoked, err := s.loadRevoked()
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.secret, s.epoch, s.hash, s.revoked = secret, epoch, hash, revoked
	s.mu.Unlock()
	return nil
}

// ---- token issuing / checking ----

func (s *Service) sign(c Claims) (string, error) {
	s.mu.RLock()
	secret := s.secret
	s.mu.RUnlock()
	return jwt.NewWithClaims(jwt.SigningMethodHS256, c).SignedString(secret)
}

// issueOperatorSession returns a 12 h level-4 live session token.
func (s *Service) issueOperatorSession() (string, time.Time, error) {
	now := s.now()
	exp := now.Add(OperatorSessionTTL)
	s.mu.RLock()
	epoch := s.epoch
	s.mu.RUnlock()
	tok, err := s.sign(Claims{
		UserID: "operator", UserLevel: OperatorLevel, TenantID: localTenant, CompanyID: localTenant,
		MCPProfile: ProfileLive, TokenUse: TokenUseOperator, SessionEpoch: epoch,
		RegisteredClaims: jwt.RegisteredClaims{
			ID: uuid.NewString(), IssuedAt: jwt.NewNumericDate(now), ExpiresAt: jwt.NewNumericDate(exp),
		},
	})
	return tok, exp, err
}

// AgentLevel maps an agent profile to the numeric user_level of its token.
func AgentLevel(profile string) int {
	switch profile {
	case ProfileLive:
		return 3
	case ProfilePaper:
		return 2
	default:
		return 1
	}
}

// CheckToken fully validates a raw bearer token: HS256 signature with the current
// secret, expiry, operator session epoch, and agent-token revocation. For agent
// tokens it also records last use (coalesced).
func (s *Service) CheckToken(raw string) (*Principal, error) {
	if !s.Standalone() {
		return nil, ErrNotAvailable
	}
	claims := &Claims{}
	_, err := jwt.ParseWithClaims(raw, claims, func(t *jwt.Token) (interface{}, error) {
		if t.Method != jwt.SigningMethodHS256 {
			return nil, fmt.Errorf("unexpected signing method %v", t.Header["alg"])
		}
		s.mu.RLock()
		defer s.mu.RUnlock()
		return s.secret, nil
	}, jwt.WithoutClaimsValidation())
	if err != nil {
		return nil, ErrInvalidToken
	}
	return s.CheckClaims(claims)
}

// CheckClaims applies the stateful checks (expiry, epoch, revocation) to claims
// whose signature was already verified by the existing validator.
func (s *Service) CheckClaims(c *Claims) (*Principal, error) {
	if c.ExpiresAt == nil || !c.ExpiresAt.After(s.now()) {
		return nil, ErrInvalidToken
	}
	p := &Principal{
		Subject: c.UserID, Level: int(c.UserLevel), Profile: c.MCPProfile,
		TokenUse: c.TokenUse, JTI: c.ID, ExpiresAt: c.ExpiresAt.Time,
	}
	switch p.Profile {
	case ProfileRead, ProfilePaper, ProfileLive:
	default:
		return nil, ErrInvalidToken
	}
	switch c.TokenUse {
	case TokenUseOperator:
		s.mu.RLock()
		ok := c.SessionEpoch == s.epoch
		s.mu.RUnlock()
		if !ok {
			return nil, ErrInvalidToken
		}
		p.IsOperator = true
	case TokenUseAgent:
		if c.ID == "" {
			return nil, ErrInvalidToken
		}
		if s.IsRevoked(c.ID) {
			return nil, ErrTokenRevoked
		}
		s.touchLastUsed(c.ID)
	default:
		return nil, ErrInvalidToken
	}
	return p, nil
}

// IsRevoked reports whether an agent token jti is revoked (in-memory, reloaded
// every 2 s and on every local revocation).
func (s *Service) IsRevoked(jti string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.revoked[jti]
	return ok
}

// ---- password / secret management (CLI-facing) ----

// SetPassword validates and stores a new operator password and invalidates every
// operator session (by bumping the session epoch). Agent tokens are unaffected.
func (s *Service) SetPassword(password string) error {
	if err := SetPassword(s.dir, password, s.cost); err != nil {
		return err
	}
	return s.Refresh()
}

// RotateSecret generates a new signing secret, invalidating all sessions and
// tokens, and also bumps the epoch.
func (s *Service) RotateSecret() error {
	if err := RotateSecret(s.dir); err != nil {
		return err
	}
	return s.Refresh()
}

// SetPassword is the stateless variant used by the CLI: it works on the data
// directory alone. cost 0 = bcrypt cost 12.
func SetPassword(dataDir, password string, cost int) error {
	if len([]rune(password)) < MinPasswordLength {
		return ErrPasswordTooShort
	}
	if len(password) > maxPasswordBytes {
		return ErrPasswordTooLong
	}
	if cost == 0 {
		cost = 12
	}
	if err := ensureDir(dataDir); err != nil {
		return err
	}
	h, err := bcrypt.GenerateFromPassword([]byte(password), cost)
	if err != nil {
		return err
	}
	if err := writeSecretFile(filepath.Join(dataDir, passwordFile), h); err != nil {
		return err
	}
	return bumpEpoch(dataDir)
}

// RotateSecret is the stateless variant used by the CLI.
func RotateSecret(dataDir string) error {
	if err := ensureDir(dataDir); err != nil {
		return err
	}
	sec, err := newSecret()
	if err != nil {
		return err
	}
	if err := writeSecretFile(filepath.Join(dataDir, secretFile), sec); err != nil {
		return err
	}
	return bumpEpoch(dataDir)
}

func (s *Service) verifyPassword(password string) error {
	// `operator set-password` runs in another process: reload the hash and the
	// session epoch from disk first, so a login right after it neither sees the
	// password as unset nor mints a session with the old epoch (which the next
	// background refresh would invalidate).
	if err := s.Refresh(); err != nil {
		s.log.WithError(err).Warn("localauth: refresh before login failed; using the cached state")
	}
	s.mu.RLock()
	h := s.hash
	s.mu.RUnlock()
	if len(h) == 0 {
		return ErrPasswordNotSet
	}
	return bcrypt.CompareHashAndPassword(h, []byte(password))
}

// ---- files ----

func ensureDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("localauth: create data dir: %w", err)
	}
	return nil
}

func newSecret() ([]byte, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	return []byte(hex.EncodeToString(b)), nil
}

// writeSecretFile writes atomically with 0600 permissions.
func writeSecretFile(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// tightenPerms forces 0600 on an existing file, warning when it was looser.
func tightenPerms(path string, log *logrus.Logger) error {
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	if fi.Mode().Perm()&0o077 != 0 {
		if err := os.Chmod(path, 0o600); err != nil {
			return err
		}
		if log != nil {
			log.WithField("file", path).Warn("localauth: file had open permissions, tightened to 0600")
		}
	}
	return nil
}

func loadOrCreateSecret(dir string, log *logrus.Logger) ([]byte, error) {
	path := filepath.Join(dir, secretFile)
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		sec, err := newSecret()
		if err != nil {
			return nil, err
		}
		if err := writeSecretFile(path, sec); err != nil {
			return nil, err
		}
		if log != nil {
			log.Info("localauth: signing secret generated on first start")
		}
		return sec, nil
	}
	if err != nil {
		return nil, err
	}
	if err := tightenPerms(path, log); err != nil {
		return nil, err
	}
	sec := []byte(strings.TrimSpace(string(b)))
	if len(sec) < 32 {
		return nil, errors.New("localauth: signing secret file is corrupt")
	}
	return sec, nil
}

func readEpoch(dir string) (int64, error) {
	b, err := os.ReadFile(filepath.Join(dir, epochFile))
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
}

func bumpEpoch(dir string) error {
	e, err := readEpoch(dir)
	if err != nil {
		return err
	}
	return writeSecretFile(filepath.Join(dir, epochFile), []byte(strconv.FormatInt(e+1, 10)))
}

func readHash(dir string) ([]byte, error) {
	path := filepath.Join(dir, passwordFile)
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return []byte(strings.TrimSpace(string(b))), nil
}
