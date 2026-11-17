//go:build release

package license

import "testing"

// TestReleaseKeyIsNotPlaceholder is the publication check (task 7.11). Run it
// with `go test -tags release -run TestReleaseKeyIsNotPlaceholder ./internal/license`;
// it fails while pubkey.ed25519 still holds the placeholder key.
func TestReleaseKeyIsNotPlaceholder(t *testing.T) {
	if IsPlaceholderKey(EmbeddedPublicKey()) {
		t.Fatal("internal/license/pubkey.ed25519 contains the placeholder public key: " +
			"replace it with the real signing public key before publishing a release")
	}
}
