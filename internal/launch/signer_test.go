package launch

import (
	"encoding/json"
	"errors"
	"testing"
)

func testPolicy() SignerPolicy {
	return NewSignerPolicy(Env{SignerMaxTxLamports: 1_000, SignerProgramAllowlist: DefaultProgramAllowlist})
}

func okRequest() SignRequest {
	return SignRequest{
		Network:       "solana",
		Tx:            []byte{1, 2, 3},
		ProgramIDs:    []string{DefaultProgramAllowlist[0], DefaultProgramAllowlist[5]},
		NativeOutflow: 1_000,
		Reason:        "buy opportunity 42",
	}
}

func TestSignerPolicy_AllowsCompliantRequest(t *testing.T) {
	if err := testPolicy().Check(okRequest()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSignerPolicy_Rejects(t *testing.T) {
	cases := map[string]func(*SignRequest){
		"outflow over cap": func(r *SignRequest) { r.NativeOutflow = 1_001 },
		"program not allowlisted": func(r *SignRequest) {
			r.ProgramIDs = append(r.ProgramIDs, "Vote111111111111111111111111111111111111111")
		},
		"no programs declared": func(r *SignRequest) { r.ProgramIDs = nil },
		"empty transaction":    func(r *SignRequest) { r.Tx = nil },
		"missing reason":       func(r *SignRequest) { r.Reason = "" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			req := okRequest()
			mutate(&req)
			if err := testPolicy().Check(req); !errors.Is(err, ErrPolicyViolation) {
				t.Fatalf("expected ErrPolicyViolation, got %v", err)
			}
		})
	}
}

func TestSignerPolicy_UnconfiguredFailsClosed(t *testing.T) {
	for name, p := range map[string]SignerPolicy{
		"zero value":      {},
		"no allowlist":    NewSignerPolicy(Env{SignerMaxTxLamports: 1_000}),
		"no max tx limit": NewSignerPolicy(Env{SignerProgramAllowlist: DefaultProgramAllowlist}),
	} {
		t.Run(name, func(t *testing.T) {
			if err := p.Check(okRequest()); !errors.Is(err, ErrPolicyViolation) {
				t.Fatalf("expected ErrPolicyViolation, got %v", err)
			}
		})
	}
}

func TestCommandValidate(t *testing.T) {
	ok := Command{Kind: CommandKillSwitch, Actor: "zascontab", Reason: "manual stop"}
	if err := ok.Validate(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	bad := map[string]Command{
		"unknown kind": {Kind: "launch_nukes", Actor: "a", Reason: "r"},
		"no actor":     {Kind: CommandKillSwitch, Reason: "r"},
		"no reason":    {Kind: CommandKillSwitch, Actor: "a"},
		"invalid json": {Kind: CommandApprove, Actor: "a", Reason: "r", Payload: json.RawMessage(`{`)},
	}
	for name, c := range bad {
		if err := c.Validate(); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}
