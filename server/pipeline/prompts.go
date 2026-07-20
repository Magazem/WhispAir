package pipeline

import (
	"os"
	"strings"
	"time"

	"go.uber.org/zap"
)

// PromptSet holds all configurable prompts
type PromptSet struct {
	SystemContext  string
	Classifier     string
	ExtractTask    string
	ExtractIdea    string
	ExtractJournal string
	ExtractMixed   string
	Critic         string
	Router         string
}

// PromptLoader manages prompts (currently defaults; file-based loading later)
type PromptLoader struct {
	prompts PromptSet
}

// NewPromptLoader creates a new prompt loader
func NewPromptLoader(path string, logger *zap.Logger) *PromptLoader {
	pl := &PromptLoader{prompts: defaultPrompts()}

	if _, err := os.Stat(path); err == nil {
		data, err := os.ReadFile(path)
		if err == nil {
			pl.prompts = parseYAML(string(data))
		}
	}
	return pl
}

// Get returns the current prompt set
func (pl *PromptLoader) Get() PromptSet {
	return pl.prompts
}

// Render returns the extraction prompt for a category with {{ message }} replaced
func (pl *PromptLoader) Render(category string, message string) string {
	p := pl.prompts
	var t string
	switch category {
	case "task":
		t = p.ExtractTask
	case "idea":
		t = p.ExtractIdea
	case "journal":
		t = p.ExtractJournal
	default:
		t = p.ExtractMixed
	}

	t = strings.ReplaceAll(t, "{{ system_context }}", p.SystemContext)
	t = strings.ReplaceAll(t, "{{ message }}", message)
	t = strings.ReplaceAll(t, "{{ current_month }}", time.Now().Format("2006.01"))
	return t
}

// RenderClassifier returns the classifier prompt
func (pl *PromptLoader) RenderClassifier(message string) string {
	p := pl.prompts
	t := p.Classifier
	t = strings.ReplaceAll(t, "{{ system_context }}", p.SystemContext)
	t = strings.ReplaceAll(t, "{{ message }}", message)
	return t
}

func defaultPrompts() PromptSet {
	return PromptSet{
		SystemContext:  "You are Memoire, a private thought-capture system. Preserve your user's exact voice and phrasing.",
		Classifier:     "Classify the message into ONE category: task, idea, journal, mixed.\nMessage: {{ message }}\n\nReturn ONLY JSON: {\"category\":\"...\",\"confidence\":0.0-1.0}",
		ExtractTask:    "Extract actionable tasks.\nMessage: {{ message }}\n\nReturn JSON array: [{\"type\":\"task\",\"text\":\"...\",\"target\":\"Later.md\"}]",
		ExtractIdea:    "Extract ideas and insights.\nMessage: {{ message }}\n\nReturn JSON array: [{\"type\":\"idea\",\"text\":\"...\",\"target\":\"brain/name.md\"}]",
		ExtractJournal: "Extract journal-worthy personal reflections.\nMessage: {{ message }}\n\nReturn JSON array: [{\"type\":\"journal\",\"text\":\"...\",\"target\":\"journal/{{ current_month }}.md\"}]",
		ExtractMixed:   "Extract and categorize all content.\nMessage: {{ message }}\n\nReturn JSON array with items of appropriate types (task/idea/journal).",
		Critic:         "Review the extraction. Check for missed items, misclassifications, split/merge opportunities.",
		Router:         "Given an item and message context, determine the best target file and any related files.",
	}
}

func parseYAML(s string) PromptSet {
	p := defaultPrompts()
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if i := strings.Index(line, ":"); i > 0 && !strings.HasPrefix(line, "#") && !strings.HasPrefix(line, "-") {
			key := strings.TrimSpace(line[:i])
			val := strings.Trim(strings.TrimSpace(line[i+1:]), `"`)
			switch key {
			case "system_context":
				if val != "" { p.SystemContext = val }
			case "classifier":
				if val != "" { p.Classifier = val }
			case "extract_task":
				if val != "" { p.ExtractTask = val }
			case "extract_idea":
				if val != "" { p.ExtractIdea = val }
			case "extract_journal":
				if val != "" { p.ExtractJournal = val }
			case "extract_mixed":
				if val != "" { p.ExtractMixed = val }
			case "critic":
				if val != "" { p.Critic = val }
			case "router":
				if val != "" { p.Router = val }
			}
		}
	}
	return p
}
