package main

import (
	"strings"
	"testing"

	"github.com/rantipay/trading-mcp/internal/edition"
	"github.com/sirupsen/logrus"
	logtest "github.com/sirupsen/logrus/hooks/test"
)

func TestJWTAuthGuard(t *testing.T) {
	cases := []struct {
		name                 string
		jwt, realMoney, live bool
		wantFatal, wantMsg   bool
	}{
		{"jwt on", true, true, true, false, false},
		{"jwt off, real money, live", false, true, true, true, true},
		{"jwt off, sandbox", false, false, true, false, true},
		{"jwt off, real money, live disabled", false, true, false, false, true},
	}
	for _, c := range cases {
		fatal, msg := jwtAuthGuard(c.jwt, c.realMoney, c.live)
		if fatal != c.wantFatal || (msg != "") != c.wantMsg {
			t.Errorf("%s: fatal=%v msg=%q", c.name, fatal, msg)
		}
	}
}

func TestHTTPListenAddr(t *testing.T) {
	if got := httpListenAddr("127.0.0.1", "8081"); got != "127.0.0.1:8081" {
		t.Errorf("got %q", got)
	}
}

// Escenario "Exposición explícita": una dirección de escucha no local deja un
// aviso que nombra el puerto; la dirección local no deja ninguno.
func TestWarnIfExposed(t *testing.T) {
	cases := []struct {
		name, bind string
		wantWarn   bool
	}{
		{"loopback", "127.0.0.1", false},
		{"localhost", "localhost", false},
		{"all interfaces", "0.0.0.0", true},
		{"empty means all interfaces", "", true},
		{"lan address", "192.168.1.8", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			log, hook := logtest.NewNullLogger()
			warnIfExposed(log, "rest", "10600", c.bind)
			entries := hook.AllEntries()
			if !c.wantWarn {
				if len(entries) != 0 {
					t.Fatalf("local bind %q must not warn, got %v", c.bind, entries[0].Message)
				}
				return
			}
			if len(entries) != 1 || entries[0].Level != logrus.WarnLevel {
				t.Fatalf("bind %q must log exactly one warning, got %d", c.bind, len(entries))
			}
			if entries[0].Data["port"] != "10600" || entries[0].Data["listener"] != "rest" {
				t.Errorf("the warning must name the exposed port: %v", entries[0].Data)
			}
		})
	}
}

// Escenario "Consulta de versión": -version imprime versión, commit y edición.
func TestVersionLineCarriesVersionCommitAndEdition(t *testing.T) {
	line := versionLine()
	for _, k := range []string{"version=", "commit=", "edition="} {
		if !strings.Contains(line, k) {
			t.Errorf("version line %q misses %q", line, k)
		}
	}
	if !strings.Contains(line, "edition="+edition.Build()) {
		t.Errorf("version line %q does not report the build edition %q", line, edition.Build())
	}
}
