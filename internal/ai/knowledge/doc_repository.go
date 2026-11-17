package knowledge

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// TrainingDocRepository manages training documentation
type TrainingDocRepository struct {
	docsDir   string
	documents map[string]*TrainingDocument
	sections  map[string]*KnowledgeSection // Index by section ID for fast lookup
}

// NewTrainingDocRepository creates a new training document repository
func NewTrainingDocRepository(docsDir string) *TrainingDocRepository {
	return &TrainingDocRepository{
		docsDir:   docsDir,
		documents: make(map[string]*TrainingDocument),
		sections:  make(map[string]*KnowledgeSection),
	}
}

// Load loads all training documents from the directory
func (t *TrainingDocRepository) Load(ctx context.Context) error {
	// Check if directory exists
	if _, err := os.Stat(t.docsDir); os.IsNotExist(err) {
		// Create directory if it doesn't exist
		if err := os.MkdirAll(t.docsDir, 0755); err != nil {
			return fmt.Errorf("failed to create training docs directory: %w", err)
		}
		return nil
	}

	// Walk through directory and load all .md files
	err := filepath.Walk(t.docsDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip directories and non-markdown files
		if info.IsDir() || !strings.HasSuffix(info.Name(), ".md") {
			return nil
		}

		// Load and parse the document
		doc, err := t.loadDocument(path, info)
		if err != nil {
			return fmt.Errorf("failed to load training document %s: %w", path, err)
		}

		// Store in map
		relPath, _ := filepath.Rel(t.docsDir, path)
		t.documents[relPath] = doc

		// Index sections for fast lookup
		for id, section := range doc.Sections {
			t.sections[id] = section
		}

		return nil
	})

	if err != nil {
		return fmt.Errorf("failed to walk training docs directory: %w", err)
	}

	return nil
}

// loadDocument loads and parses a single training document
func (t *TrainingDocRepository) loadDocument(path string, info os.FileInfo) (*TrainingDocument, error) {
	// Read file content
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	contentStr := string(content)

	// Extract title from first heading
	title := t.extractTitle(contentStr)
	if title == "" {
		title = filepath.Base(path)
	}

	// Parse sections from markdown headings
	sections := t.parseSections(contentStr)

	return &TrainingDocument{
		Path:        path,
		Title:       title,
		Sections:    sections,
		LastUpdated: info.ModTime(),
	}, nil
}

// extractTitle extracts the document title from the first # heading
func (t *TrainingDocRepository) extractTitle(content string) string {
	re := regexp.MustCompile(`(?m)^#\s+(.+)$`)
	matches := re.FindStringSubmatch(content)
	if len(matches) > 1 {
		return strings.TrimSpace(matches[1])
	}
	return ""
}

// parseSections parses markdown sections based on headings
func (t *TrainingDocRepository) parseSections(content string) map[string]*KnowledgeSection {
	sections := make(map[string]*KnowledgeSection)

	// Split content by ## headings (level 2)
	re := regexp.MustCompile(`(?m)^##\s+(.+)$`)
	matches := re.FindAllStringSubmatchIndex(content, -1)

	for i, match := range matches {
		// Extract section title
		titleStart := match[2]
		titleEnd := match[3]
		title := content[titleStart:titleEnd]

		// Extract section content
		contentStart := match[1]
		var contentEnd int
		if i < len(matches)-1 {
			contentEnd = matches[i+1][0]
		} else {
			contentEnd = len(content)
		}

		sectionContent := content[contentStart:contentEnd]

		// Generate section ID from title
		sectionID := t.generateSectionID(title)

		// Determine category and tags
		category := t.categorizeSection(title, sectionContent)
		tags := t.extractTags(title, sectionContent)

		sections[sectionID] = &KnowledgeSection{
			ID:          sectionID,
			Title:       title,
			Content:     sectionContent,
			Category:    category,
			Tags:        tags,
			LastUpdated: time.Now(),
		}
	}

	return sections
}

