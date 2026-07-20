package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/Magazem/WhispAir/types"
	"go.uber.org/zap"
)

// Client is the interface for LLM interactions
type Client interface {
	Classify(ctx context.Context, text string) (types.Classification, error)
	Extract(ctx context.Context, text string, category string) ([]types.ExtractedItem, error)
	Critic(ctx context.Context, text string, items []types.ExtractedItem) ([]types.ExtractedItem, error)
}

// DefaultClient implements Client
type DefaultClient struct {
	config     Config
	httpClient *http.Client
}

// Config for the LLM client
type Config struct {
	OllamaHost string
	ClaudeKey  string
	Logger     *zap.Logger
}

// NewClient creates a new LLM client
func NewClient(ollamaHost, claudeKey string, logger *zap.Logger) Client {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &DefaultClient{
		config: Config{
			OllamaHost: ollamaHost,
			ClaudeKey:  claudeKey,
			Logger:     logger,
		},
		httpClient: &http.Client{
			Timeout: 120 * time.Second,
		},
	}
}

// Classify the given text
func (c *DefaultClient) Classify(ctx context.Context, text string) (types.Classification, error) {
	c.config.Logger.Debug("classify", zap.String("text", text))

	// Try local Ollama first
	result, err := c.classifyOllama(ctx, text)
	if err == nil {
		return result, nil
	}

	c.config.Logger.Warn("ollama classify failed, using mock", zap.Error(err))
	return mockClassify(text), nil
}

// Extract items from text
func (c *DefaultClient) Extract(ctx context.Context, text string, category string) ([]types.ExtractedItem, error) {
	c.config.Logger.Debug("extract", zap.String("category", category))

	// Try local Ollama first
	items, err := c.extractOllama(ctx, text, category)
	if err == nil {
		return items, nil
	}

	c.config.Logger.Warn("ollama extract failed, using mock", zap.Error(err))
	return mockExtract(text, category), nil
}

// Critically review and refine items
func (c *DefaultClient) Critic(ctx context.Context, text string, items []types.ExtractedItem) ([]types.ExtractedItem, error) {
	c.config.Logger.Debug("critic", zap.Int("items", len(items)))

	// Try local Ollama first
	refined, err := c.criticOllama(ctx, text, items)
	if err == nil {
		return refined, nil
	}

	c.config.Logger.Warn("ollama critic failed, using mock", zap.Error(err))
	return items, nil
}

// classifyOllama calls the local Ollama instance
func (c *DefaultClient) classifyOllama(ctx context.Context, text string) (types.Classification, error) {
	if c.config.OllamaHost == "" {
		return types.Classification{}, fmt.Errorf("ollama host not configured")
	}

	prompt := fmt.Sprintf(`You are a classifier for personal thought-capture messages. Given the message below, classify it into one of these categories:
- task: actionable items, things to do
- idea: insights, concepts, hunches
- journal: personal reflections, experiences
- mixed: contains multiple types

Respond with ONLY a JSON object: {"category": "...", "confidence": 0.0-1.0}

Message: %s`, text)

	reqBody := map[string]interface{}{
		"model":  "qwen3:0.6b",
		"prompt": prompt,
		"stream": false,
	}

	var resp struct {
		Response string `json:"response"`
	}

	if err := c.doOllamaRequest(ctx, "/api/generate", reqBody, &resp); err != nil {
		return types.Classification{}, err
	}

	var result types.Classification
	if err := json.Unmarshal([]byte(resp.Response), &result); err != nil {
		return types.Classification{}, fmt.Errorf("failed to parse classify response: %w", err)
	}

	return result, nil
}

// extractOllama calls the local Ollama instance for extraction
func (c *DefaultClient) extractOllama(ctx context.Context, text string, category string) ([]types.ExtractedItem, error) {
	if c.config.OllamaHost == "" {
		return nil, fmt.Errorf("ollama host not configured")
	}

	prompt := fmt.Sprintf(`You are an extractor for personal thought-capture messages. Given the message below and its category "%s", extract structured items.

For each item:
- type: "task", "idea", or "journal"
- text: the exact extracted text
- target: the file it should go to (e.g., "Later.md", "brain/some-idea.md", "journal/current.md")

Respond with ONLY a JSON array: [{"type": "...", "text": "...", "target": "..."}]

Message: %s`, category, text)

	reqBody := map[string]interface{}{
		"model":  "qwen3:8b",
		"prompt": prompt,
		"stream": false,
		"format": "json",
	}

	var resp struct {
		Response string `json:"response"`
	}

	if err := c.doOllamaRequest(ctx, "/api/generate", reqBody, &resp); err != nil {
		return nil, err
	}

	var items []types.ExtractedItem
	if err := json.Unmarshal([]byte(resp.Response), &items); err != nil {
		return nil, fmt.Errorf("failed to parse extract response: %w", err)
	}

	return items, nil
}

// criticOllama calls the local Ollama for criticism
func (c *DefaultClient) criticOllama(ctx context.Context, text string, items []types.ExtractedItem) ([]types.ExtractedItem, error) {
	if c.config.OllamaHost == "" {
		return nil, fmt.Errorf("ollama host not configured")
	}

	itemsJSON, _ := json.Marshal(items)
	prompt := fmt.Sprintf(`You are a critic reviewing extracted items from a thought-capture message. For each item, check:
1. Was anything missed? Return "missed": ["item text"]
2. Was anything misclassified? Return "reclassified": [{"old_type": "...", "new_type": "...", "text": "..."}]
3. Can any item be split? Return "splittable": [{"original": "...", "parts": ["..."]}]

Return a JSON object with these keys. If nothing needs correction, return: {"missed": [], "reclassified": [], "splittable": []}

Original message: %s

Extracted items: %s`, text, string(itemsJSON))

	reqBody := map[string]interface{}{
		"model":  "qwen3:8b",
		"prompt": prompt,
		"stream": false,
		"format": "json",
	}

	var resp struct {
		Response string `json:"response"`
	}

	if err := c.doOllamaRequest(ctx, "/api/generate", reqBody, &resp); err != nil {
		return nil, err
	}

	// For now, return original items
	return items, nil
}

