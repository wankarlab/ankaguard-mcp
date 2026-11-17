//go:build product

package main

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/thrasher-corp/gocryptotrader/gctrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

// Boot test of the product binary (OpenSpec guarded-mcp-product, task 8.7).
//
// It compiles the server with the product tag, starts it in a temporary
// directory against a fake GoCryptoTrader (TLS gRPC serving tickers), and
// checks over HTTP that it is healthy, lists only product tools, serves the
// paper broker and refuses real orders. Postgres is optional: with no Docker
// daemon the test runs without it (the server logs that it is missing); set
// PRODUCT_BOOT_PG_HOST and TRADING_CONFIG_DB_PASSWORD to include it.
//
// It uses only free ephemeral ports, never 10600, 8081, 9090 or 9052.

type fakeGCT struct {
	gctrpc.UnimplementedGoCryptoTraderServiceServer
}

func (fakeGCT) GetTicker(_ context.Context, _ *gctrpc.GetTickerRequest) (*gctrpc.TickerResponse, error) {
	return &gctrpc.TickerResponse{Last: 50000, Bid: 49990, Ask: 50010, High: 51000, Low: 49000, Volume: 1234}, nil
}

func startFakeGCT(t *testing.T) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "fake-gct"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert := tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer(grpc.Creds(credentials.NewServerTLSFromCert(&cert)))
	gctrpc.RegisterGoCryptoTraderServiceServer(srv, fakeGCT{})
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	return lis.Addr().String()
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

type bootEnv struct {
	mcp, rest string // base URLs
	out       *bytes.Buffer
}

func (b *bootEnv) do(t *testing.T, method, url, token string, body any) (int, map[string]any) {
	t.Helper()
	var rd io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		rd = bytes.NewReader(raw)
	}
	req, _ := http.NewRequest(method, url, rd)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return resp.StatusCode, out
}

