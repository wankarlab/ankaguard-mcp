package config

import (
	"os"
	"path/filepath"
	"testing"
)

const baseYAML = `
server:
  host: "0.0.0.0"
  port: 10600
  mode: sandbox
  protocol: http
gocryptotrader:
  grpc_address: "localhost:9052"
  username: u
  password: p
risk_limits:
  max_order_size_usd: 2
  max_daily_loss_usd: 5
logging:
  level: info
`

func writeCfg(t *testing.T, extra string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte(baseYAML+extra), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// With a config that says nothing new, the deployment keeps today's behaviour:
// external auth, the existing host, live trading left to the registry default.
func TestDefaultsKeepCurrentBehaviour(t *testing.T) {
	t.Setenv("AUTH_MODE", "")
	t.Setenv("MCP_BIND_ADDRESS", "")
	cfg, err := Load(writeCfg(t, ""))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Auth.Mode != AuthModeExternal {
		t.Errorf("auth.mode = %q, want external", cfg.Auth.Mode)
	}
	if cfg.Server.BindAddress != "" || cfg.ListenHost() != "0.0.0.0" {
		t.Errorf("bind = %q listen = %q: the default must keep the current host", cfg.Server.BindAddress, cfg.ListenHost())
	}
	if cfg.Trading.LiveEnabled != nil {
		t.Error("trading.live_enabled must be unset by default (the registry default applies)")
	}
}

func TestBindAddressOverridesHost(t *testing.T) {
	t.Setenv("MCP_BIND_ADDRESS", "127.0.0.1")
	cfg, err := Load(writeCfg(t, ""))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ListenHost() != "127.0.0.1" || !IsLocalBind(cfg.ListenHost()) {
		t.Fatalf("ListenHost = %q", cfg.ListenHost())
	}
}

func TestAuthModeValidation(t *testing.T) {
	t.Setenv("AUTH_MODE", "standalone")
	cfg, err := Load(writeCfg(t, ""))
	if err != nil || cfg.Auth.Mode != AuthModeStandalone {
		t.Fatalf("standalone: %v %v", cfg, err)
	}
	t.Setenv("AUTH_MODE", "bogus")
	if _, err := Load(writeCfg(t, "")); err == nil {
		t.Fatal("an unknown auth.mode must be rejected at startup")
	}
}

func TestLoadAuthSettingsNeedsNoValidConfig(t *testing.T) {
	t.Setenv("AUTH_MODE", "")
	t.Setenv("AUTH_DATA_DIR", "")
	// A file that would fail Validate (no GCT credentials) still yields the settings.
	p := filepath.Join(t.TempDir(), "c.yaml")
	if err := os.WriteFile(p, []byte("auth:\n  mode: standalone\n  data_dir: /var/lib/x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	st := LoadAuthSettings(p)
	if st.Mode != "standalone" || st.DataDir != "/var/lib/x" {
		t.Fatalf("settings = %+v", st)
	}
	st = LoadAuthSettings(filepath.Join(t.TempDir(), "missing.yaml"))
	if st.Mode != "external" || st.DataDir != "data/auth" {
		t.Fatalf("defaults = %+v", st)
	}
}

func TestIsLocalBind(t *testing.T) {
	for host, want := range map[string]bool{
		"127.0.0.1": true, "localhost": true, "::1": true, "[::1]": true,
		"": false, "0.0.0.0": false, "::": false, "192.168.1.8": false, "10.0.0.2": false,
	} {
		if got := IsLocalBind(host); got != want {
			t.Errorf("IsLocalBind(%q) = %v, want %v", host, got, want)
		}
	}
}

// Escenarios "Configuración por defecto" e "Instalación por defecto y token
// real": el config.yaml que se instala con el producto escucha solo en local,
// usa autenticación local y deja el dinero real deshabilitado.
func TestShippedProductConfigIsLocalAndLiveDisabled(t *testing.T) {
	t.Setenv("AUTH_MODE", "")
	t.Setenv("MCP_BIND_ADDRESS", "")
	t.Setenv("GCT_USERNAME", "u")
	t.Setenv("GCT_PASSWORD", "p")
	cfg, err := Load(filepath.Join("..", "..", "..", "configs", "product", "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !IsLocalBind(cfg.ListenHost()) {
		t.Errorf("product config listens on %q: not local", cfg.ListenHost())
	}
	if cfg.Auth.Mode != AuthModeStandalone {
		t.Errorf("product config auth.mode = %q, want standalone", cfg.Auth.Mode)
	}
	if cfg.Trading.LiveEnabled == nil || *cfg.Trading.LiveEnabled {
		t.Errorf("product config must set trading.live_enabled: false, got %v", cfg.Trading.LiveEnabled)
	}
}
