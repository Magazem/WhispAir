package pipeline

import (
	"fmt"
	"os"
	"strings"
	"sync"

	"go.uber.org/zap"
	"gopkg.in/yaml.v3"
)

// PromptSet holds all prompts from prompts.yaml
type PromptSet struct {
	SystemContext string            `yaml:"system_context"`
	Classifier    string            `yaml:"classifier"`
	ExtractTask   string            `yaml:"extract_task"`
	ExtractIdea   string            `yaml:"extract_idea"`
	ExtractJournal string           `yaml:"extract_journal"`
	ExtractMixed  string            `yaml:"extract_mixed"`
	Critic        string            `yaml:"critic"`
	Router        string            `yaml:"router"`
}

// PromptLoader manages hot-reloadable prompts
type PromptLoader struct {
	mu       sync.RWMutex
	prompts  PromptSet
	path     string
	logger   *zap.Logger
	lastMod  int64
}

// NewPromptLoader creates a new prompt loader
func NewPromptLoader(path string, logger *zap.Logger) *PromptLoader {
	if logger == nil {
		logger = zap.NewNop()
	}
	pl := &PromptLoader{
		path:   path,
		logger: logger,
	}
	
	// Load initial prompts if file exists
	if _, err := os.Stat(path); err == nil {
		if err := pl.Load(); err != nil {
			logger.Warn("failed to load prompts", zap.Error(err))
		}
	} else {
		logger.Info("prompts file not found, using defaults",
			zap.String("path", path))
		pl.prompts = defaultPrompts()
	}
	
	return pl
}

// Load reads prompts from disk
func (pl *PromptLoader) Load() error {
	info, err := os.Stat(pl.path)
	if err != nil {
		return fmt.Errorf("cannot stat prompts file: %w", err)
	}
	
	// Skip if not modified
	if info.ModTime().Unix() == pl.lastMod && pl.lastMod > 0 {
		return nil
	}
	
	data, err := os.ReadFile(pl.path)
	if err != nil {
		return fmt.Errorf("cannot read prompts file: %w", err)
	}
	
	var newPrompts PromptSet
	if err := yaml.Unmarshal(data, &newPrompts); err != nil {
		return fmt.Errorf("cannot parse prompts YAML: %w", err)
	}
	
	pl.mu.Lock()
	pl.prompts = newPrompts
	pl.lastMod = info.ModTime().Unix()
	pl.mu.Unlock()
	
	pl.logger.Info("prompts loaded",
		zap.String("path", pl.path),
		zap.Time("modified", info.ModTime()))
	
	return nil
}

// Get returns the current prompt set
func (pl *PromptLoader) Get() PromptSet {
	pl.mu.RLock()
	defer pl.mu.RUnlock()
	return pl.prompts
}

// RenderCategory returns the extraction prompt for a category
func (pl *PromptLoader) RenderCategory(category string, message string) string {
	p := pl.Get()
	var template string
	
	switch category {
	case "task":
		template = p.ExtractTask
	case "idea":
		template = p.ExtractIdea
	case "journal":
		template = p.ExtractJournal
	case "mixed":
		template = p.ExtractMixed
	default:
		template = p.ExtractMixed
	}
	
	// Replace placeholders
	result := strings.ReplaceAll(template, "{{ system_context }}", p.SystemContext)
	result = strings.ReplaceAll(result, "{{ message }}", message)
	
	return result
}

// RenderClassifier returns the classifier prompt
func (pl *PromptLoader) RenderClassifier(message string) string {
	p := pl.Get()
	template := p.Classifier
	
	result := strings.ReplaceAll(template, "{{ system_context }}", p.SystemContext)
	result = strings.ReplaceAll(result, "{{ message }}", message)
	
	return result
}

// RenderCritic returns the critic prompt
func (pl *PromptLoader) RenderCritic(message string, items string) string {
	p := pl.Get()
	template := p.Critic
	
	result := strings.ReplaceAll(template, "{{ system_context }}", p.SystemContext)
	result = strings.ReplaceAll(result, "{{ message }}", message)
	result = strings.ReplaceAll(result, "{{ extracted_items }}", items)
	
	return result
}

// defaultPrompts returns default prompts if no file exists
func defaultPrompts() PromptSet {
	return PromptSet{
		SystemContext: "You are Memoire, a private thought-capture system. Preserve the user's exact voice.",
		Classifier: `Classify the message: {{ message }}

Categories: task, idea, journal, mixed

Return ONLY JSON: {"category": "...", "confidence": 0.0-1.0}`,
		ExtractTask: `Extract tasks from: {{ message }}

Return JSON array: [{"type":"task","text":"...","target":"Later.md"}]`,
		ExtractIdea: `Extract ideas from: {{ message }}

Return JSON array: [{"type":"idea","text":"...","target":"brain/<name>.md"}]`,
		ExtractJournal: `Extract journal content from: {{ message }}

Return JSON array: [{"type":"journal","text":"...","target":"journal/<month>.md"}]`,
		ExtractMixed: `Extract and categorize content from: {{ message }}

Return JSON array with items of appropriate types.`,
		Critic: `Review extraction from: {{ message }}

Items: {{ extracted_items }}

Return JSON: {"missed":[],"reclassified":[],"splittable":[],"mergeable":[]}`,
		Router: `Given item: {{ item }}
Context: {{ message }}

Return JSON: {"target":"...","related_files":[]}`,
	}
}

// Ensure imports are used
var _ = yaml.Unmarshal
