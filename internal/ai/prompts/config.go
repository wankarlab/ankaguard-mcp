package prompts

// Config holds configuration for the prompt engine
type Config struct {
	// TemplatesDir is the directory containing prompt templates
	TemplatesDir string

	// WorkflowsDir is the directory containing workflow definitions
	WorkflowsDir string

	// MaxPromptLength is the maximum length of generated prompts
	MaxPromptLength int

	// VariableValidation enables strict variable validation
	VariableValidation bool
}

// DefaultConfig returns default configuration
func DefaultConfig() *Config {
	return &Config{
		TemplatesDir:       "config/prompts/templates",
		WorkflowsDir:       "config/prompts/workflows",
		MaxPromptLength:    8000,
		VariableValidation: true,
	}
}
