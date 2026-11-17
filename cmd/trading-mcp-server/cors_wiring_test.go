package main

import (
	"strings"
	"testing"
)

// B3: the product only allows the local console origins.
func TestCORSOriginsFor(t *testing.T) {
	for _, o := range corsOriginsFor(true, "") {
		if !strings.HasPrefix(o, "http://localhost:") && !strings.HasPrefix(o, "http://127.0.0.1:") {
			t.Errorf("product CORS origin %q is not local", o)
		}
	}
	found := false
	for _, o := range corsOriginsFor(false, "http://evil.example") {
		found = found || o == "http://localhost:3002"
	}
	if !found {
		t.Error("standard build lost its local console origin")
	}
}

// The product accepts the console origins of CONSOLE_ORIGINS (custom
// CONSOLE_PORT) and ignores wildcards and malformed entries.
func TestCORSOriginsForExtra(t *testing.T) {
	got := corsOriginsFor(true, " http://127.0.0.1:18602/ ,*,ftp://x,,http://localhost:3002")
	want := []string{"http://localhost:3002", "http://127.0.0.1:3002", "http://127.0.0.1:18602"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("got %v, want %v", got, want)
	}
	for _, o := range corsOriginsFor(false, "http://evil.example") {
		if o == "http://evil.example" {
			t.Error("standard build must ignore CONSOLE_ORIGINS")
		}
	}
}
