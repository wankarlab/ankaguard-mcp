package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/rantipay/trading-mcp/internal/killswitch"
	"github.com/rantipay/trading-mcp/internal/xsec"
)

// ---- fakes ----

type fakeRing0Exchange struct {
	mu        sync.Mutex
	positions []Ring0Position
	failClose map[string]bool
	closed    []string
	cancelled int
	cancelN   int
	listErrs  int // ListOpenPositions fails this many times before working
}

func (f *fakeRing0Exchange) ListOpenPositions(ctx context.Context) ([]Ring0Position, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.listErrs > 0 {
		f.listErrs--
		return nil, errors.New("exchange unreachable")
	}
	var out []Ring0Position
	for _, p := range f.positions {
		isClosed := false
		for _, c := range f.closed {
			if c == p.Symbol {
				isClosed = true
			}
		}
		if !isClosed {
			out = append(out, p)
		}
	}
	return out, nil
}

func (f *fakeRing0Exchange) ClosePosition(ctx context.Context, symbol string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failClose[symbol] {
		return errors.New("exchange rejected close")
	}
	f.closed = append(f.closed, symbol)
	return nil
}

func (f *fakeRing0Exchange) CancelAllOrders(ctx context.Context) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cancelled++
	return f.cancelN, nil
}

type fakeStopper struct {
	name    string
	err     error
	stopped int
}

func (s *fakeStopper) Name() string { return s.name }
func (s *fakeStopper) Stop(ctx context.Context) error {
	s.stopped++
	return s.err
}

type fakeAllStopper struct {
	live, dry int
}

func (b *fakeAllStopper) EmergencyStopAll(ctx context.Context, dryRun bool) ([]string, map[string]string) {
	if dryRun {
		b.dry++
		return []string{"u1", "u2"}, map[string]string{}
	}
	b.live++
	return []string{"u1", "u2"}, map[string]string{"u3": "boom"}
}

type fakeLiveBook struct{ closed []string }

func (b *fakeLiveBook) MarkClosed(symbol string) error {
	b.closed = append(b.closed, symbol)
	return nil
}

// ---- helpers ----

func ring0Router(h *KillSwitchHandler, level int) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("kong_level", level); c.Next() })
	r.POST("/ring/0", h.Ring0)
	r.POST("/ring/0/reset", h.Ring0Reset)
	return r
}

