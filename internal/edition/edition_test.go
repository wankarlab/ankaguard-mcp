package edition

import "testing"

// TestBuildMatchesTags checks Build/IsProduct/IsPro are consistent with each
// other; the tag-specific expectations live in the per-tag test files.
func TestBuildConsistent(t *testing.T) {
	switch Build() {
	case BuildStandard:
		if IsProduct() || IsPro() {
			t.Fatal("standard build must not report product or pro")
		}
	case BuildProduct:
		if !IsProduct() || IsPro() {
			t.Fatal("product build must report product and not pro")
		}
	case BuildProductPro:
		if !IsProduct() || !IsPro() {
			t.Fatal("product-pro build must report product and pro")
		}
	default:
		t.Fatalf("unknown build %q", Build())
	}
}
