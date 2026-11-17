package launch

import (
	"strings"
	"testing"
)

func envFrom(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func validProdEnv() map[string]string {
	return map[string]string{
		"ENVIRONMENT":                     "production",
		"LAUNCH_DATABASE_URL":             "postgres://launch@db:5432/launch",
		"LAUNCH_RPC_PRIMARY_URL":          "https://mainnet.helius-rpc.com/?api-key=x",
		"LAUNCH_RPC_SECONDARY_URL":        "https://example.solana-mainnet.quiknode.pro/x/",
		"LAUNCH_SIGNER_BACKEND":           "local",
		"LAUNCH_SIGNER_SOCKET":            "/run/launch-signer/signer.sock",
		"LAUNCH_SIGNER_MAX_TX_LAMPORTS":   "100000000",
		"LAUNCH_SIGNER_PROGRAM_ALLOWLIST": strings.Join(DefaultProgramAllowlist, ","),
		"LAUNCH_TELEGRAM_TOKEN":           "123:abc",
		"LAUNCH_SOURCE_PRIMARY_WS_URL":    "wss://stream.example.com/api/data",
		"LAUNCH_SOURCE_SECONDARY_URL":     "https://pools.example.com",
		"LAUNCH_QUOTE_API_URL":            "https://quote.example.com/v1",
	}
}

func TestLoadEnv_DevelopmentDefaults(t *testing.T) {
	env, err := LoadEnv(envFrom(map[string]string{"LAUNCH_DATABASE_URL": "postgres://localhost/x"}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if env.Environment != EnvDevelopment {
		t.Errorf("environment = %q, want development", env.Environment)
	}
	if env.RPCPrimaryURL != devRPCPrimaryURL || env.SignerBackend != SignerBackendLocal || env.CommandBus != CommandBusPostgres {
		t.Errorf("unexpected dev defaults: %+v", env)
	}
	if env.SignerMaxTxLamports != devSignerMaxTxLamports || len(env.SignerProgramAllowlist) != len(DefaultProgramAllowlist) {
		t.Errorf("unexpected signer defaults: %+v", env)
	}
}

func TestLoadEnv_DatabaseAlwaysRequired(t *testing.T) {
	if _, err := LoadEnv(envFrom(nil)); err == nil || !strings.Contains(err.Error(), "LAUNCH_DATABASE_URL") {
		t.Fatalf("expected LAUNCH_DATABASE_URL error, got %v", err)
	}
}

func TestLoadEnv_ProductionValid(t *testing.T) {
	env, err := LoadEnv(envFrom(validProdEnv()))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if env.Environment != EnvProduction || env.SignerMaxTxLamports != 100_000_000 {
		t.Errorf("unexpected env: %+v", env)
	}
}

func TestLoadEnv_ProductionRequiresEverything(t *testing.T) {
	_, err := LoadEnv(envFrom(map[string]string{"ENVIRONMENT": "production"}))
	if err == nil {
		t.Fatal("expected error")
	}
	for _, key := range []string{
		"LAUNCH_DATABASE_URL", "LAUNCH_RPC_PRIMARY_URL", "LAUNCH_RPC_SECONDARY_URL",
		"LAUNCH_SIGNER_BACKEND", "LAUNCH_SIGNER_SOCKET", "LAUNCH_SIGNER_MAX_TX_LAMPORTS",
		"LAUNCH_SIGNER_PROGRAM_ALLOWLIST", "LAUNCH_TELEGRAM_TOKEN",
	} {
		if !strings.Contains(err.Error(), key) {
			t.Errorf("error does not mention %s: %v", key, err)
		}
	}
	// Missing values are reported once as missing, never validated as dev defaults.
	for _, noise := range []string{"devnet", "/tmp", "must be local"} {
		if strings.Contains(err.Error(), noise) {
			t.Errorf("error validates a development default (%q): %v", noise, err)
		}
	}
}

func TestLoadEnv_ProductionRejectsDevelopmentValues(t *testing.T) {
	cases := map[string]map[string]string{
		"public devnet RPC":    {"LAUNCH_RPC_PRIMARY_URL": "https://api.devnet.solana.com"},
		"public mainnet RPC":   {"LAUNCH_RPC_SECONDARY_URL": "https://api.mainnet-beta.solana.com"},
		"plain http RPC":       {"LAUNCH_RPC_PRIMARY_URL": "http://rpc.internal:8899"},
		"same RPC twice":       {"LAUNCH_RPC_SECONDARY_URL": "https://mainnet.helius-rpc.com/?api-key=x"},
		"socket in /tmp":       {"LAUNCH_SIGNER_SOCKET": "/tmp/launch-signer.sock"},
		"relative socket":      {"LAUNCH_SIGNER_SOCKET": "signer.sock"},
		"zero tx cap":          {"LAUNCH_SIGNER_MAX_TX_LAMPORTS": "0"},
		"non-numeric tx cap":   {"LAUNCH_SIGNER_MAX_TX_LAMPORTS": "0.1"},
		"invalid program id":   {"LAUNCH_SIGNER_PROGRAM_ALLOWLIST": "not-a-key"},
		"unimplemented signer": {"LAUNCH_SIGNER_BACKEND": "turnkey"},
		"unknown signer":       {"LAUNCH_SIGNER_BACKEND": "aws"},
		"unimplemented bus":    {"LAUNCH_COMMAND_BUS": "valkey"},
		"unknown environment":  {"ENVIRONMENT": "staging"},
	}
	for name, override := range cases {
		t.Run(name, func(t *testing.T) {
			m := validProdEnv()
			for k, v := range override {
				m[k] = v
			}
			if _, err := LoadEnv(envFrom(m)); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestIsValidPublicKey(t *testing.T) {
	for _, k := range DefaultProgramAllowlist {
		if !IsValidPublicKey(k) {
			t.Errorf("%s should be valid", k)
		}
	}
	for _, k := range []string{"", "0OIl", "abc", "TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DAA"} {
		if IsValidPublicKey(k) {
			t.Errorf("%q should be invalid", k)
		}
	}
}

func TestLoadEnvPhase1SourcesDefaultsAndValidation(t *testing.T) {
	base := map[string]string{"LAUNCH_DATABASE_URL": "postgres://u:p@127.0.0.1:5432/db"}
	get := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

	env, err := LoadEnv(get(base))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if env.SourcePrimaryWSURL == "" || env.SourceSecondaryURL == "" || env.QuoteAPIURL == "" {
		t.Fatalf("development defaults missing: %+v", env)
	}
	if env.RugCheckURL != "" {
		t.Fatalf("rugcheck must be disabled by default, got %q", env.RugCheckURL)
	}

	bad := map[string]string{"LAUNCH_DATABASE_URL": base["LAUNCH_DATABASE_URL"], "LAUNCH_QUOTE_API_URL": "ftp://x"}
	if _, err := LoadEnv(get(bad)); err == nil {
		t.Fatal("expected unsupported scheme to be rejected")
	}
}
