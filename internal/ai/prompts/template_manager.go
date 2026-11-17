package prompts

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// TemplateManager manages prompt templates for different trading styles
type TemplateManager struct {
	templatesDir string
	templates    map[string]string
}

// NewTemplateManager creates a new template manager
func NewTemplateManager(templatesDir string) *TemplateManager {
	return &TemplateManager{
		templatesDir: templatesDir,
		templates:    make(map[string]string),
	}
}

// Load loads all prompt templates from the directory
func (tm *TemplateManager) Load() error {
	// Check if directory exists
	if _, err := os.Stat(tm.templatesDir); os.IsNotExist(err) {
		return fmt.Errorf("templates directory does not exist: %s", tm.templatesDir)
	}

	// Load all .txt files
	files, err := filepath.Glob(filepath.Join(tm.templatesDir, "*.txt"))
	if err != nil {
		return fmt.Errorf("failed to list template files: %w", err)
	}

	for _, file := range files {
		content, err := os.ReadFile(file)
		if err != nil {
			return fmt.Errorf("failed to read template %s: %w", file, err)
		}

		// Use filename without extension as key
		name := strings.TrimSuffix(filepath.Base(file), ".txt")
		tm.templates[name] = string(content)
	}

	return nil
}

// GetTemplate returns a template by name
func (tm *TemplateManager) GetTemplate(name string) (string, error) {
	template, ok := tm.templates[name]
	if !ok {
		return "", fmt.Errorf("template not found: %s", name)
	}
	return template, nil
}

// GetTemplateForStyle returns the appropriate template for a trading style
func (tm *TemplateManager) GetTemplateForStyle(style string) (string, error) {
	var templateName string

	switch strings.ToLower(style) {
	case "scalping":
		templateName = "scalping-analysis"
	case "day_trading", "daytrading":
		templateName = "day-trading-analysis"
	case "swing_trading", "swingtrading":
		templateName = "swing-trading-analysis"
	default:
		return "", fmt.Errorf("unknown trading style: %s", style)
	}

	return tm.GetTemplate(templateName)
}

// InjectVariables replaces template variables with actual values
func (tm *TemplateManager) InjectVariables(template string, variables map[string]string) string {
	result := template

	for key, value := range variables {
		placeholder := fmt.Sprintf("{{%s}}", key)
		result = strings.ReplaceAll(result, placeholder, value)
	}

	return result
}

// Count returns the number of loaded templates
func (tm *TemplateManager) Count() int {
	return len(tm.templates)
}
