package prompts

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// WorkflowOrchestrator executes multi-step workflows
type WorkflowOrchestrator struct {
	workflowsDir string
	workflows    map[string]*Workflow
}

// NewWorkflowOrchestrator creates a new workflow orchestrator
func NewWorkflowOrchestrator(workflowsDir string) *WorkflowOrchestrator {
	return &WorkflowOrchestrator{
		workflowsDir: workflowsDir,
		workflows:    make(map[string]*Workflow),
	}
}

// LoadWorkflows loads all workflow definitions from disk
func (wo *WorkflowOrchestrator) LoadWorkflows() error {
	if _, err := os.Stat(wo.workflowsDir); os.IsNotExist(err) {
		return fmt.Errorf("workflows directory does not exist: %s", wo.workflowsDir)
	}

	return filepath.Walk(wo.workflowsDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.IsDir() || !strings.HasSuffix(path, ".yaml") {
			return nil
		}

		workflow, err := wo.loadWorkflowFile(path)
		if err != nil {
			return fmt.Errorf("failed to load workflow %s: %w", path, err)
		}

		wo.workflows[workflow.Name] = workflow
		return nil
	})
}

// loadWorkflowFile loads a single workflow definition
func (wo *WorkflowOrchestrator) loadWorkflowFile(path string) (*Workflow, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read workflow file: %w", err)
	}

	var workflowData struct {
		Name        string `yaml:"name"`
		Description string `yaml:"description"`
		MaxRetries  int    `yaml:"max_retries"`
		RetryDelay  string `yaml:"retry_delay"`
		Steps       []struct {
			ID             string   `yaml:"id"`
			PromptTemplate string   `yaml:"prompt_template"`
			Required       bool     `yaml:"required"`
			Condition      string   `yaml:"condition"`
			DependsOn      []string `yaml:"depends_on"`
			Timeout        string   `yaml:"timeout"`
		} `yaml:"steps"`
	}

	if err := yaml.Unmarshal(data, &workflowData); err != nil {
		return nil, fmt.Errorf("failed to parse workflow YAML: %w", err)
	}

	// Parse retry delay
	retryDelay := 1 * time.Second
	if workflowData.RetryDelay != "" {
		if d, err := time.ParseDuration(workflowData.RetryDelay); err == nil {
			retryDelay = d
		}
	}

	// Convert steps
	steps := make([]WorkflowStep, len(workflowData.Steps))
	for i, stepData := range workflowData.Steps {
		timeout := 30 * time.Second
		if stepData.Timeout != "" {
			if t, err := time.ParseDuration(stepData.Timeout); err == nil {
				timeout = t
			}
		}

		steps[i] = WorkflowStep{
			ID:             stepData.ID,
			PromptTemplate: stepData.PromptTemplate,
			Required:       stepData.Required,
			Condition:      stepData.Condition,
			DependsOn:      stepData.DependsOn,
			Timeout:        timeout,
		}
	}

	workflow := &Workflow{
		Name:        workflowData.Name,
		Description: workflowData.Description,
		Steps:       steps,
		MaxRetries:  workflowData.MaxRetries,
		RetryDelay:  retryDelay,
	}

	if workflow.MaxRetries == 0 {
		workflow.MaxRetries = 3
	}

	return workflow, nil
}