func (b *bootEnv) rpc(t *testing.T, token, method string, params any) map[string]any {
	t.Helper()
	_, out := b.do(t, "POST", b.mcp+"/mcp", token, map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	return out
}

// productBinary compiles the product binary once per test run.
var (
	builtBin  string
	buildOnce sync.Once
	buildErr  error
)

func productBinary(t *testing.T) string {
	t.Helper()
	buildOnce.Do(func() {
		dir, err := os.MkdirTemp("", "product-boot-bin")
		if err != nil {
			buildErr = err
			return
		}
		builtBin = filepath.Join(dir, "trading-mcp-server-product")
		cmd := exec.Command("go", "build", "-tags", "product", "-o", builtBin, "./cmd/trading-mcp-server")
		cmd.Dir, _ = filepath.Abs("../..")
		if out, err := cmd.CombinedOutput(); err != nil {
			buildErr = fmt.Errorf("%w\n%s", err, out)
		}
	})
	if buildErr != nil {
		t.Fatalf("building the product binary: %v", buildErr)
	}
	return builtBin
}

const operatorPassword = "boot-test-password-1"

// startProduct sets the operator password, starts the product binary in a
// temporary directory against the given GoCryptoTrader address and returns
// its URLs and log. The process is killed at the end of the test.
func startProduct(t *testing.T, gctAddr string) *bootEnv {
	t.Helper()
	bin := productBinary(t)
	tmp := t.TempDir()
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}

	// Derived config: the shipped product config with ephemeral ports.
	raw, err := os.ReadFile(filepath.Join(repo, "configs", "product", "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	mcpPort, restPort, gwPort, gwMetrics := freePort(t), freePort(t), freePort(t), freePort(t)
	repl := []string{}
	if h := os.Getenv("PRODUCT_BOOT_PG_HOST"); h != "" {
		repl = append(repl, `host: ""`, fmt.Sprintf("host: %q", h))
	}
	cfgText := strings.NewReplacer(append(repl,
		"port: 10600", fmt.Sprintf("port: %d", mcpPort),
		`port: "8081"`, fmt.Sprintf(`port: "%d"`, restPort))...,
	).Replace(string(raw))
	// The server resolves migrations/ relative to its working directory, as
	// the image does under /app.
	if err := os.Symlink(filepath.Join(repo, "migrations"), filepath.Join(tmp, "migrations")); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(tmp, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(cfgText), 0o600); err != nil {
		t.Fatal(err)
	}

	env := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + tmp,
		fmt.Sprintf("GATEWAY_PORT=%d", gwPort), fmt.Sprintf("GATEWAY_METRICS_PORT=%d", gwMetrics),
		"AUTH_DATA_DIR=" + filepath.Join(tmp, "auth"),
	}
	if gctAddr != "" {
		env = append(env, "GCT_ADDRESS="+gctAddr, "GCT_GRPC_ADDRESS="+gctAddr, "GCT_USERNAME=boot", "GCT_PASSWORD=boot")
	}
	if os.Getenv("PRODUCT_BOOT_PG_HOST") != "" {
		env = append(env, "TRADING_CONFIG_DB_PASSWORD="+os.Getenv("TRADING_CONFIG_DB_PASSWORD"))
	}

	// Operator password through the subcommand (no server running yet).
	pwFile := filepath.Join(tmp, "pw")
	if err := os.WriteFile(pwFile, []byte(operatorPassword+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	stdin, _ := os.Open(pwFile)
	defer stdin.Close()
	setPw := exec.Command(bin, "-config", cfgPath, "operator", "set-password")
	setPw.Dir, setPw.Env, setPw.Stdin = tmp, env, stdin
	if out, err := setPw.CombinedOutput(); err != nil {
		t.Fatalf("operator set-password: %v\n%s", err, out)
	}

	logs := &bytes.Buffer{}
	srv := exec.Command(bin, "-config", cfgPath)
	srv.Dir, srv.Env = tmp, env
	srv.Stdout, srv.Stderr = logs, logs
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = srv.Process.Kill()
		_ = srv.Wait()
		if t.Failed() {
			t.Logf("server log:\n%s", logs.String())
		}
	})
	return &bootEnv{mcp: fmt.Sprintf("http://127.0.0.1:%d", mcpPort), rest: fmt.Sprintf("http://127.0.0.1:%d", restPort), out: logs}
}

func waitHealthy(t *testing.T, b *bootEnv) map[string]any {
	t.Helper()
	var health map[string]any
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		if resp, err := http.Get(b.mcp + "/health"); err == nil {
			raw, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode == 200 && json.Unmarshal(raw, &health) == nil && health["status"] == "ok" {
				return health
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("the server did not become healthy: %v", health)
	return nil
}

// TestProductBinaryBootsWithoutGoCryptoTrader checks that a missing GCT is not
// fatal and is reported with a clear message (task 8.4).
func TestProductBinaryBootsWithoutGoCryptoTrader(t *testing.T) {
	if testing.Short() {
		t.Skip("boot test compiles and starts the binary")
	}
	b := startProduct(t, "127.0.0.1:"+fmt.Sprint(freePort(t))) // nothing listens there
	health := waitHealthy(t, b)
	if health["status"] != "ok" {
		t.Fatalf("unhealthy: %v", health)
	}
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) && !strings.Contains(b.out.String(), "GoCryptoTrader is not reachable") {
		time.Sleep(500 * time.Millisecond)
	}
	if !strings.Contains(b.out.String(), "GoCryptoTrader is not reachable") {
		t.Errorf("the log must say that GoCryptoTrader is unreachable")
	}
	if os.Getenv("PRODUCT_BOOT_PG_HOST") == "" && !strings.Contains(b.out.String(), "Postgres is not configured") {
		t.Errorf("the log must say that Postgres is not configured")
	}
}

func TestProductBinaryBoot(t *testing.T) {
	if testing.Short() {
		t.Skip("boot test compiles and starts the binary")
	}
	b := startProduct(t, startFakeGCT(t))

	// 1. Healthy, and the unauthenticated health reveals only status/version/edition.
	health := waitHealthy(t, b)
	for _, k := range []string{"version", "commit", "edition"} {
		if _, ok := health[k]; !ok {
			t.Errorf("/health misses %q: %v", k, health)
		}
	}
	if len(health) != 4 {
		t.Errorf("/health must only expose status, version, commit and edition: %v", health)
	}

	// 2. Operator session and agent tokens.
	status, login := b.do(t, "POST", b.rest+"/api/v1/auth/local/login", "", map[string]any{"password": operatorPassword})
	op, _ := login["token"].(string)
	if status != 200 || op == "" {
		t.Fatalf("operator login: %d %v", status, login)
	}
	mint := func(profile string) (int, string, map[string]any) {
		st, out := b.do(t, "POST", b.rest+"/api/v1/auth/local/tokens", op, map[string]any{"name": "boot-" + profile, "profile": profile, "expires_in_days": 1})
		tok, _ := out["token"].(string)
		return st, tok, out
	}
	st, readTok, out := mint("read")
	if st != 201 || readTok == "" {
		t.Fatalf("read token: %d %v", st, out)
	}
	st, paperTok, out := mint("paper")
	if st != 201 || paperTok == "" {
		t.Fatalf("paper token: %d %v", st, out)
	}

	// 3. A live token cannot be created without a Pro license (real orders are out of reach).
	if st, _, out := mint("live"); st != 402 {
		t.Errorf("live token creation must be refused without a license, got %d %v", st, out)
	}

	// 4. tools/list: product tools only, filtered by profile.
	listNames := func(token string) map[string]bool {
		list := b.rpc(t, token, "tools/list", map[string]any{})
		res, _ := list["result"].(map[string]any)
		tools, _ := res["tools"].([]any)
		names := map[string]bool{}
		for _, tl := range tools {
			if m, ok := tl.(map[string]any); ok {
				names[fmt.Sprint(m["name"])] = true
			}
		}
		if len(names) < 30 {
			t.Fatalf("tools/list returned only %d tools: %v", len(names), list)
		}
		return names
	}
	readNames, paperNames := listNames(readTok), listNames(paperTok)
	if !readNames["get_ticker"] || readNames["submit_market_order"] {
		t.Errorf("the read profile must list read tools only (get_ticker yes, submit_market_order no)")
	}
	if !paperNames["submit_market_order"] {
		t.Errorf("the paper profile must list the order tools")
	}
	t.Logf("product tools: %d (read profile %d)", len(paperNames), len(readNames))
	for n := range paperNames {
		for _, banned := range []string{"scalping", "launch_", "llm_", "master_", "ai_bot", "tournament", "xsec"} {
			if strings.Contains(n, banned) {
				t.Errorf("tool %q does not belong to the product", n)
			}
		}
	}

	// 5. A read token cannot place orders.
	args := map[string]any{"name": "submit_market_order", "arguments": map[string]any{"exchange": "kucoin", "pair": "BTC-USDT", "side": "buy", "amount": 0.00002}}
	denied := b.rpc(t, readTok, "tools/call", args)
	if e, _ := denied["error"].(map[string]any); e == nil || fmt.Sprint(e["data"]) == "" || !strings.Contains(fmt.Sprint(e["data"]), "PERMISSION_DENIED") {
		t.Errorf("a read token must be denied, got %v", denied)
	}

	// 6. The paper profile fills against the fake GCT price and never reaches the exchange.
	filled := b.rpc(t, paperTok, "tools/call", args)
	if filled["error"] != nil || !strings.Contains(fmt.Sprint(filled["result"]), "paper") {
		t.Errorf("paper order failed: %v", filled)
	}

	// 7. The per-order cap applies to market orders even without Postgres
	// (registry defaults): a $5 order is over the $2 default cap.
	big := map[string]any{"name": "submit_market_order", "arguments": map[string]any{"exchange": "kucoin", "pair": "BTC-USDT", "side": "buy", "amount": 0.0001}}
	if over := b.rpc(t, paperTok, "tools/call", big); !strings.Contains(fmt.Sprint(over["error"]), "ORDER_CAP_EXCEEDED") {
		t.Errorf("an order over the cap must be rejected, got %v", over)
	}
}
