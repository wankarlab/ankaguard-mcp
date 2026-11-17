package main

import (
	"fmt"
	"os"

	"github.com/rantipay/trading-mcp/internal/edition"
	buildversion "github.com/rantipay/trading-mcp/internal/version"
)

// productProfile is the single decision block of the boot wiring (task 8.1,
// design D13). Every service that the product build leaves out is switched
// here and nowhere else; main.go only reads these flags.
//
// The standard build (no build tag) keeps every flag true, so its behaviour is
// exactly the one of the deployment that runs the real-money bot.
type productProfile struct {
	// Product reports whether the binary was compiled with the product tag.
	Product bool

	// RunAIBot gates the autonomous AI trading bot: auto-start, its helper
	// loops and its REST handler.
	RunAIBot bool
	// RunScalping gates the scalping engine (auto-start, order flow feeder),
	// its MCP tools and its dashboard routes.
	RunScalping bool
	// RunMasterAgent gates the master agent, the multi-pair orchestrator and
	// the /api/v1/master and /api/v1/bots routes.
	RunMasterAgent bool
	// RunXSEC gates the XSEC live wiring (status mirror, live book ownership
	// registry) and the XSEC REST surface.
	RunXSEC bool
	// RunAnkaLaunch gates the AnkaLaunch MCP tools and console routes.
	RunAnkaLaunch bool
	// RunTournament gates the tournament routes.
	RunTournament bool
	// RunRantiPayServices gates the integrations with RantiPay microservices
	// (push notifications, auth proxy-only features) that the product does not
	// ship.
	RunRantiPayServices bool

	// RequireGCTEnv makes missing GCT_* variables fatal. The product starts
	// without GoCryptoTrader and reports it instead.
	RequireGCTEnv bool
	// GCTConnectAttempts is the number of initial gRPC connection attempts.
	GCTConnectAttempts int
}

// newProductProfile computes the profile for the compiled edition.
func newProductProfile(isProduct bool) productProfile {
	if !isProduct {
		return productProfile{
			RunAIBot: true, RunScalping: true, RunMasterAgent: true, RunXSEC: true,
			RunAnkaLaunch: true, RunTournament: true, RunRantiPayServices: true,
			RequireGCTEnv: true, GCTConnectAttempts: 5,
		}
	}
	return productProfile{Product: true, GCTConnectAttempts: 1}
}

// companionServices reports whether any RantiPay-side surface is served; the
// REST router omits them all together when this is false.
func (p productProfile) companionServices() bool {
	return p.RunXSEC || p.RunAnkaLaunch || p.RunTournament || p.RunRantiPayServices
}

// versionLine is the text printed by -version.
func versionLine() string {
	commit := buildversion.GitCommit
	if commit == "" || commit == "unknown" {
		commit = vcsRevision()
	}
	return fmt.Sprintf("trading-mcp-server version=%s commit=%s edition=%s", buildversion.Version, commit, edition.Build())
}

// printVersion writes versionLine to stdout.
func printVersion() { fmt.Fprintln(os.Stdout, versionLine()) }
