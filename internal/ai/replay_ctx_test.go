package ai

import (
	"context"
	"testing"
)

func TestReplayContextMarker(t *testing.T) {
	if IsReplay(context.Background()) {
		t.Fatal("plain context must not be a replay")
	}
	if !IsReplay(WithReplay(context.Background())) {
		t.Fatal("WithReplay context must be a replay")
	}
}
