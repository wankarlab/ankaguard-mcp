package main

import (
	"strings"
	"testing"
)

// A2: the client-level live order gate of the product edition.
func TestLiveOrderGate(t *testing.T) {
	yes := func() bool { return true }
	no := func() bool { return false }
	cases := []struct {
		name       string
		pro        bool
		lic, live  func() bool
		wantPrefix string
	}{
		{"community", false, yes, yes, liveGateLicenseRequired},
		{"pro without license", true, no, yes, liveGateLicenseRequired},
		{"pro, nil license", true, nil, yes, liveGateLicenseRequired},
		{"pro, live disabled", true, yes, no, liveGateLiveDisabled},
		{"pro, licensed, live enabled", true, yes, yes, ""},
	}
	for _, c := range cases {
		err := liveOrderGate(c.pro, c.lic, c.live)()
		if c.wantPrefix == "" {
			if err != nil {
				t.Errorf("%s: %v", c.name, err)
			}
			continue
		}
		if err == nil || !strings.HasPrefix(err.Error(), c.wantPrefix) {
			t.Errorf("%s: got %v, want %s", c.name, err, c.wantPrefix)
		}
	}
}
