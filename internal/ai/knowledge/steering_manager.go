package knowledge

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// SteeringFileManager manages Kiro steering files
type SteeringFileManager struct {
	steeringDir string
	files       map[string]*SteeringFile
}

// NewSteeringFileManager creates a new steering file manager
func NewSteeringFileManager(steeringDir string) *SteeringFileManager {
	return &SteeringFileManager{
		steeringDir: steeringDir,
		files:       make(map[string]*SteeringFile),
	}
}

// Load loads all steering files from the directory
func (s *SteeringFileManager) Load(ctx context.Context) error {
	// Check if directory exists
	if _, err := os.Stat(s.steeringDir); os.IsNotExist(err) {
		// Create directory if it doesn't exist
		if err := os.MkdirAll(s.steeringDir, 0755); err != nil {
			return fmt.Errorf("failed to create steering directory: %w", err)
		}
		return nil
	}

	// Walk through directory and load all .md files
	err := filepath.Walk(s.steeringDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip directories and non-markdown files
		if info.IsDir() || !strings.HasSuffix(info.Name(), ".md") {
			return nil
		}

		// Load and parse the file
		steeringFile, err := s.loadFile(path)
		if err != nil {
			return fmt.Errorf("failed to load steering file %s: %w", path, err)
		}

		// Store in map
		relPath, _ := filepath.Rel(s.steeringDir, path)
		s.files[relPath] = steeringFile

		return nil
	})

	if err != nil {
		return fmt.Errorf("failed to walk steering directory: %w", err)
	}

	return nil
}

// loadFile loads and parses a single steering file
func (s *SteeringFileManager) loadFile(path string) (*SteeringFile, error) {
	// Read file content
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	contentStr := string(content)

	// Parse frontmatter if present
	inclusion := "always" // default
	pattern := ""

	if strings.HasPrefix(contentStr, "---") {
		// Extract frontmatter
		parts := strings.SplitN(contentStr, "---", 3)
		if len(parts) >= 3 {
			frontmatter := parts[1]
			contentStr = parts[2]

			// Parse inclusion type
			if match := regexp.MustCompile(`inclusion:\s*(\w+)`).FindStringSubmatch(frontmatter); len(match) > 1 {
				inclusion = match[1]
			}

			// Parse file match pattern
			if match := regexp.MustCompile(`fileMatchPattern:\s*['"]([^'"]+)['"]`).FindStringSubmatch(frontmatter); len(match) > 1 {
				pattern = match[1]
			}
		}
	}

	// Extract file references (#[[file:path]])
	references := s.extractFileReferences(contentStr)

	return &SteeringFile{
		Path:       path,
		Inclusion:  inclusion,
		Pattern:    pattern,
		Content:    contentStr,
		References: references,
	}, nil
}

// extractFileReferences finds all file references in the content
func (s *SteeringFileManager) extractFileReferences(content string) []string {
	// Match pattern: #[[file:path/to/file.md]]
	re := regexp.MustCompile(`#\[\[file:([^\]]+)\]\]`)
	matches := re.FindAllStringSubmatch(content, -1)

	references := make([]string, 0, len(matches))
	for _, match := range matches {
		if len(match) > 1 {
			references = append(references, match[1])
		}
	}

	return references
}

// Count returns the number of loaded steering files
func (s *SteeringFileManager) Count() int {
	return len(s.files)
}

// GetFile returns a steering file by path
func (s *SteeringFileManager) GetFile(path string) (*SteeringFile, bool) {
	file, ok := s.files[path]
	return file, ok
}

// GetAllFiles returns all loaded steering files
func (s *SteeringFileManager) GetAllFiles() []*SteeringFile {
	files := make([]*SteeringFile, 0, len(s.files))
	for _, file := range s.files {
		files = append(files, file)
	}
	return files
}

// GetAlwaysIncluded returns steering files that should always be included
func (s *SteeringFileManager) GetAlwaysIncluded() []*SteeringFile {
	files := make([]*SteeringFile, 0)
	for _, file := range s.files {
		if file.Inclusion == "always" {
			files = append(files, file)
		}
	}
	return files
}