func postJSON(r http.Handler, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func decodeRing0(t *testing.T, w *httptest.ResponseRecorder) ring0Result {
	t.Helper()
	var env struct {
		Data ring0Result `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v body=%s", err, w.Body.String())
	}
	return env.Data
}

func newRing0Fixture(t *testing.T) (*KillSwitchHandler, *fakeRing0Exchange, *fakeStopper, *fakeStopper, *fakeAllStopper, *fakeLiveBook) {
	t.Helper()
	killswitch.SetRing0Active(false)
	t.Cleanup(func() { killswitch.SetRing0Active(false) })
	ex := &fakeRing0Exchange{
		positions: []Ring0Position{
			{Symbol: "QNTUSDTM", Side: "long", Size: 4, UnrealizedPnL: -0.1},
			{Symbol: "ETHUSDTM", Side: "short", Size: 2, UnrealizedPnL: 0.3},
			{Symbol: "SOLUSDTM", Side: "long", Size: 1, UnrealizedPnL: 0},
		},
		failClose: map[string]bool{"ETHUSDTM": true},
		cancelN:   3,
	}
	good := &fakeStopper{name: "ai_trading_bot"}
	bad := &fakeStopper{name: "scalping_engine", err: errors.New("stuck")}
	bots := &fakeAllStopper{}
	book := &fakeLiveBook{}
	h := NewKillSwitchHandler(nil, bots, testLogger())
	h.SetExchange(ex)
	h.SetLiveBook(book)
	h.AddEngine(good)
	h.AddEngine(bad)
	return h, ex, good, bad, bots, book
}

// ---- tests ----

func TestRing0_DryRunChangesNothing(t *testing.T) {
	h, ex, good, bad, bots, book := newRing0Fixture(t)
	r := ring0Router(h, 4)

	w := postJSON(r, "/ring/0", `{"dry_run": true}`) // no confirm needed
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	res := decodeRing0(t, w)
	if res.Status != "dry_run" || !res.DryRun {
		t.Errorf("status=%q dry=%v", res.Status, res.DryRun)
	}
	if killswitch.Ring0Active() {
		t.Error("dry run must not set the ring-0 flag")
	}
	if good.stopped+bad.stopped != 0 || bots.live != 0 || len(ex.closed) != 0 || ex.cancelled != 0 || len(book.closed) != 0 {
		t.Errorf("dry run mutated state: engines=%d/%d botsLive=%d closed=%v cancels=%d book=%v",
			good.stopped, bad.stopped, bots.live, ex.closed, ex.cancelled, book.closed)
	}
	if len(res.Positions) != 3 {
		t.Fatalf("positions = %d, want 3", len(res.Positions))
	}
	for _, p := range res.Positions {
		if p.Result != "would_close" {
			t.Errorf("%s result = %q, want would_close", p.Symbol, p.Result)
		}
	}
	if res.BotsStopped != 2 || len(res.EnginesStopped) != 2 {
		t.Errorf("dry-run preview: bots=%d engines=%v", res.BotsStopped, res.EnginesStopped)
	}
}

func TestRing0_LiveRequiresConfirm(t *testing.T) {
	h, ex, _, _, _, _ := newRing0Fixture(t)
	r := ring0Router(h, 4)
	for _, body := range []string{`{}`, `{"confirm":"nope"}`, ``} {
		w := postJSON(r, "/ring/0", body)
		if w.Code != http.StatusBadRequest {
			t.Errorf("body %q: status = %d, want 400", body, w.Code)
		}
	}
	if killswitch.Ring0Active() || len(ex.closed) != 0 {
		t.Error("unconfirmed request must change nothing")
	}
}

func TestRing0_KYCGate(t *testing.T) {
	h, _, _, _, _, _ := newRing0Fixture(t)
	r := ring0Router(h, 3)
	if w := postJSON(r, "/ring/0", `{"confirm":"CERRAR TODO"}`); w.Code != http.StatusForbidden {
		t.Errorf("ring0 status = %d, want 403", w.Code)
	}
	if w := postJSON(r, "/ring/0/reset", ``); w.Code != http.StatusForbidden {
		t.Errorf("reset status = %d, want 403", w.Code)
	}
}

func TestRing0_LiveClosesEverythingEvenIfOneFails(t *testing.T) {
	h, ex, good, bad, bots, book := newRing0Fixture(t)
	r := ring0Router(h, 4)

	w := postJSON(r, "/ring/0", `{"confirm":"CERRAR TODO"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	res := decodeRing0(t, w)

	if res.Status != "global_stop_active" || !killswitch.Ring0Active() {
		t.Errorf("status=%q flag=%v", res.Status, killswitch.Ring0Active())
	}
	if bots.live != 1 || res.BotsStopped != 2 || res.BotsFailed != 1 {
		t.Errorf("bots: live=%d stopped=%d failed=%d", bots.live, res.BotsStopped, res.BotsFailed)
	}
	// The failing engine must not prevent the others from stopping.
	if good.stopped != 1 || bad.stopped != 1 {
		t.Errorf("engine stops good=%d bad=%d", good.stopped, bad.stopped)
	}
	if len(res.EnginesStopped) != 1 || res.EnginesStopped[0] != "ai_trading_bot" || res.EnginesFailed["scalping_engine"] == "" {
		t.Errorf("engines stopped=%v failed=%v", res.EnginesStopped, res.EnginesFailed)
	}
	if res.OrdersCancelled < 3 || ex.cancelled == 0 {
		t.Errorf("orders cancelled = %d", res.OrdersCancelled)
	}
	// ETH close fails but QNT and SOL must still be closed.
	if res.PositionsClosed != 2 || res.PositionsFailed != 1 {
		t.Errorf("closed=%d failed=%d (%+v)", res.PositionsClosed, res.PositionsFailed, res.Positions)
	}
	got := map[string]string{}
	for _, p := range res.Positions {
		got[p.Symbol] = p.Result
	}
	if got["QNTUSDTM"] != "closed" || got["SOLUSDTM"] != "closed" || got["ETHUSDTM"] != "failed" {
		t.Errorf("per-position results = %v", got)
	}
	if len(book.closed) != 2 {
		t.Errorf("live book marked %v, want the 2 closed symbols", book.closed)
	}
}

func TestRing0_ListFailureIsNotAPosition(t *testing.T) {
	h, ex, _, _, _, _ := newRing0Fixture(t)
	r := ring0Router(h, 4)

	ex.listErrs = 1
	res := decodeRing0(t, postJSON(r, "/ring/0", `{"dry_run": true}`))
	if res.PositionsListError == "" || len(res.Positions) != 0 || res.PositionsFailed != 0 {
		t.Errorf("dry run: list_error=%q positions=%+v failed=%d", res.PositionsListError, res.Positions, res.PositionsFailed)
	}

	// Execution: the first sweep fails, the re-sweep closes everything it can.
	ex.listErrs = 1
	res = decodeRing0(t, postJSON(r, "/ring/0", `{"confirm":"CERRAR TODO"}`))
	if res.PositionsListError != "" {
		t.Errorf("a successful re-sweep must clear the list error, got %q", res.PositionsListError)
	}
	if res.PositionsClosed != 2 || res.PositionsFailed != 1 {
		t.Errorf("closed=%d failed=%d (%+v)", res.PositionsClosed, res.PositionsFailed, res.Positions)
	}
}

func TestRing0_ResetClearsFlagAndGuard(t *testing.T) {
	h, _, _, _, _, _ := newRing0Fixture(t)
	r := ring0Router(h, 4)
	postJSON(r, "/ring/0", `{"confirm":"CERRAR TODO"}`)
	if !killswitch.Ring0Active() {
		t.Fatal("flag should be active after ring 0")
	}

	// Guard blocks while active.
	g := gin.New()
	g.POST("/orders", Ring0Guard(), func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })
	w := postJSON(g, "/orders", `{}`)
	if w.Code != http.StatusLocked || !strings.Contains(w.Body.String(), "KILLSWITCH_ACTIVE") {
		t.Errorf("guard active: status=%d body=%s", w.Code, w.Body.String())
	}

	if w := postJSON(r, "/ring/0/reset", ``); w.Code != http.StatusOK {
		t.Fatalf("reset status = %d body=%s", w.Code, w.Body.String())
	}
	if killswitch.Ring0Active() {
		t.Error("flag still active after reset")
	}
	if w := postJSON(g, "/orders", `{}`); w.Code != http.StatusOK {
		t.Errorf("guard after reset: status=%d", w.Code)
	}
}

func TestLiveBookPairFromSymbol(t *testing.T) {
	cases := map[string]string{"QNTUSDTM": "QNT-USDT", "XBTUSDTM": "BTC-USDT", "QNT-USDT": "QNT-USDT", "ethusdtm": "ETH-USDT"}
	for in, want := range cases {
		if got := liveBookPairFromSymbol(in); got != want {
			t.Errorf("liveBookPairFromSymbol(%q) = %q, want %q", in, got, want)
		}
	}
}

// ---- xsec job runner guard ----

func TestXSecRunner_Ring0Guard(t *testing.T) {
	t.Cleanup(func() { killswitch.SetRing0Active(false) })

	blocked := []string{XSecJobModeRebalanceLive, XSecJobModeRelaunch}
	allowed := []string{
		XSecJobModeCloseAll, XSecJobModeCloseLeg, XSecJobModeNeutralize, XSecJobModeCancelOrder,
		XSecJobModeSyncBook, XSecJobModeRebalanceDry, XSecJobModeBacktest, XSecJobModeAutoconfig,
	}
	for _, m := range blocked {
		if !xsecModeIncreasesRisk(m) {
			t.Errorf("%s should be classified risk-increasing", m)
		}
	}
	for _, m := range allowed {
		if xsecModeIncreasesRisk(m) {
			t.Errorf("%s should NOT be classified risk-increasing", m)
		}
	}

	killswitch.SetRing0Active(true)
	db := newXSecJobsTestDB(t)
	// Never run a real binary from a test: /bin/true ignores its argv.
	runner := NewXSecJobRunner(db, testLogger()).WithBinPath("/bin/true")

	for _, m := range blocked {
		_, err := runner.Start(XSecJobRequest{Mode: m, K: intPtr(5), MaxPosUSD: floatPtr(3.0)}, "u")
		var se *XSecJobStartError
		if !errors.As(err, &se) || se.Status != http.StatusLocked || se.Code != "KILLSWITCH_ACTIVE" {
			t.Errorf("%s: err = %v, want 423 KILLSWITCH_ACTIVE", m, err)
		}
	}

	// De-risking mode passes the guard (the fake binary is a no-op).
	_, err := runner.Start(XSecJobRequest{Mode: XSecJobModeCloseAll}, "u")
	var se *XSecJobStartError
	if errors.As(err, &se) && se.Code == "KILLSWITCH_ACTIVE" {
		t.Errorf("close-all must be allowed while ring 0 is active, got %v", err)
	}
	// Let the async no-op job finish before the DB is closed.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, running, _ := xsec.AnyJobRunning(db); !running {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
}
