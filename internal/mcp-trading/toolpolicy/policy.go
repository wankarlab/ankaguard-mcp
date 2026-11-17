// Package toolpolicy is the single source of truth for what each MCP tool is
// (its risk class) and what each token profile may do with it.
//
// It is intentionally dependency-free (standard library only) so the MCP
// server, the global kill switch and the catalog generators can all import it
// without cycles. Nothing here talks to an exchange.
//
// Design decisions (openspec change guarded-mcp-product, D1/D2):
//   - Every registered tool MUST have exactly one class. A tool with no class
//     cannot be listed or executed, with any profile (fail closed).
//   - A class can depend on the call arguments (see argClassifiers): a futures
//     order marked reduce-only or close is trade_reduce, anything else is
//     trade_increase.
//   - When in doubt, a tool is classified as control (the most restricted class
//     that still lets a live token use it).
package toolpolicy

// Class is the risk class of a tool call.
type Class string

const (
	// ClassRead is a query or a calculation without side effects.
	ClassRead Class = "read"
	// ClassTradeIncrease opens or enlarges exposure, or starts a trading loop.
	ClassTradeIncrease Class = "trade_increase"
	// ClassTradeReduce cancels, closes or reduces exposure.
	ClassTradeReduce Class = "trade_reduce"
	// ClassControl changes configuration, leverage, margin mode, subscriptions
	// or sends notifications, or lifts a safety stop.
	ClassControl Class = "control"
	// ClassSafety activates an emergency stop.
	ClassSafety Class = "safety"
)

// Classes lists every valid class in a stable order.
var Classes = []Class{ClassRead, ClassTradeIncrease, ClassTradeReduce, ClassControl, ClassSafety}

// Profile is the permission profile carried by a token (claim mcp_profile).
type Profile string

const (
	// ProfileRead can only call read tools.
	ProfileRead Profile = "read"
	// ProfilePaper can trade, but only against the simulated broker.
	ProfilePaper Profile = "paper"
	// ProfileLive can trade against the real exchange and change configuration.
	ProfileLive Profile = "live"
)

// OperatorLevel is the minimum user level that is treated as live when the
// token declares no explicit profile.
const OperatorLevel = 4

// ProfileFromClaim resolves the effective profile of a token.
//
//   - Empty claim: live when level >= OperatorLevel, read otherwise.
//   - Known value (read, paper, live): that profile.
//   - Any other value: read.
func ProfileFromClaim(claim string, level int) Profile {
	switch Profile(claim) {
	case ProfileRead, ProfilePaper, ProfileLive:
		return Profile(claim)
	case "":
		if level >= OperatorLevel {
			return ProfileLive
		}
		return ProfileRead
	default:
		return ProfileRead
	}
}

// Outcome is the result of checking a profile against a class.
type Outcome int

const (
	// Deny rejects the call.
	Deny Outcome = iota
	// AllowReal lets the call run against the real exchange / real state.
	AllowReal
	// AllowPaper routes the call to the paper broker; it must never reach the
	// exchange.
	AllowPaper
)

// Decide applies the permission matrix:
//
//	class            read   paper         live
//	read             allow  allow         allow
//	trade_increase   deny   paper broker  real exchange
//	trade_reduce     deny   paper broker  real exchange
//	control          deny   deny          allow
//	safety           deny   allow         allow
//
// An unknown class or profile is always denied.
func Decide(p Profile, c Class) Outcome {
	switch c {
	case ClassRead:
		switch p {
		case ProfileRead, ProfilePaper, ProfileLive:
			return AllowReal
		}
	case ClassTradeIncrease, ClassTradeReduce:
		switch p {
		case ProfilePaper:
			return AllowPaper
		case ProfileLive:
			return AllowReal
		}
	case ClassControl:
		if p == ProfileLive {
			return AllowReal
		}
	case ClassSafety:
		if p == ProfilePaper || p == ProfileLive {
			return AllowReal
		}
	}
	return Deny
}

// Permits reports whether the profile may execute (or list) the class.
func Permits(p Profile, c Class) bool { return Decide(p, c) != Deny }
