package knowledge

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"
)

// ContextBuilder builds context strings for AI analysis
type ContextBuilder struct {
	steeringManager *SteeringFileManager
	docRepository   *TrainingDocRepository
	cache           *ContextCache
}

// NewContextBuilder creates a new context builder
func NewContextBuilder() *ContextBuilder {
	return &ContextBuilder{
		cache: NewContextCache(5 * time.Minute), // Default 5 min TTL
	}
}

// Initialize initializes the context builder with managers
func (c *ContextBuilder) Initialize(
	steeringManager *SteeringFileManager,
	docRepository *TrainingDocRepository,
) {
	c.steeringManager = steeringManager
	c.docRepository = docRepository
}

// BuildContext builds context for the given request
func (c *ContextBuilder) BuildContext(
	ctx context.Context,
	req *ContextRequest,
) (*ContextResponse, error) {
	// Check cache first
	cacheKey := c.cache.GenerateKey(req)
	if cached, ok := c.cache.Get(cacheKey); ok {
		return cached, nil
	}

	var contextParts []string
	var sections []string
	var sources []string

	// 1. Add always-included steering files
	alwaysFiles := c.steeringManager.GetAlwaysIncluded()
	for _, file := range alwaysFiles {
		// Process file references
		content, fileSources := c.processFileReferences(file.Content)
		contextParts = append(contextParts, content)
		sources = append(sources, file.Path)
		sources = append(sources, fileSources...)
	}

	// 2. Add trading style-specific steering file if provided
	if req.TradingStyle != "" {
		styleFile := c.getStyleSteeringFile(req.TradingStyle)
		if styleFile != nil {
			content, fileSources := c.processFileReferences(styleFile.Content)
			contextParts = append(contextParts, content)
			sources = append(sources, styleFile.Path)
			sources = append(sources, fileSources...)
		}
	}

	// 3. Add relevant sections based on analysis type
	relevantSections := c.getRelevantSections(req.AnalysisType)
	for _, section := range relevantSections {
		contextParts = append(contextParts, fmt.Sprintf("## %s\n\n%s", section.Title, section.Content))
		sections = append(sections, section.ID)
		sources = append(sources, fmt.Sprintf("section:%s", section.ID))
	}

	// 4. Add pair-specific context if provided
	if req.Pair != "" {
		pairContext := c.buildPairContext(req.Pair)
		if pairContext != "" {
			contextParts = append(contextParts, pairContext)
		}
	}

	// 5. Add timeframe-specific context if provided
	if req.Timeframe != "" {
		timeframeContext := c.buildTimeframeContext(req.Timeframe)
		if timeframeContext != "" {
			contextParts = append(contextParts, timeframeContext)
		}
	}

	// Combine all parts
	finalContext := strings.Join(contextParts, "\n\n---\n\n")

	response := &ContextResponse{
		Context:     finalContext,
		Sections:    sections,
		Sources:     sources,
		GeneratedAt: time.Now(),
	}

	// Cache the response
	c.cache.Set(cacheKey, response)

	return response, nil
}

// processFileReferences processes file inclusion references in content
func (c *ContextBuilder) processFileReferences(content string) (string, []string) {
	sources := []string{}

	// Find all file references
	lines := strings.Split(content, "\n")
	var processedLines []string

	for _, line := range lines {
		// Check if line contains file reference
		if strings.Contains(line, "#[[file:") {
			// Extract file path
			start := strings.Index(line, "#[[file:")
			end := strings.Index(line[start:], "]]")
			if end != -1 {
				filePath := line[start+8 : start+end]

				// Try to load the referenced file
				fileContent, err := c.loadReferencedFile(filePath)
				if err == nil {
					// Add a summary or excerpt instead of full content
					excerpt := c.createExcerpt(fileContent, 500) // First 500 chars
					processedLines = append(processedLines, fmt.Sprintf("### Referenced: %s\n\n%s\n\n[Full document available]", filePath, excerpt))
					sources = append(sources, filePath)
				} else {
					// Keep original reference if file can't be loaded
					processedLines = append(processedLines, line)
				}
				continue
			}
		}
		processedLines = append(processedLines, line)
	}

	return strings.Join(processedLines, "\n"), sources
}

// loadReferencedFile loads a file referenced in steering files
func (c *ContextBuilder) loadReferencedFile(filePath string) (string, error) {
	// Try to read the file
	content, err := os.ReadFile(filePath)
	if err != nil {
		return "", fmt.Errorf("failed to read referenced file: %w", err)
	}

	return string(content), nil
}

// createExcerpt creates a short excerpt from content
func (c *ContextBuilder) createExcerpt(content string, maxLength int) string {
	if len(content) <= maxLength {
		return content
	}

	// Find a good breaking point (end of sentence or paragraph)
	excerpt := content[:maxLength]
	lastPeriod := strings.LastIndex(excerpt, ".")
	lastNewline := strings.LastIndex(excerpt, "\n\n")

	breakPoint := maxLength
	if lastNewline > maxLength/2 {
		breakPoint = lastNewline
	} else if lastPeriod > maxLength/2 {
		breakPoint = lastPeriod + 1
	}

	return content[:breakPoint] + "..."
}

