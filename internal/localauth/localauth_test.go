package localauth

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/sirupsen/logrus"

	tradingmw "github.com/rantipay/trading-mcp/internal/trading/middleware"
)

const goodPassword = "correct horse battery"

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) Add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func openDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	ddl, err := os.ReadFile("../storage/migrations/030_agent_tokens.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(string(ddl)); err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	return db
}

type env struct {
	svc  *Service
	clk  *clock
	dir  string
	db   *sql.DB
	logs *bytes.Buffer
}

func newEnv(t *testing.T, mode string) *env {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "data")
	dbPath := filepath.Join(t.TempDir(), "t.db")
	clk := &clock{t: time.Now()}
	logs := &bytes.Buffer{}
	log := logrus.New()
	log.SetOutput(logs)
	db := openDB(t, dbPath)
	svc, err := New(Config{Mode: mode, DataDir: dir, DB: db, Logger: log, Now: clk.Now, BcryptCost: 4})
	if err != nil {
		t.Fatal(err)
	}
	return &env{svc, clk, dir, db, logs}
}

func (e *env) do(method, path, token, body string, remote string) (*httptest.ResponseRecorder, map[string]any) {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if remote != "" {
		req.RemoteAddr = remote
	}
	w := httptest.NewRecorder()
	e.svc.HTTPHandler().ServeHTTP(w, req)
	var m map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &m)
	return w, m
}

func (e *env) login(t *testing.T) string {
	t.Helper()
	w, m := e.do("POST", BasePath+"/login", "", `{"password":"`+goodPassword+`"}`, "10.0.0.1:1000")
	if w.Code != 200 {
		t.Fatalf("login: %d %s", w.Code, w.Body.String())
	}
	return m["token"].(string)
}

