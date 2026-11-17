package knowledge

import (
	"context"
	"fmt"
	"sync"
)

// Service provides access to the trading knowledge base
type Service interface {
	// Initialize loads all knowledge base files
	Initialize(ctx context.Context) error

	// GetContext returns relevant context for the given analysis type
	GetContext(ctx context.Context, req *ContextRequest) (*ContextResponse, error)

	// Reload refreshes the knowledge base from disk
	Reload(ctx context.Context) error

	// GetStatus returns the current status of the knowledge base
	GetStatus() *Status
}

// Status represents the current state of the knowledge base
type Status struct {
	Initialized   bool
	SteeringFiles int
	TrainingDocs  int
	TotalSections int
	LastReload    string
	CacheHitRate  float64
}

// KnowledgeBaseService implements the Service interface
type KnowledgeBaseService struct {
	steeringManager *SteeringFileManager
	docRepository   *TrainingDocRepository
	contextBuilder  *ContextBuilder
	cache           *KnowledgeCache

	mu          sync.RWMutex
	initialized bool
}

// NewKnowledgeBaseService creates a new knowledge base service
func NewKnowledgeBaseService(config *Config) *KnowledgeBaseService {
	return &KnowledgeBaseService{
		steeringManager: NewSteeringFileManager(config.SteeringDir),
		docRepository:   NewTrainingDocRepository(config.TrainingDocsDir),
		contextBuilder:  NewContextBuilder(),
		cache:           NewKnowledgeCache(config.CacheTTL, config.CacheEnabled),
	}
}

// Initialize loads all knowledge base files
func (k *KnowledgeBaseService) Initialize(ctx context.Context) error {
	k.mu.Lock()
	defer k.mu.Unlock()

	// Load steering files
	if err := k.steeringManager.Load(ctx); err != nil {
		return fmt.Errorf("failed to load steering files: %w", err)
	}

	// Load training documents
	if err := k.docRepository.Load(ctx); err != nil {
		return fmt.Errorf("failed to load training documents: %w", err)
	}

	// Initialize context builder with loaded data
	k.contextBuilder.Initialize(k.steeringManager, k.docRepository)

	k.initialized = true
	return nil
}

// GetContext returns relevant context for analysis
func (k *KnowledgeBaseService) GetContext(ctx context.Context, req *ContextRequest) (*ContextResponse, error) {
	k.mu.RLock()
	defer k.mu.RUnlock()

	if !k.initialized {
		return nil, fmt.Errorf("knowledge base not initialized")
	}

	// Check cache first
	if cached := k.cache.Get(req); cached != nil {
		return cached, nil
	}

	// Build context
	response, err := k.contextBuilder.BuildContext(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to build context: %w", err)
	}

	// Cache the result
	k.cache.Set(req, response)

	return response, nil
}

// Reload refreshes knowledge base from disk
func (k *KnowledgeBaseService) Reload(ctx context.Context) error {
	return k.Initialize(ctx)
}

// GetStatus returns current knowledge base status
func (k *KnowledgeBaseService) GetStatus() *Status {
	k.mu.RLock()
	defer k.mu.RUnlock()

	return &Status{
		Initialized:   k.initialized,
		SteeringFiles: k.steeringManager.Count(),
		TrainingDocs:  k.docRepository.Count(),
		TotalSections: k.docRepository.SectionCount(),
		CacheHitRate:  k.cache.HitRate(),
	}
}
