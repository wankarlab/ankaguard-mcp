package license

import (
	"crypto/ed25519"
	"crypto/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var t0 = time.Date(2027, 1, 10, 12, 0, 0, 0, time.UTC)

type clock struct{ now time.Time }

func (c *clock) Now() time.Time { return c.now }

func keypair(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return pub, priv
}

func issue(t *testing.T, priv ed25519.PrivateKey, edition string, expires time.Time) string {
	t.Helper()
	doc, err := Sign(Payload{ID: "lic-1", Holder: "ACME", Edition: edition, IssuedAt: expires.AddDate(-1, 0, 0), ExpiresAt: expires}, priv)
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

func newMgr(t *testing.T, pub ed25519.PublicKey, c *clock, dir string) *Manager {
	t.Helper()
	return NewManager(Options{
		Verifier: NewVerifierWithKey(pub), DataDir: dir, Now: c.Now,
		ProBuild: func() bool { return true },
	})
}

func warn(s LicenseStatus) string {
	if s.Warning == nil {
		return ""
	}
	return *s.Warning
}

func TestParseDocuments(t *testing.T) {
	pub, priv := keypair(t)
	otherPub, otherPriv := keypair(t)
	_ = otherPub
	v := NewVerifierWithKey(pub)
	good := issue(t, priv, EditionPro, t0.AddDate(1, 0, 0))

	if p, err := v.Parse(good); err != nil || p.Holder != "ACME" || p.Edition != "pro" {
		t.Fatalf("valid doc rejected: %v %+v", err, p)
	}
	// Tampered payload: swap the payload part for another signed with the same key.
	parts := strings.Split(good, ".")
	other := strings.Split(issue(t, priv, EditionCommunity, t0.AddDate(5, 0, 0)), ".")
	tampered := other[0] + "." + parts[1]
	if _, err := v.Parse(tampered); Reason(err) != ReasonBadSignature {
		t.Fatalf("tampered: got %v", err)
	}
	// Signed with another key.
	foreign := issue(t, otherPriv, EditionPro, t0.AddDate(1, 0, 0))
	if _, err := v.Parse(foreign); Reason(err) != ReasonBadSignature {
		t.Fatalf("foreign key: got %v", err)
	}
	// Unreadable.
	for _, junk := range []string{"", "garbage", "a.b.c", "!!!.???", good[:len(good)-4]} {
		if _, err := v.Parse(junk); Reason(err) != ReasonMalformed {
			t.Fatalf("junk %q: got %v", junk, err)
		}
	}
	// Placeholder key verifier cannot accept anything.
	if _, err := NewVerifierWithKey(make([]byte, 32)).Parse(good); Reason(err) != ReasonWrongKey {
		t.Fatalf("placeholder: got %v", err)
	}
}

func TestSignRejectsBadInput(t *testing.T) {
	_, priv := keypair(t)
	if _, err := Sign(Payload{ID: "x", Holder: "h", Edition: "pro", IssuedAt: t0, ExpiresAt: t0}, priv); err == nil {
		t.Fatal("zero-length validity must be rejected")
	}
	if _, err := Sign(Payload{ID: "x", Holder: "h", Edition: "gold", IssuedAt: t0, ExpiresAt: t0.Add(time.Hour)}, priv); err == nil {
		t.Fatal("unknown edition must be rejected")
	}
}

func TestPlaceholderKeyDetected(t *testing.T) {
	// The release check relies on an all-zero key being flagged.
	if !IsPlaceholderKey(make(ed25519.PublicKey, ed25519.PublicKeySize)) {
		t.Fatal("all-zero key not flagged as placeholder")
	}
	if IsPlaceholderKey(EmbeddedPublicKey()) {
		t.Fatal("the embedded release key is flagged as placeholder")
	}
	pub, _ := keypair(t)
	if IsPlaceholderKey(pub) {
		t.Fatal("real key flagged as placeholder")
	}
}

func TestStates(t *testing.T) {
	pub, priv := keypair(t)
	cases := []struct {
		name       string
		expiresIn  time.Duration
		state      string
		edition    string
		live       bool
		warning    string
		days, grac int
	}{
		{"valid far", 100 * 24 * time.Hour, StateValid, "pro", true, "", 100, 0},
		{"expiring soon", 10 * 24 * time.Hour, StateValid, "pro", true, WarningExpiringSoon, 10, 0},
		{"exactly 14 days", 14 * 24 * time.Hour, StateValid, "pro", true, WarningExpiringSoon, 14, 0},
		{"grace 5 days", -5 * 24 * time.Hour, StateGrace, "pro", true, WarningInGrace, 0, 9},
		{"expired 20 days", -20 * 24 * time.Hour, StateExpired, "community", false, "", 0, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			clk := &clock{t0}
			m := newMgr(t, pub, clk, "")
			if _, err := m.Install(issue(t, priv, EditionPro, t0.Add(c.expiresIn))); err != nil {
				if c.state == StateExpired && Reason(err) == ReasonExpired {
					return // expired beyond grace is refused at install time
				}
				t.Fatal(err)
			}
			s := m.Status()
			if s.State != c.state || s.Edition != c.edition || m.AllowsLive() != c.live || warn(s) != c.warning ||
				s.DaysRemaining != c.days || s.GraceDaysRemaining != c.grac {
				t.Fatalf("unexpected status %+v live=%v", s, m.AllowsLive())
			}
		})
	}
}

func TestExpiredAfterInstallAndNoLicense(t *testing.T) {
	pub, priv := keypair(t)
	clk := &clock{t0}
	m := newMgr(t, pub, clk, "")
	if s := m.Status(); s.State != StateInvalid || s.Edition != "community" || m.AllowsLive() {
		t.Fatalf("no license: %+v", s)
	}
	if _, err := m.Install(issue(t, priv, EditionPro, t0.Add(24*time.Hour))); err != nil {
		t.Fatal(err)
	}
	clk.now = t0.Add(20 * 24 * time.Hour) // 19 days past expiry
	if s := m.Status(); s.State != StateExpired || m.AllowsLive() || s.Edition != "community" {
		t.Fatalf("after expiry: %+v", s)
	}
	// Installing a license already expired beyond grace is refused.
	if _, err := m.Install(issue(t, priv, EditionPro, t0)); Reason(err) != ReasonExpired {
		t.Fatalf("expired install: %v", err)
	}
}

func TestCommunityLicenseAndNonProBuild(t *testing.T) {
	pub, priv := keypair(t)
	clk := &clock{t0}
	m := newMgr(t, pub, clk, "")
	m.Install(issue(t, priv, EditionCommunity, t0.AddDate(1, 0, 0)))
	if m.Status().Edition != "community" || m.AllowsLive() {
		t.Fatal("community license must not grant pro")
	}
	// Pro license on a non-Pro build: effective edition stays community.
	m2 := NewManager(Options{Verifier: NewVerifierWithKey(pub), Now: clk.Now, ProBuild: func() bool { return false }})
	m2.Install(issue(t, priv, EditionPro, t0.AddDate(1, 0, 0)))
	if m2.Status().Edition != "community" || m2.Status().State != StateValid {
		t.Fatalf("non-pro build: %+v", m2.Status())
	}
}

func TestStatusNeverContainsDocument(t *testing.T) {
	pub, priv := keypair(t)
	m := newMgr(t, pub, &clock{t0}, "")
	doc := issue(t, priv, EditionPro, t0.AddDate(1, 0, 0))
	m.Install(doc)
	b, _ := jsonMarshal(m.Status())
	if strings.Contains(string(b), strings.Split(doc, ".")[0]) || strings.Contains(string(b), strings.Split(doc, ".")[1]) {
		t.Fatal("status leaked the license document")
	}
}

func TestClockSkew(t *testing.T) {
	pub, priv := keypair(t)
	dir := t.TempDir()
	clk := &clock{t0}
	m := newMgr(t, pub, clk, dir)
	m.Install(issue(t, priv, EditionPro, t0.AddDate(1, 0, 0)))

	// One hour back: no change.
	clk.now = t0.Add(-time.Hour)
	m.Reverify()
	if s := m.Status(); s.State != StateValid || s.Warning != nil {
		t.Fatalf("small rollback changed status: %+v", s)
	}
	// Three days back: expired + clock_skew, survives a restart (persisted).
	clk.now = t0.Add(-72 * time.Hour)
	m2 := newMgr(t, pub, clk, dir)
	s := m2.Status()
	if s.State != StateExpired || warn(s) != WarningClockSkew || m2.AllowsLive() || s.Edition != "community" {
		t.Fatalf("skew: %+v", s)
	}
	// Reverify while skewed must not move the record, so returning to the
	// real time recovers.
	m2.Reverify()
	clk.now = t0.Add(time.Hour)
	if s := m2.Status(); s.State != StateValid {
		t.Fatalf("recovery: %+v", s)
	}
}

func TestInstallKeepsPreviousOnInvalid(t *testing.T) {
	pub, priv := keypair(t)
	_, otherPriv := keypair(t)
	dir := t.TempDir()
	clk := &clock{t0}
	m := newMgr(t, pub, clk, dir)
	good := issue(t, priv, EditionPro, t0.AddDate(1, 0, 0))
	if _, err := m.Install(good); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Install(issue(t, otherPriv, EditionPro, t0.AddDate(2, 0, 0))); Reason(err) != ReasonBadSignature {
		t.Fatalf("got %v", err)
	}
	if s := m.Status(); s.State != StateValid || *s.LicenseID != "lic-1" {
		t.Fatalf("previous license lost: %+v", s)
	}
	b, _ := os.ReadFile(filepath.Join(dir, licenseFileName))
	if strings.TrimSpace(string(b)) != good {
		t.Fatal("stored document replaced by invalid one")
	}
	info, _ := os.Stat(filepath.Join(dir, licenseFileName))
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("license file mode %v", info.Mode().Perm())
	}
}