// generateSectionID creates a unique ID from section title
func (t *TrainingDocRepository) generateSectionID(title string) string {
	// Convert to lowercase and replace spaces with underscores
	id := strings.ToLower(title)
	id = regexp.MustCompile(`[^a-z0-9]+`).ReplaceAllString(id, "_")
	id = strings.Trim(id, "_")
	return id
}

// categorizeSection determines the category of a section
func (t *TrainingDocRepository) categorizeSection(title, content string) string {
	titleLower := strings.ToLower(title)
//	contentLower := strings.ToLower(content)

	// Check for keywords to categorize
	if strings.Contains(titleLower, "risk") || strings.Contains(titleLower, "gestión") {
		return "risk_management"
	}
	if strings.Contains(titleLower, "setup") || strings.Contains(titleLower, "identificación") {
		return "setup_identification"
	}
	if strings.Contains(titleLower, "order flow") || strings.Contains(titleLower, "tape") {
		return "order_flow"
	}
	if strings.Contains(titleLower, "funding") || strings.Contains(titleLower, "liquidaciones") {
		return "derivatives"
	}
	if strings.Contains(titleLower, "on-chain") || strings.Contains(titleLower, "blockchain") {
		return "on_chain"
	}
	if strings.Contains(titleLower, "análisis") || strings.Contains(titleLower, "analysis") {
		return "market_analysis"
	}
	if strings.Contains(titleLower, "indicador") || strings.Contains(titleLower, "indicator") {
		return "technical_indicators"
	}
	if strings.Contains(titleLower, "prompt") {
		return "prompts"
	}

	return "general"
}

// extractTags extracts relevant tags from title and content
func (t *TrainingDocRepository) extractTags(title, content string) []string {
	tags := []string{}
	combined := strings.ToLower(title + " " + content)

	// Define tag keywords
	tagKeywords := map[string]string{
		"scalping":    "scalping",
		"hft":         "hft",
		"order_flow":  "order flow",
		"tape":        "tape reading",
		"funding":     "funding",
		"liquidation": "liquidation",
		"risk":        "risk",
		"setup":       "setup",
		"confluence":  "confluence",
		"validation":  "validation",
		"derivatives": "derivatives",
		"on_chain":    "on-chain",
		"technical":   "technical",
		"indicator":   "indicator",
	}

	for tag, keyword := range tagKeywords {
		if strings.Contains(combined, keyword) {
			tags = append(tags, tag)
		}
	}

	return tags
}

// Count returns the number of loaded documents
func (t *TrainingDocRepository) Count() int {
	return len(t.documents)
}

// SectionCount returns the total number of sections across all documents
func (t *TrainingDocRepository) SectionCount() int {
	return len(t.sections)
}

// GetSection returns a section by ID
func (t *TrainingDocRepository) GetSection(sectionID string) (*KnowledgeSection, bool) {
	section, ok := t.sections[sectionID]
	return section, ok
}

// GetSectionsByCategory returns all sections in a category
func (t *TrainingDocRepository) GetSectionsByCategory(category string) []*KnowledgeSection {
	sections := make([]*KnowledgeSection, 0)
	for _, section := range t.sections {
		if section.Category == category {
			sections = append(sections, section)
		}
	}
	return sections
}

// GetSectionsByTag returns all sections with a specific tag
func (t *TrainingDocRepository) GetSectionsByTag(tag string) []*KnowledgeSection {
	sections := make([]*KnowledgeSection, 0)
	for _, section := range t.sections {
		for _, sectionTag := range section.Tags {
			if sectionTag == tag {
				sections = append(sections, section)
				break
			}
		}
	}
	return sections
}

// SearchSections searches for sections containing a keyword
func (t *TrainingDocRepository) SearchSections(keyword string) []*KnowledgeSection {
	sections := make([]*KnowledgeSection, 0)
	keywordLower := strings.ToLower(keyword)

	for _, section := range t.sections {
		if strings.Contains(strings.ToLower(section.Title), keywordLower) ||
			strings.Contains(strings.ToLower(section.Content), keywordLower) {
			sections = append(sections, section)
		}
	}

	return sections
}
