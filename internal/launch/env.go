// Package launch holds the AnkaLaunch (on-chain new-token bot) foundations:
// startup environment validation, the Venue/Signer/CommandBus contracts and
// the Postgres command bus. See docs/bot/tech-stack.md.
package launch

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// Environment selects how strictly the startup configuration is validated.
type Environment string

const (
	EnvDevelopment Environment = "development"
	EnvProduction  Environment = "production"
)

// Signer backends (docs/bot/tech-stack.md §7 decision 2). Only "local" is
// implemented in phases 0-2; the rest are reserved behind the Signer interface.
const (
	SignerBackendLocal   = "local"
	SignerBackendTurnkey = "turnkey"
	SignerBackendKMS     = "kms"
	SignerBackendHSM     = "hsm"
)

// Command bus transports (decision 3).
const (
	CommandBusPostgres = "postgres"
	CommandBusValkey   = "valkey"
)

// Development defaults. None of them is accepted in production.
const (
	devSourcePrimaryWSURL = "wss://pumpportal.fun/api/data"
	devSourceSecondaryURL = "https://api.dexscreener.com"
	devQuoteAPIURL        = "https://lite-api.jup.ag/swap/v1"

	devRPCPrimaryURL       = "https://api.devnet.solana.com"
	devSignerSocket        = "/tmp/launch-signer.sock"
	devSignerMaxTxLamports = 50_000_000 // 0.05 SOL
)

// publicRPCHosts are free, rate-limited endpoints without Geyser; they are fine
// for development but never for production (docs/bot/tech-stack.md §2).
var publicRPCHosts = []string{
	"api.devnet.solana.com",
	"api.testnet.solana.com",
	"api.mainnet-beta.solana.com",
}

// DefaultProgramAllowlist is the development allowlist: System, SPL Token,
// Token-2022, Associated Token Account, Compute Budget and Jupiter v6.
var DefaultProgramAllowlist = []string{
	"11111111111111111111111111111111",
	"TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA",
	"TokenzQdBNbLqP5VEhdkAS6EPFLC1PHnBqCXEpPxuEb",
	"ATokenGPvbdGVxr1b2hvZbsiqW5xWH25efTNsLJA8knL",
	"ComputeBudget111111111111111111111111111111",
	"JUP6LkbZbjS1jKKwapdHNy74zcZ3tLUZoi5QNyVTaV4",
}

// Env is the infrastructure/secret configuration of the launch processes.
// Tunable limits live in the ConfigStore (launch.* parameters), not here.
type Env struct {
	Environment            Environment
	DatabaseURL            string
	RPCPrimaryURL          string
	RPCSecondaryURL        string
	SignerBackend          string
	SignerSocket           string
	SignerMaxTxLamports    uint64
	SignerProgramAllowlist []string
	CommandBus             string
	TelegramToken          string

	// Phase 1 (SHADOW) market-data sources. All free tiers; see
	// openspec/changes/ankalaunch-phase1-shadow/design.md D1.
	SourcePrimaryWSURL string // pump.fun event stream (PumpPortal websocket)
	SourceSecondaryURL string // polling source for new Solana pools (Dexscreener API)
	QuoteAPIURL        string // executable quotes, no swaps (Jupiter quote API)
	RugCheckURL        string // optional extra risk source; empty = disabled
}