// Execute runs a workflow
func (wo *WorkflowOrchestrator) Execute(
	ctx context.Context,
	workflowName string,
	initialData *MarketData,
) (*WorkflowResult, error) {
	workflow, exists := wo.workflows[workflowName]
	if !exists {
		return nil, fmt.Errorf("workflow not found: %s", workflowName)
	}

	result := &WorkflowResult{
		WorkflowName: workflowName,
		Steps:        make([]StepResult, 0, len(workflow.Steps)),
		Success:      true,
		StartedAt:    time.Now(),
	}

	// Store step results for dependency resolution
	stepResults := make(map[string]interface{})

	for _, step := range workflow.Steps {
		// Check if step should be executed based on dependencies
		if !wo.shouldExecuteStep(step, stepResults) {
			continue
		}

		// Check condition if specified
		if step.Condition != "" && !wo.evaluateCondition(step.Condition, stepResults) {
			continue
		}

		// Execute step with retries
		stepResult := wo.executeStepWithRetry(ctx, step, workflow.MaxRetries, workflow.RetryDelay)
		result.Steps = append(result.Steps, stepResult)

		if !stepResult.Success {
			if step.Required {
				result.Success = false
				result.Error = fmt.Errorf("required step %s failed: %w", step.ID, stepResult.Error)
				break
			}
		} else {
			stepResults[step.ID] = stepResult.Response
		}
	}

	result.CompletedAt = time.Now()
	return result, result.Error
}

// shouldExecuteStep checks if a step's dependencies are satisfied
func (wo *WorkflowOrchestrator) shouldExecuteStep(step WorkflowStep, results map[string]interface{}) bool {
	for _, depID := range step.DependsOn {
		if _, exists := results[depID]; !exists {
			return false
		}
	}
	return true
}

// evaluateCondition evaluates a simple condition
func (wo *WorkflowOrchestrator) evaluateCondition(condition string, results map[string]interface{}) bool {
	// Simple condition evaluation (can be extended)
	// Format: "step_id.field == value" or "step_id exists"
	if strings.Contains(condition, "exists") {
		stepID := strings.TrimSpace(strings.Replace(condition, "exists", "", 1))
		_, exists := results[stepID]
		return exists
	}

	// Default to true for now
	return true
}

// executeStepWithRetry executes a step with retry logic
func (wo *WorkflowOrchestrator) executeStepWithRetry(
	ctx context.Context,
	step WorkflowStep,
	maxRetries int,
	retryDelay time.Duration,
) StepResult {
	startTime := time.Now()
	var lastErr error

	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return StepResult{
					StepID:   step.ID,
					Success:  false,
					Error:    ctx.Err(),
					Duration: time.Since(startTime),
				}
			case <-time.After(retryDelay):
			}
		}

		// Create step context with timeout
		stepCtx, cancel := context.WithTimeout(ctx, step.Timeout)
		response, err := wo.executeStep(stepCtx, step)
		cancel()

		if err == nil {
			return StepResult{
				StepID:   step.ID,
				Success:  true,
				Response: response,
				Duration: time.Since(startTime),
			}
		}

		lastErr = err
	}

	return StepResult{
		StepID:   step.ID,
		Success:  false,
		Error:    fmt.Errorf("step failed after %d retries: %w", maxRetries, lastErr),
		Duration: time.Since(startTime),
	}
}

// executeStep executes a single workflow step
func (wo *WorkflowOrchestrator) executeStep(ctx context.Context, step WorkflowStep) (interface{}, error) {
	// This is a placeholder - actual execution would call the prompt engine
	// and AI service to generate and execute the prompt
	// For now, return a mock response
	return map[string]interface{}{
		"step_id":   step.ID,
		"template":  step.PromptTemplate,
		"completed": true,
	}, nil
}

// GetWorkflow retrieves a workflow by name
func (wo *WorkflowOrchestrator) GetWorkflow(name string) (*Workflow, error) {
	workflow, exists := wo.workflows[name]
	if !exists {
		return nil, fmt.Errorf("workflow not found: %s", name)
	}
	return workflow, nil
}

// ListWorkflows returns all available workflows
func (wo *WorkflowOrchestrator) ListWorkflows() []*Workflow {
	workflows := make([]*Workflow, 0, len(wo.workflows))
	for _, wf := range wo.workflows {
		workflows = append(workflows, wf)
	}
	return workflows
}

// Reload reloads all workflows from disk
func (wo *WorkflowOrchestrator) Reload() error {
	wo.workflows = make(map[string]*Workflow)
	return wo.LoadWorkflows()
}