func (e *env) mkToken(t *testing.T, op, profile string) (string, string) {
	t.Helper()
	w, m := e.do("POST", BasePath+"/tokens", op, `{"name":"bot","profile":"`+profile+`"}`, "")
	if w.Code != 201 {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	return m["token"].(string), m["agent_token"].(map[string]any)["id"].(string)
}

// --- mode ---

func TestModeEndpoint(t *testing.T) {
	e := newEnv(t, ModeStandalone)
	w, m := e.do("GET", BasePath+"/mode", "", "", "")
	if w.Code != 200 || m["mode"] != "standalone" || m["password_set"] != false || len(m) != 2 {
		t.Fatalf("unexpected %v", m)
	}
	if err := e.svc.SetPassword(goodPassword); err != nil {
		t.Fatal(err)
	}
	_, m = e.do("GET", BasePath+"/mode", "", "", "")
	if m["password_set"] != true {
		t.Fatalf("unexpected %v", m)
	}
}

func TestExternalModeUnavailable(t *testing.T) {
	svc, err := New(Config{Mode: ModeExternal})
	if err != nil {
		t.Fatal(err)
	}
	e := &env{svc: svc}
	w, m := e.do("GET", BasePath+"/mode", "", "", "")
	if w.Code != 200 || m["mode"] != "external" {
		t.Fatalf("mode: %d %v", w.Code, m)
	}
	for _, c := range [][2]string{{"POST", "/login"}, {"GET", "/tokens"}, {"POST", "/tokens"}, {"DELETE", "/tokens/x"}} {
		w, m := e.do(c[0], BasePath+c[1], "x", `{}`, "")
		if w.Code != http.StatusNotImplemented || m["code"] != "AUTH_MODE_EXTERNAL" {
			t.Fatalf("%v: %d %v", c, w.Code, m)
		}
	}
}

// --- password ---

func TestLoginBeforePasswordSet(t *testing.T) {
	e := newEnv(t, ModeStandalone)
	w, m := e.do("POST", BasePath+"/login", "", `{"password":"anything at all"}`, "")
	if w.Code != http.StatusConflict || m["code"] != "PASSWORD_NOT_SET" {
		t.Fatalf("%d %v", w.Code, m)
	}
}

func TestShortPasswordRejectedAndNothingSaved(t *testing.T) {
	e := newEnv(t, ModeStandalone)
	if err := e.svc.SetPassword("short-11-ch"); err != ErrPasswordTooShort {
		t.Fatalf("got %v", err)
	}
	if e.svc.PasswordSet() {
		t.Fatal("password saved")
	}
	if _, err := os.Stat(filepath.Join(e.dir, passwordFile)); !os.IsNotExist(err) {
		t.Fatal("hash file exists")
	}
}

func TestPasswordStoredOnlyAsHash(t *testing.T) {
	e := newEnv(t, ModeStandalone)
	_ = e.svc.SetPassword(goodPassword)
	b, _ := os.ReadFile(filepath.Join(e.dir, passwordFile))
	if bytes.Contains(b, []byte(goodPassword)) || !bytes.HasPrefix(b, []byte("$2")) {
		t.Fatal("not a bcrypt hash")
	}
}

func TestRecoveryInvalidatesSessions(t *testing.T) {
	e := newEnv(t, ModeStandalone)
	_ = e.svc.SetPassword(goodPassword)
	op := e.login(t)
	if _, err := e.svc.CheckToken(op); err != nil {
		t.Fatal(err)
	}
	// CLI path in another process: stateless function, then server refresh.
	if err := SetPassword(e.dir, "another long password", 4); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Refresh(); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.CheckToken(op); err == nil {
		t.Fatal("old session still valid after password change")
	}
	w, _ := e.do("GET", BasePath+"/tokens", op, "", "")
	if w.Code != 401 {
		t.Fatalf("old session accepted: %d", w.Code)
	}
}

// --- login & limiter ---

func TestLoginSuccessSession(t *testing.T) {
	e := newEnv(t, ModeStandalone)
	_ = e.svc.SetPassword(goodPassword)
	tok := e.login(t)
	p, err := e.svc.CheckToken(tok)
	if err != nil || !p.IsOperator || p.Level != 4 || p.Profile != ProfileLive {
		t.Fatalf("%+v %v", p, err)
	}
	if p.ExpiresAt.After(e.clk.Now().Add(12*time.Hour + time.Second)) {
		t.Fatal("session longer than 12h")
	}
	e.clk.Add(12*time.Hour + time.Minute)
	if _, err := e.svc.CheckToken(tok); err == nil {
		t.Fatal("session valid after 12h")
	}
}

func TestWrongPasswordGenericAndAudited(t *testing.T) {
	e := newEnv(t, ModeStandalone)
	_ = e.svc.SetPassword(goodPassword)
	w, m := e.do("POST", BasePath+"/login", "", `{"password":"wrong password here"}`, "9.9.9.9:5")
	if w.Code != 401 || m["code"] != "INVALID_CREDENTIALS" {
		t.Fatalf("%d %v", w.Code, m)
	}
	if !strings.Contains(e.logs.String(), "failed login attempt") || !strings.Contains(e.logs.String(), "9.9.9.9") {
		t.Fatal("attempt not audited")
	}
	if strings.Contains(e.logs.String(), "wrong password here") || strings.Contains(e.logs.String(), goodPassword) {
		t.Fatal("password leaked into logs")
	}
}

func TestLockoutAfterFiveFailures(t *testing.T) {
	e := newEnv(t, ModeStandalone)
	_ = e.svc.SetPassword(goodPassword)
	for i := 0; i < 5; i++ {
		w, _ := e.do("POST", BasePath+"/login", "", `{"password":"bad bad bad bad"}`, "1.2.3.4:1")
		if w.Code != 401 {
			t.Fatalf("attempt %d: %d", i, w.Code)
		}
	}
	// Correct password is rejected during the lock.
	w, _ := e.do("POST", BasePath+"/login", "", `{"password":"`+goodPassword+`"}`, "1.2.3.4:1")
	if w.Code != 429 {
		t.Fatalf("expected lock, got %d", w.Code)
	}
	e.clk.Add(14 * time.Minute)
	if w, _ := e.do("POST", BasePath+"/login", "", `{"password":"`+goodPassword+`"}`, "1.2.3.4:1"); w.Code != 429 {
		t.Fatalf("still locked expected, got %d", w.Code)
	}
	e.clk.Add(2 * time.Minute)
	if w, _ := e.do("POST", BasePath+"/login", "", `{"password":"`+goodPassword+`"}`, "1.2.3.4:1"); w.Code != 200 {
		t.Fatalf("lock should have expired, got %d", w.Code)
	}
}

func TestFailuresOutsideWindowDoNotLock(t *testing.T) {
	e := newEnv(t, ModeStandalone)
	_ = e.svc.SetPassword(goodPassword)
	for i := 0; i < 8; i++ {
		e.do("POST", BasePath+"/login", "", `{"password":"bad bad bad bad"}`, "1.2.3.4:1")
		e.clk.Add(4 * time.Minute)
		// at most 4 failures fit in any 15 minute window
		if i == 3 {
			e.clk.Add(0)
		}
	}
	w, _ := e.do("POST", BasePath+"/login", "", `{"password":"`+goodPassword+`"}`, "1.2.3.4:1")
	if w.Code != 200 {
		t.Fatalf("got %d", w.Code)
	}
}

// M3: failures spread over many sources never lock the operator out; they
// only slow every attempt down. The per-source lockout is unchanged.
func TestGlobalFailuresSlowDownButNeverLockOut(t *testing.T) {
	e := newEnv(t, ModeStandalone)
	_ = e.svc.SetPassword(goodPassword)
	var slept []time.Duration
	e.svc.limiter.sleep = func(_ context.Context, d time.Duration) { slept = append(slept, d) }
	for i := 0; i < 5; i++ {
		e.do("POST", BasePath+"/login", "", `{"password":"bad bad bad bad"}`, "5.5.5."+string(rune('1'+i))+":1")
	}
	if len(slept) != 0 {
		t.Fatalf("slowed down below the threshold: %v", slept)
	}
	w, _ := e.do("POST", BasePath+"/login", "", `{"password":"`+goodPassword+`"}`, "8.8.8.8:1")
	if w.Code != 200 {
		t.Fatalf("operator locked out by failures from other sources: %d", w.Code)
	}
	if len(slept) != 1 || slept[0] != globalSlowDelay {
		t.Fatalf("expected one global delay, got %v", slept)
	}
	// The delay decays with the window.
	e.clk.Add(16 * time.Minute)
	slept = nil
	if w, _ := e.do("POST", BasePath+"/login", "", `{"password":"`+goodPassword+`"}`, "8.8.8.8:1"); w.Code != 200 || len(slept) != 0 {
		t.Fatalf("after the window: %d %v", w.Code, slept)
	}
}

// --- agent tokens ---

func TestCreateListRevokeAndExpiry(t *testing.T) {
	e := newEnv(t, ModeStandalone)
	_ = e.svc.SetPassword(goodPassword)
	op := e.login(t)

	tok, id := e.mkToken(t, op, "paper")
	p, err := e.svc.CheckToken(tok)
	if err != nil || p.IsOperator || p.Profile != "paper" {
		t.Fatalf("%+v %v", p, err)
	}
	if d := p.ExpiresAt.Sub(e.clk.Now()); d < 89*24*time.Hour || d > 91*24*time.Hour {
		t.Fatalf("default expiry %v", d)
	}

	// listing never contains values
	w, _ := e.do("GET", BasePath+"/tokens", op, "", "")
	if w.Code != 200 || strings.Contains(w.Body.String(), tok) || strings.Contains(w.Body.String(), "eyJ") {
		t.Fatalf("list leaked value: %s", w.Body.String())
	}
	var lst struct {
		Tokens []AgentTokenInfo `json:"tokens"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &lst)
	if len(lst.Tokens) != 1 || lst.Tokens[0].Name != "bot" || lst.Tokens[0].ID != id || lst.Tokens[0].LastUsedAt == nil {
		// last used was recorded by CheckToken above
		t.Fatalf("%+v", lst.Tokens)
	}

	// out of range expiry
	w, _ = e.do("POST", BasePath+"/tokens", op, `{"name":"x","profile":"read","expires_in_days":366}`, "")
	if w.Code != 400 {
		t.Fatalf("366 days accepted: %d", w.Code)
	}
	w, _ = e.do("POST", BasePath+"/tokens", op, `{"name":"x","profile":"read","expires_in_days":365}`, "")
	if w.Code != 201 {
		t.Fatalf("365 days rejected: %d", w.Code)
	}

	// expired token
	e.clk.Add(91 * 24 * time.Hour)
	if _, err := e.svc.CheckToken(tok); err == nil {
		t.Fatal("expired token accepted")
	}
}

func TestRevocationImmediateLocalAndUnder5sCrossProcess(t *testing.T) {
	e := newEnv(t, ModeStandalone)
	_ = e.svc.SetPassword(goodPassword)
	op := e.login(t)
	tok, id := e.mkToken(t, op, "live")

	// Second service instance (another process) sharing DB and data dir.
	other, err := New(Config{Mode: ModeStandalone, DataDir: e.dir, DB: e.db, Now: e.clk.Now})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := other.CheckToken(tok); err != nil {
		t.Fatal(err)
	}

	w, _ := e.do("DELETE", BasePath+"/tokens/"+id, op, "", "")
	if w.Code != 200 {
		t.Fatalf("revoke: %d", w.Code)
	}
	if _, err := e.svc.CheckToken(tok); err == nil {
		t.Fatal("local revoke not immediate")
	}
	// Other process: the background loop reloads every 2 s (< 5 s).
	if RefreshInterval >= 5*time.Second {
		t.Fatal("refresh interval too long")
	}
	other.Start(t.Context())
	defer other.Close()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := other.CheckToken(tok); err != nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("revocation not effective within 5 s")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestRevokeUnknownIs404(t *testing.T) {
	e := newEnv(t, ModeStandalone)
	_ = e.svc.SetPassword(goodPassword)
	op := e.login(t)
	if w, _ := e.do("DELETE", BasePath+"/tokens/nope", op, "", ""); w.Code != 404 {
		t.Fatal(w.Code)
	}
}

func TestAgentTokenCannotManageTokens(t *testing.T) {
	e := newEnv(t, ModeStandalone)
	_ = e.svc.SetPassword(goodPassword)
	op := e.login(t)
	for _, prof := range []string{"read", "paper", "live"} {
		agent, id := e.mkToken(t, op, prof)
		for _, c := range [][3]string{
			{"POST", "/tokens", `{"name":"evil","profile":"live"}`},
			{"GET", "/tokens", ""},
			{"DELETE", "/tokens/" + id, ""},
		} {
			w, m := e.do(c[0], BasePath+c[1], agent, c[2], "")
			if w.Code != 403 || m["code"] != "OPERATOR_REQUIRED" {
				t.Fatalf("%s %s with %s agent: %d", c[0], c[1], prof, w.Code)
			}
		}
	}
	if w, _ := e.do("GET", BasePath+"/tokens", "", "", ""); w.Code != 401 {
		t.Fatal("anonymous accepted")
	}
}

func TestLastUsedCoalesced(t *testing.T) {
	e := newEnv(t, ModeStandalone)
	_ = e.svc.SetPassword(goodPassword)
	op := e.login(t)
	tok, id := e.mkToken(t, op, "read")
	read := func() time.Time {
		var lu sql.NullTime
		if err := e.db.QueryRow(`SELECT last_used_at FROM agent_tokens WHERE jti=?`, id).Scan(&lu); err != nil {
			t.Fatal(err)
		}
		return lu.Time
	}
	_, _ = e.svc.CheckToken(tok)
	first := read()
	if first.IsZero() {
		t.Fatal("last_used not set")
	}
	e.clk.Add(30 * time.Second)
	_, _ = e.svc.CheckToken(tok)
	if !read().Equal(first) {
		t.Fatal("written again within a minute")
	}
	e.clk.Add(40 * time.Second)
	_, _ = e.svc.CheckToken(tok)
	if read().Equal(first) {
		t.Fatal("not updated after a minute")
	}
}

// --- secret ---

func TestSecretFirstStartPermsAndNoLeak(t *testing.T) {
	e := newEnv(t, ModeStandalone)
	path := filepath.Join(e.dir, secretFile)
	fi, err := os.Stat(path)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("%v %v", fi, err)
	}
	if len(e.svc.Secret()) < 32 {
		t.Fatal("secret too short")
	}
	_ = e.svc.SetPassword(goodPassword)
	op := e.login(t)
	if strings.Contains(e.logs.String(), e.svc.Secret()) || strings.Contains(op, e.svc.Secret()) {
		t.Fatal("secret leaked")
	}
}

func TestLooseSecretPermsAreTightened(t *testing.T) {
	e := newEnv(t, ModeStandalone)
	path := filepath.Join(e.dir, secretFile)
	_ = os.Chmod(path, 0o644)
	if _, err := New(Config{Mode: ModeStandalone, DataDir: e.dir, DB: e.db, Logger: e.svc.log}); err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Stat(path)
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("perm %v", fi.Mode().Perm())
	}
	if !strings.Contains(e.logs.String(), "tightened") {
		t.Fatal("no warning")
	}
}

func TestRestartKeepsTokensAndRotationInvalidates(t *testing.T) {
	e := newEnv(t, ModeStandalone)
	_ = e.svc.SetPassword(goodPassword)
	op := e.login(t)
	agent, _ := e.mkToken(t, op, "live")

	restarted, err := New(Config{Mode: ModeStandalone, DataDir: e.dir, DB: e.db, Now: e.clk.Now})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.CheckToken(op); err != nil {
		t.Fatalf("session lost on restart: %v", err)
	}
	if _, err := restarted.CheckToken(agent); err != nil {
		t.Fatalf("agent token lost on restart: %v", err)
	}

	if err := RotateSecret(e.dir); err != nil {
		t.Fatal(err)
	}
	_ = restarted.Refresh()
	if _, err := restarted.CheckToken(op); err == nil {
		t.Fatal("session valid after rotation")
	}
	if _, err := restarted.CheckToken(agent); err == nil {
		t.Fatal("agent token valid after rotation")
	}
	// password remains
	if !restarted.PasswordSet() {
		t.Fatal("rotation dropped the password")
	}
}

// --- compatibility with the existing validator ---

func TestTokensAcceptedByExistingValidator(t *testing.T) {
	e := newEnv(t, ModeStandalone)
	_ = e.svc.SetPassword(goodPassword)
	op := e.login(t)
	agent, _ := e.mkToken(t, op, "paper")
	for name, tok := range map[string]string{"operator": op, "agent": agent} {
		req := httptest.NewRequest("GET", "/x", nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		claims, level, err := tradingmw.AuthenticateHTTPRequest(req, e.svc.Secret(), "")
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if claims.UserID == "" || claims.TenantID != "local" || level < 1 {
			t.Fatalf("%s: %+v %d", name, claims, level)
		}
		// wrong secret is rejected
		if _, _, err := tradingmw.AuthenticateHTTPRequest(req, "not-the-secret-not-the-secret-00", ""); err == nil {
			t.Fatalf("%s accepted with wrong secret", name)
		}
	}
}

func TestClaimsShape(t *testing.T) {
	e := newEnv(t, ModeStandalone)
	_ = e.svc.SetPassword(goodPassword)
	op := e.login(t)
	agent, _ := e.mkToken(t, op, "read")
	pOp, _ := e.svc.CheckToken(op)
	pAg, _ := e.svc.CheckToken(agent)
	if pOp.TokenUse != "operator_session" || pAg.TokenUse != "agent" || pAg.JTI == "" || pOp.JTI == "" {
		t.Fatalf("%+v %+v", pOp, pAg)
	}
}

func TestRunOperatorCommand(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "d")
	f, _ := os.CreateTemp(t.TempDir(), "in")
	f.WriteString("short\n")
	f.Seek(0, 0)
	var out, errb bytes.Buffer
	if err := RunOperatorCommand(dir, []string{"set-password"}, f, &out, &errb); err == nil {
		t.Fatal("short password accepted")
	}
	f2, _ := os.CreateTemp(t.TempDir(), "in")
	f2.WriteString(goodPassword + "\n")
	f2.Seek(0, 0)
	if err := RunOperatorCommand(dir, []string{"set-password"}, f2, &out, &errb); err != nil {
		t.Fatal(err)
	}
	if err := RunOperatorCommand(dir, []string{"rotate-secret"}, nil, &out, &errb); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String()+errb.String(), goodPassword) {
		t.Fatal("password echoed")
	}
}

func TestLiveTokenRequiresLicense(t *testing.T) {
	e := newEnv(t, ModeStandalone)
	allowed := false
	e.svc.liveAllowed = func() bool { return allowed }
	_ = e.svc.SetPassword(goodPassword)
	op := e.login(t)

	w, m := e.do("POST", BasePath+"/tokens", op, `{"name":"bot","profile":"live"}`, "")
	if w.Code != 402 || m["code"] != "LICENSE_REQUIRED" {
		t.Fatalf("live without license: %d %s", w.Code, w.Body.String())
	}
	if _, m := e.do("GET", BasePath+"/tokens", op, "", ""); len(m["tokens"].([]any)) != 0 {
		t.Fatalf("a token was stored: %v", m)
	}
	// paper and read are never gated by the license
	e.mkToken(t, op, "paper")
	e.mkToken(t, op, "read")

	allowed = true
	e.mkToken(t, op, "live")
}

// The CLI sets the password from another process: a login right after it must
// succeed without waiting for the background refresh, and the session must
// survive that refresh (it carries the new epoch).
func TestLoginRightAfterCLISetPassword(t *testing.T) {
	e := newEnv(t, ModeStandalone)
	f, _ := os.CreateTemp(t.TempDir(), "in")
	f.WriteString(goodPassword + "\n")
	f.Seek(0, 0)
	var out, errb bytes.Buffer
	if err := RunOperatorCommand(e.dir, []string{"set-password"}, f, &out, &errb); err != nil {
		t.Fatal(err)
	}
	op := e.login(t)
	if err := e.svc.Refresh(); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.CheckToken(op); err != nil {
		t.Fatalf("session minted right after set-password was invalidated: %v", err)
	}
}