// LoadEnv reads and validates the launch environment. In production every
// endpoint and secret is mandatory and development defaults are rejected; in
// development each missing value falls back to a safe default. All problems
// are reported together so a failed boot lists everything to fix at once.
func LoadEnv(getenv func(string) string) (Env, error) {
	var errs []error
	get := func(k string) string { return strings.TrimSpace(getenv(k)) }

	env := Env{Environment: Environment(strings.ToLower(get("ENVIRONMENT")))}
	switch env.Environment {
	case "":
		env.Environment = EnvDevelopment
	case EnvDevelopment, EnvProduction:
	default:
		return Env{}, fmt.Errorf("ENVIRONMENT=%q: must be development or production", env.Environment)
	}
	prod := env.Environment == EnvProduction

	required := func(key, devDefault string) string {
		v := get(key)
		if v == "" {
			if prod {
				// Report it as missing only; never validate a dev default in production.
				errs = append(errs, fmt.Errorf("%s is required in production", key))
				return ""
			}
			return devDefault
		}
		return v
	}

	env.DatabaseURL = required("LAUNCH_DATABASE_URL", "")
	if env.DatabaseURL == "" && !prod {
		errs = append(errs, errors.New("LAUNCH_DATABASE_URL is required (Postgres DSN for the launch schema)"))
	}

	env.RPCPrimaryURL = required("LAUNCH_RPC_PRIMARY_URL", devRPCPrimaryURL)
	env.RPCSecondaryURL = required("LAUNCH_RPC_SECONDARY_URL", "")
	for key, u := range map[string]string{"LAUNCH_RPC_PRIMARY_URL": env.RPCPrimaryURL, "LAUNCH_RPC_SECONDARY_URL": env.RPCSecondaryURL} {
		if u == "" {
			continue
		}
		if err := validateRPCURL(u, prod); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", key, err))
		}
	}
	if prod && env.RPCPrimaryURL != "" && env.RPCPrimaryURL == env.RPCSecondaryURL {
		errs = append(errs, errors.New("LAUNCH_RPC_SECONDARY_URL must differ from the primary (failover needs two providers)"))
	}

	env.SignerBackend = strings.ToLower(required("LAUNCH_SIGNER_BACKEND", SignerBackendLocal))
	switch env.SignerBackend {
	case "": // missing in production, already reported
	case SignerBackendLocal:
	case SignerBackendTurnkey, SignerBackendKMS, SignerBackendHSM:
		errs = append(errs, fmt.Errorf("LAUNCH_SIGNER_BACKEND=%s is not implemented yet (only local)", env.SignerBackend))
	default:
		errs = append(errs, fmt.Errorf("LAUNCH_SIGNER_BACKEND=%q: must be local, turnkey, kms or hsm", env.SignerBackend))
	}

	env.SignerSocket = required("LAUNCH_SIGNER_SOCKET", devSignerSocket)
	if env.SignerSocket != "" && !strings.HasPrefix(env.SignerSocket, "/") {
		errs = append(errs, fmt.Errorf("LAUNCH_SIGNER_SOCKET=%q must be an absolute Unix socket path", env.SignerSocket))
	}
	if prod && strings.HasPrefix(env.SignerSocket, "/tmp/") {
		errs = append(errs, errors.New("LAUNCH_SIGNER_SOCKET must not live in /tmp in production"))
	}

	maxTx := required("LAUNCH_SIGNER_MAX_TX_LAMPORTS", strconv.FormatUint(devSignerMaxTxLamports, 10))
	if maxTx != "" {
		n, err := strconv.ParseUint(maxTx, 10, 64)
		if err != nil || n == 0 {
			errs = append(errs, fmt.Errorf("LAUNCH_SIGNER_MAX_TX_LAMPORTS=%q must be a positive integer", maxTx))
		}
		env.SignerMaxTxLamports = n
	}

	allow := required("LAUNCH_SIGNER_PROGRAM_ALLOWLIST", strings.Join(DefaultProgramAllowlist, ","))
	for _, p := range strings.Split(allow, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if !IsValidPublicKey(p) {
			errs = append(errs, fmt.Errorf("LAUNCH_SIGNER_PROGRAM_ALLOWLIST: %q is not a valid Solana address", p))
			continue
		}
		env.SignerProgramAllowlist = append(env.SignerProgramAllowlist, p)
	}
	if allow != "" && len(env.SignerProgramAllowlist) == 0 {
		errs = append(errs, errors.New("LAUNCH_SIGNER_PROGRAM_ALLOWLIST is empty"))
	}

	env.CommandBus = strings.ToLower(get("LAUNCH_COMMAND_BUS"))
	switch env.CommandBus {
	case "":
		env.CommandBus = CommandBusPostgres
	case CommandBusPostgres:
	case CommandBusValkey:
		errs = append(errs, errors.New("LAUNCH_COMMAND_BUS=valkey is not implemented yet (use postgres)"))
	default:
		errs = append(errs, fmt.Errorf("LAUNCH_COMMAND_BUS=%q: must be postgres or valkey", env.CommandBus))
	}

	env.SourcePrimaryWSURL = required("LAUNCH_SOURCE_PRIMARY_WS_URL", devSourcePrimaryWSURL)
	env.SourceSecondaryURL = required("LAUNCH_SOURCE_SECONDARY_URL", devSourceSecondaryURL)
	env.QuoteAPIURL = required("LAUNCH_QUOTE_API_URL", devQuoteAPIURL)
	env.RugCheckURL = get("LAUNCH_RUGCHECK_URL")
	for key, raw := range map[string]string{
		"LAUNCH_SOURCE_PRIMARY_WS_URL": env.SourcePrimaryWSURL,
		"LAUNCH_SOURCE_SECONDARY_URL":  env.SourceSecondaryURL,
		"LAUNCH_QUOTE_API_URL":         env.QuoteAPIURL,
		"LAUNCH_RUGCHECK_URL":          env.RugCheckURL,
	} {
		if raw == "" {
			continue
		}
		if err := validateSourceURL(raw, prod); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", key, err))
		}
	}

	// Telegram is the mandatory second alert channel in production (prd.md §11 q5).
	env.TelegramToken = required("LAUNCH_TELEGRAM_TOKEN", "")

	if len(errs) > 0 {
		return Env{}, fmt.Errorf("invalid launch environment (%s): %w", env.Environment, errors.Join(errs...))
	}
	return env, nil
}

func validateRPCURL(raw string, prod bool) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return fmt.Errorf("%q is not a valid URL", raw)
	}
	switch u.Scheme {
	case "https", "wss":
	case "http", "ws":
		if prod {
			return fmt.Errorf("%q must use TLS (https/wss) in production", raw)
		}
	default:
		return fmt.Errorf("%q: unsupported scheme %q", raw, u.Scheme)
	}
	if prod {
		for _, h := range publicRPCHosts {
			if strings.EqualFold(u.Hostname(), h) {
				return fmt.Errorf("public endpoint %s is not allowed in production (no Geyser, rate-limited)", h)
			}
		}
	}
	return nil
}

// validateSourceURL checks a market-data endpoint: http(s)/ws(s) only, TLS
// mandatory in production.
func validateSourceURL(raw string, prod bool) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return fmt.Errorf("%q is not a valid URL", raw)
	}
	switch u.Scheme {
	case "https", "wss":
	case "http", "ws":
		if prod {
			return fmt.Errorf("%q must use TLS (https/wss) in production", raw)
		}
	default:
		return fmt.Errorf("%q: unsupported scheme %q", raw, u.Scheme)
	}
	return nil
}