func TestHotInstallAndSources(t *testing.T) {
	pub, priv := keypair(t)
	dir := t.TempDir()
	clk := &clock{t0}
	m := newMgr(t, pub, clk, dir)
	if m.AllowsLive() {
		t.Fatal("no license yet")
	}
	if _, err := m.Install(issue(t, priv, EditionPro, t0.AddDate(1, 0, 0))); err != nil || !m.AllowsLive() {
		t.Fatalf("hot install failed: %v", err)
	}
	// Restart: loaded from the data dir.
	if m2 := newMgr(t, pub, clk, dir); !m2.AllowsLive() {
		t.Fatal("stored license not loaded")
	}
	// Env value (older) vs stored (newer): the newer expiry wins.
	older := issue(t, priv, EditionPro, t0.AddDate(0, 1, 0))
	m3 := NewManager(Options{Verifier: NewVerifierWithKey(pub), DataDir: dir, LicenseKey: older, Now: clk.Now, ProBuild: func() bool { return true }})
	if got := *m3.Status().ExpiresAt; got != t0.AddDate(1, 0, 0).Format(time.RFC3339) {
		t.Fatalf("expected stored (newer) license, got %s", got)
	}
	// LICENSE_FILE only.
	f := filepath.Join(t.TempDir(), "lic.txt")
	os.WriteFile(f, []byte(older+"\n"), 0o600)
	m4 := NewManager(Options{Verifier: NewVerifierWithKey(pub), LicenseFile: f, Now: clk.Now, ProBuild: func() bool { return true }})
	if !m4.AllowsLive() {
		t.Fatal("LICENSE_FILE not loaded")
	}
	// LICENSE_KEY only.
	m5 := NewManager(Options{Verifier: NewVerifierWithKey(pub), LicenseKey: older, Now: clk.Now, ProBuild: func() bool { return true }})
	if !m5.AllowsLive() {
		t.Fatal("LICENSE_KEY not loaded")
	}
}
