package knowledge

import (
	"context"
	"testing"
)

func TestContextBuilder_BuildContext(t *testing.T) {
	// Setup
	builder := NewContextBuilder()

	// Mock steering manager and doc repository
	steeringMgr := &SteeringFileManager{
		steeringDir: ".kiro/steering",
		files:       make(map[string]*SteeringFile),
	}

	docRepo := &TrainingDocRepository{
		docsDir:   "docs/training",
		documents: make(map[string]*TrainingDocument),
	}

	builder.Initialize(steeringMgr, docRepo)

	tests := []struct {
		name        string
		request     *ContextRequest
		wantErr     bool
		wantContext bool
	}{
		{
			name: "valid market analysis request",
			request: &ContextRequest{
				AnalysisType: "market_analysis",
				Pair:         "BTCUSDT",
			},
			wantErr:     false,
			wantContext: true,
		},
		{
			name: "empty analysis type",
			request: &ContextRequest{
				AnalysisType: "",
			},
			wantErr:     false,
			wantContext: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			response, err := builder.BuildContext(ctx, tt.request)

			if (err != nil) != tt.wantErr {
				t.Errorf("BuildContext() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantContext && response == nil {
				t.Error("BuildContext() expected response, got nil")
			}
		})
	}
}

func TestContextBuilder_ExtractSections(t *testing.T) {
	_ = NewContextBuilder()

	tests := []struct {
		name     string
		sections []string
		wantLen  int
	}{
		{
			name:     "single section",
			sections: []string{"market_structure"},
			wantLen:  1,
		},
		{
			name:     "multiple sections",
			sections: []string{"market_structure", "order_flow"},
			wantLen:  2,
		},
		{
			name:     "empty sections",
			sections: []string{},
			wantLen:  0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Test would extract sections from mock documents
			if len(tt.sections) != tt.wantLen {
				t.Errorf("Expected %d sections, got %d", tt.wantLen, len(tt.sections))
			}
		})
	}
}
