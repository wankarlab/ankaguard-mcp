package prompts

import (
	"context"
	"fmt"
	"time"
)

// Service provides prompt generation capabilities
type Service interface {
	// GeneratePrompt creates a prompt for the given scenario
	GeneratePrompt(ctx context.Context, scenario string, data *MarketData, context string) (*Prompt, error)

	// ExecuteWorkflow runs a multi-step analysis workflow
	ExecuteWorkflow(ctx context.Context, workflowName string, initialData *MarketData) (*WorkflowResult, error)

	// GetTemplate retrieves a template by name
	GetTemplate(name string) (*Template, error)

	// ListTemplates returns all available templates
	ListTemplates() []*Template
}

// PromptEngineService implements the Service interface
type PromptEngineService struct {
	templateManager *TemplateManager
	dataInjector    *MarketDataInjector
	workflowOrch    *WorkflowOrchestrator
	promptBuilder   *PromptBuilder
}

// NewPromptEngineService creates a new prompt engine service
func NewPromptEngineService(config *Config) *PromptEngineService {
	tm := NewTemplateManager(config.TemplatesDir)
	return &PromptEngineService{
		templateManager: tm,
		dataInjector:    NewMarketDataInjector(30 * time.Second), // 30s cache TTL
		workflowOrch:    NewWorkflowOrchestrator(config.WorkflowsDir),
		promptBuilder:   NewPromptBuilder(tm),
	}
}

// GeneratePrompt creates a prompt for the given scenario
func (p *PromptEngineService) GeneratePrompt(
	ctx context.Context,
	scenario string,
	data *MarketData,
	context string,
) (*Prompt, error) {
	// Get template
	template, err := p.templateManager.GetTemplate(scenario)
	if err != nil {
		return nil, fmt.Errorf("failed to get template: %w", err)
	}

	// Inject market data
	variables, err := p.dataInjector.InjectData(data)
	if err != nil {
		return nil, fmt.Errorf("failed to inject market data: %w", err)
	}

	// Convert variables to string map for injection
	stringVars := make(map[string]string)
	for k, v := range variables {
		stringVars[k] = fmt.Sprintf("%v", v)
	}

	// Inject variables into template
	promptText := p.templateManager.InjectVariables(template, stringVars)

	return &Prompt{
		Template:    scenario,
		Content:     promptText,
		Variables:   variables,
		Context:     context,
		GeneratedAt: time.Now(),
	}, nil
}

// ExecuteWorkflow runs a multi-step analysis workflow
func (p *PromptEngineService) ExecuteWorkflow(
	ctx context.Context,
	workflowName string,
	initialData *MarketData,
) (*WorkflowResult, error) {
	return p.workflowOrch.Execute(ctx, workflowName, initialData)
}

// GetTemplate retrieves a template by name
func (p *PromptEngineService) GetTemplate(name string) (*Template, error) {
	templateStr, err := p.templateManager.GetTemplate(name)
	if err != nil {
		return nil, err
	}
	return &Template{
		Name:         name,
		TemplateText: templateStr,
		Variables:    []string{},
	}, nil
}

// ListTemplates returns all available templates
func (p *PromptEngineService) ListTemplates() []*Template {
	// TemplateManager doesn't have ListTemplates, return empty for now
	return []*Template{}
}

// Initialize loads all templates and workflows
func (p *PromptEngineService) Initialize(ctx context.Context) error {
	// Load templates
	if err := p.templateManager.Load(); err != nil {
		return fmt.Errorf("failed to load templates: %w", err)
	}

	// Load workflows
	if err := p.workflowOrch.LoadWorkflows(); err != nil {
		return fmt.Errorf("failed to load workflows: %w", err)
	}

	return nil
}

// ReloadTemplates reloads all templates from disk
func (p *PromptEngineService) ReloadTemplates(ctx context.Context) error {
	if err := p.templateManager.Load(); err != nil {
		return fmt.Errorf("failed to reload templates: %w", err)
	}

	if err := p.workflowOrch.Reload(); err != nil {
		return fmt.Errorf("failed to reload workflows: %w", err)
	}

	return nil
}
