package edition

import "testing"

// wantBuild is set by the tag-specific files below.
func TestBuildNameForTags(t *testing.T) {
	if Build() != wantBuild {
		t.Fatalf("Build() = %q, want %q", Build(), wantBuild)
	}
	if IsProduct() != (wantBuild != "standard") {
		t.Fatalf("IsProduct() wrong for %q", wantBuild)
	}
	if IsPro() != (wantBuild == "product-pro") {
		t.Fatalf("IsPro() wrong for %q", wantBuild)
	}
}