// doOllamaRequest makes a request to the local Ollama instance
func (c *DefaultClient) doOllamaRequest(ctx context.Context, path string, body, result interface{}) error {
	bodyJSON, err := json.Marshal(body)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.config.OllamaHost+path, bytes.NewReader(bodyJSON))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("ollama returned status %d", resp.StatusCode)
	}

	return json.NewDecoder(resp.Body).Decode(result)
}

// mockClassify provides a simple keyword-based classifier for testing
func mockClassify(text string) types.Classification {
	taskKeywords := []string{"todo", "task", "do ", "buy", "order", "call", "send", "finish", "complete"}
	ideaKeywords := []string{"idea", "think", "maybe", "what if", "consider", "hunch", "insight"}
	journalKeywords := []string{"felt", "today", "yesterday", "experience", "realized", "learned"}

	taskScore := countKeywords(text, taskKeywords)
	ideaScore := countKeywords(text, ideaKeywords)
	journalScore := countKeywords(text, journalKeywords)

	maxScore := taskScore
	category := "task"
	confidence := 0.6

	if ideaScore > maxScore {
		maxScore = ideaScore
		category = "idea"
	}
	if journalScore > maxScore {
		maxScore = journalScore
		category = "journal"
	}

	if maxScore > 0 {
		confidence = 0.7 + float64(maxScore)*0.1
		if confidence > 0.95 {
			confidence = 0.95
		}
	}

	// Check for mixed
	if taskScore > 0 && ideaScore > 0 || taskScore > 0 && journalScore > 0 || ideaScore > 0 && journalScore > 0 {
		category = "mixed"
		confidence = 0.6
	}

	return types.Classification{
		Category:   category,
		Confidence: confidence,
	}
}

// mockExtract provides simple extraction for testing
func mockExtract(text string, category string) []types.ExtractedItem {
	items := []types.ExtractedItem{}

	sentences := splitSentences(text)
	for _, s := range sentences {
		s = trimSpace(s)
		if s == "" {
			continue
		}

		itemType := category
		target := "Chat.md"

		switch category {
		case "task":
			target = "Later.md"
		case "idea":
			target = "brain/" + sanitizeFilename(s) + ".md"
		case "journal":
			target = "journal/" + currentMonthFile()
		case "mixed":
			itemType = detectTypeFromText(s)
			switch itemType {
			case "task":
				target = "Later.md"
			case "idea":
				target = "brain/" + sanitizeFilename(s) + ".md"
			case "journal":
				target = "journal/" + currentMonthFile()
			default:
				target = "Chat.md"
			}
		}

		items = append(items, types.ExtractedItem{
			Type:   itemType,
			Text:   s,
			Target: target,
		})
	}

	return items
}

// Helper functions
func countKeywords(text string, keywords []string) int {
	count := 0
	for _, kw := range keywords {
		if containsIgnoreCase(text, kw) {
			count++
		}
	}
	return count
}

func containsIgnoreCase(s, substr string) bool {
	return indexOfLower(s, substr) >= 0
}

func indexOfLower(s, substr string) int {
	sl := toLower(s)
	subl := toLower(substr)
	for i := 0; i <= len(sl)-len(subl); i++ {
		if sl[i:i+len(subl)] == subl {
			return i
		}
	}
	return -1
}

func toLower(s string) string {
	result := ""
	for _, c := range s {
		if c >= 'A' && c <= 'Z' {
			result += string(c + 32)
		} else {
			result += string(c)
		}
	}
	return result
}

func splitSentences(text string) []string {
	if text == "" {
		return nil
	}
	var sentences []string
	start := 0
	hasContent := false
	for i := 0; i < len(text); i++ {
		c := text[i]
		if c == '.' || c == '!' || c == '?' {
			if hasContent {
				sentences = append(sentences, text[start:i+1])
			}
			start = i + 1
			hasContent = false
		} else if c != ' ' && c != '\t' && c != '\n' {
			hasContent = true
		}
	}
	if start < len(text) && hasContent {
		sentences = append(sentences, text[start:])
	}
	return sentences
}

func trimSpace(s string) string {
	start := 0
	end := len(s)
	for start < end && (s[start] == ' ' || s[start] == '\t' || s[start] == '\n') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t' || s[end-1] == '\n') {
		end--
	}
	return s[start:end]
}

func sanitizeFilename(s string) string {
	result := ""
	for _, r := range s {
		c := byte(r)
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_' {
			result += string(c)
		} else if c == ' ' {
			result += "-"
		}
		if len(result) > 50 {
			break
		}
	}
	if result == "" {
		result = "untitled"
	}
	return result
}

func currentMonthFile() string {
	now := time.Now()
	names := []string{"January", "February", "March", "April", "May", "June",
		"July", "August", "September", "October", "November", "December"}
	m := now.Month() - 1
	if m < 0 || m > 11 {
		m = 0
	}
	return fmt.Sprintf("%d.%02d %s.md", now.Year(), now.Month(), names[m])
}

func detectTypeFromText(text string) string {
	cl := mockClassify(text)
	return cl.Category
}