// getRelevantSections returns sections relevant to the analysis type
func (c *ContextBuilder) getRelevantSections(analysisType AnalysisType) []*KnowledgeSection {
	var sections []*KnowledgeSection

	switch analysisType {
	case AnalysisTypeMarket:
		sections = append(sections, c.docRepository.GetSectionsByCategory("market_analysis")...)
		sections = append(sections, c.docRepository.GetSectionsByCategory("technical_indicators")...)

	case AnalysisTypeSetup:
		sections = append(sections, c.docRepository.GetSectionsByCategory("setup_identification")...)
		sections = append(sections, c.docRepository.GetSectionsByTag("confluence")...)

	case AnalysisTypeRisk:
		sections = append(sections, c.docRepository.GetSectionsByCategory("risk_management")...)

	case AnalysisTypeValidation:
		sections = append(sections, c.docRepository.GetSectionsByTag("validation")...)
		sections = append(sections, c.docRepository.GetSectionsByCategory("risk_management")...)

	case AnalysisTypePositionSizing:
		sections = append(sections, c.docRepository.GetSectionsByCategory("risk_management")...)
		sections = append(sections, c.docRepository.GetSectionsByTag("risk")...)

	case AnalysisTypeOrderFlow:
		sections = append(sections, c.docRepository.GetSectionsByCategory("order_flow")...)
		sections = append(sections, c.docRepository.GetSectionsByTag("tape")...)

	case AnalysisTypeDerivatives:
		sections = append(sections, c.docRepository.GetSectionsByCategory("derivatives")...)
		sections = append(sections, c.docRepository.GetSectionsByTag("funding")...)
		sections = append(sections, c.docRepository.GetSectionsByTag("liquidation")...)

	case AnalysisTypeOnChain:
		sections = append(sections, c.docRepository.GetSectionsByCategory("on_chain")...)

	default:
		// Return general sections
		sections = append(sections, c.docRepository.GetSectionsByCategory("general")...)
	}

	// Limit to top 5 most relevant sections to avoid context overflow
	if len(sections) > 5 {
		sections = sections[:5]
	}

	return sections
}

// buildPairContext builds context specific to a trading pair
func (c *ContextBuilder) buildPairContext(pair string) string {
	// Extract base asset (e.g., BTC from BTC/USDT)
	parts := strings.Split(pair, "/")
	if len(parts) == 0 {
		return ""
	}

	baseAsset := parts[0]

	context := fmt.Sprintf("## Trading Pair Context: %s\n\n", pair)

	// Add asset-specific considerations
	switch baseAsset {
	case "BTC":
		context += "Bitcoin is the market leader. Its movements often dictate overall market direction. "
		context += "High liquidity allows for larger positions. Watch for correlation with traditional markets.\n"

	case "ETH":
		context += "Ethereum is the second-largest crypto. Often follows BTC but can have independent moves. "
		context += "Consider ETH/BTC ratio for relative strength analysis.\n"

	default:
		context += fmt.Sprintf("%s is an altcoin. Typically higher volatility than BTC/ETH. ", baseAsset)
		context += "Lower liquidity requires smaller positions. Strong correlation with BTC movements.\n"
	}

	return context
}

// buildTimeframeContext builds context specific to a timeframe
func (c *ContextBuilder) buildTimeframeContext(timeframe string) string {
	context := fmt.Sprintf("## Timeframe Context: %s\n\n", timeframe)

	switch {
	case strings.Contains(timeframe, "1m") || strings.Contains(timeframe, "5m"):
		context += "Very short timeframe for scalping. Requires quick decisions and tight stops. "
		context += "Focus on order flow and tape reading. Noise is high, so require strong confluence.\n"

	case strings.Contains(timeframe, "15m") || strings.Contains(timeframe, "1h"):
		context += "Medium timeframe for intraday trading. Balance between noise and signal. "
		context += "Technical structure more reliable. Good for swing entries.\n"

	case strings.Contains(timeframe, "4h") || strings.Contains(timeframe, "1d"):
		context += "Higher timeframe for trend analysis. Lower noise, more reliable signals. "
		context += "Use for overall bias and major support/resistance levels.\n"

	default:
		context += "Standard timeframe analysis applies.\n"
	}

	return context
}

// getStyleSteeringFile returns the steering file for a specific trading style
func (c *ContextBuilder) getStyleSteeringFile(style string) *SteeringFile {
	if c.steeringManager == nil {
		return nil
	}

	// Map trading style to steering file name
	var fileName string
	switch strings.ToLower(style) {
	case "scalping":
		fileName = "scalping-knowledge.md"
	case "day_trading", "daytrading":
		fileName = "day-trading-knowledge.md"
	case "swing_trading", "swingtrading":
		fileName = "swing-trading-knowledge.md"
	default:
		return nil
	}

	// Get all steering files and find the matching one
	allFiles := c.steeringManager.GetAllFiles()
	for _, file := range allFiles {
		if strings.HasSuffix(file.Path, fileName) {
			return file
		}
	}

	return nil
}

// ClearCache clears the context cache
func (c *ContextBuilder) ClearCache() {
	if c.cache != nil {
		c.cache.Clear()
	}
}

// CleanExpiredCache removes expired entries from the cache
func (c *ContextBuilder) CleanExpiredCache() int {
	if c.cache != nil {
		return c.cache.CleanExpired()
	}
	return 0
}

// GetCacheSize returns the number of cached contexts
func (c *ContextBuilder) GetCacheSize() int {
	if c.cache != nil {
		return c.cache.Size()
	}
	return 0
}
