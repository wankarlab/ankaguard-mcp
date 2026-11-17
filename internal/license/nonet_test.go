package license

import (
	"context"
	"errors"
	"go/parser"
	"go/token"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

type failTransport struct{ t *testing.T }

func (f failTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	f.t.Errorf("license package attempted an HTTP request to %s", r.URL)
	return nil, errors.New("network forbidden")
}

// TestNoNetworkDuringVerificationCycle runs install, status and reverify with
// every default network path replaced by one that fails the test.
func TestNoNetworkDuringVerificationCycle(t *testing.T) {
	oldT, oldR := http.DefaultTransport, net.DefaultResolver
	defer func() { http.DefaultTransport, net.DefaultResolver = oldT, oldR }()
	http.DefaultTransport = failTransport{t}
	net.DefaultResolver = &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, addr string) (net.Conn, error) {
		t.Errorf("license package attempted a network dial to %s %s", network, addr)
		return nil, errors.New("network forbidden")
	}}

	pub, priv := keypair(t)
	clk := &clock{t0}
	m := newMgr(t, pub, clk, t.TempDir())
	if _, err := m.Install(issue(t, priv, EditionPro, t0.AddDate(1, 0, 0))); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		clk.now = clk.now.Add(time.Hour)
		m.Reverify()
		m.Status()
		m.AllowsLive()
	}
}

// TestPackageImportsNoNetwork guards against someone adding net or net/http
// to the core license package; HTTP lives in internal/license/httpapi.
func TestPackageImportsNoNetwork(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, e.Name(), nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range f.Imports {
			p, _ := strconv.Unquote(imp.Path.Value)
			if p == "net" || strings.HasPrefix(p, "net/") {
				t.Errorf("%s imports %s", e.Name(), p)
			}
		}
	}
}
