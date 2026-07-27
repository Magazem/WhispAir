package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
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
	// AllowMock permits falling back to keyword-based mock results when the
	// LLM is unavailable. When false, errors propagate to the caller.
	AllowMock bool
}

// NewClient creates a new LLM client with AllowMock=false.
// Kept for backward compatibility — server.go calls this.
func NewClient(ollamaHost, claudeKey string, logger *zap.Logger) Client {
	return NewClientWithOptions(Config{
		OllamaHost: ollamaHost,
		ClaudeKey:  claudeKey,
		Logger:     logger,
		AllowMock:  false,
	})
}

// NewClientWithOptions creates a new LLM client with full configuration.
func NewClientWithOptions(cfg Config) Client {
	if cfg.Logger == nil {
		cfg.Logger = zap.NewNop()
	}
	return &DefaultClient{
		config: cfg,
		httpClient: &http.Client{
			Timeout: 120 * time.Second,
		},
	}
}

// Classify the given text
func (c *DefaultClient) Classify(ctx context.Context, text string) (types.Classification, error) {
	c.config.Logger.Debug("classify", zap.String("text", text))

	result, err := c.classifyOllama(ctx, text)
	if err == nil {
		return result, nil
	}

	if c.config.AllowMock {
		c.config.Logger.Warn("MOCK LLM - not real inference")
		return mockClassify(text), nil
	}

	return types.Classification{}, err
}

// Extract items from text
func (c *DefaultClient) Extract(ctx context.Context, text string, category string) ([]types.ExtractedItem, error) {
	c.config.Logger.Debug("extract", zap.String("category", category))

	items, err := c.extractOllama(ctx, text, category)
	if err == nil {
		return items, nil
	}

	if c.config.AllowMock {
		c.config.Logger.Warn("MOCK LLM - not real inference")
		return mockExtract(text, category), nil
	}

	return nil, err
}

// Critically review and refine items
func (c *DefaultClient) Critic(ctx context.Context, text string, items []types.ExtractedItem) ([]types.ExtractedItem, error) {
	c.config.Logger.Debug("critic", zap.Int("items", len(items)))

	refined, err := c.criticOllama(ctx, text, items)
	if err == nil {
		return refined, nil
	}

	if c.config.AllowMock {
		c.config.Logger.Warn("MOCK LLM - not real inference")
		return items, nil
	}

	return nil, err
}

// callOllamaWithRetry sends a prompt to Ollama and parses the response with
// parseFunc. On parse failure it appends the raw response and the error to the
// prompt and retries, up to 3 attempts total. Every attempt is logged.
func (c *DefaultClient) callOllamaWithRetry(
	ctx context.Context,
	model string,
	basePrompt string,
	parseFunc func(raw string) error,
) error {
	var lastErr error
	var lastRaw string

	for attempt := 1; attempt <= 3; attempt++ {
		prompt := basePrompt
		if attempt > 1 {
			// Feed the previous raw response and parse error back into the prompt.
			prompt = basePrompt + fmt.Sprintf(
				"\n\nPrevious attempt returned invalid JSON.\nRaw response: %s\nParse error: %s\nPlease return ONLY valid JSON, no other text.",
				lastRaw, lastErr,
			)
		}

		reqBody := map[string]interface{}{
			"model":  model,
			"prompt": prompt,
			"stream": false,
			"format": "json",
		}

		var resp struct {
		 Response string `json:"response"`
		}

		if err := c.doOllamaRequest(ctx, "/api/generate", reqBody, &resp); err != nil {
			lastErr = err
			c.config.Logger.Warn("ollama request failed",
				zap.Int("attempt", attempt),
				zap.Error(err),
			)
			continue
		}

		// Defensively strip <think> reasoning blocks Qwen 3 emits even with format:json.
		raw := stripThinkBlocks(resp.Response)
		lastRaw = raw

		if err := parseFunc(raw); err != nil {
			lastErr = err
			c.config.Logger.Warn("ollama response parse failed",
				zap.Int("attempt", attempt),
				zap.Error(err),
			)
			continue
		}

		return nil
	}

	return fmt.Errorf("ollama call failed after 3 attempts, last error: %w", lastErr)
}

// classifyOllama calls the local Ollama instance
func (c *DefaultClient) classifyOllama(ctx context.Context, text string) (types.Classification, error) {
	if c.config.OllamaHost == "" {
		return types.Classification{}, fmt.Errorf("ollama host not configured")
	}

	basePrompt := fmt.Sprintf(`You are a classifier for personal thought-capture messages. Given the message below, classify it into one of these categories:
- task: actionable items, things to do
- idea: insights, concepts, hunches
- journal: personal reflections, experiences
- mixed: contains multiple types

Respond with ONLY a JSON object: {"category": "...", "confidence": 0.0-1.0}

Message: %s`, text)

	var result types.Classification
	if err := c.callOllamaWithRetry(ctx, "qwen3:0.6b", basePrompt, func(raw string) error {
		return json.Unmarshal([]byte(raw), &result)
	}); err != nil {
		return types.Classification{}, err
	}

	return result, nil
}

// extractOllama calls the local Ollama instance for extraction
func (c *DefaultClient) extractOllama(ctx context.Context, text string, category string) ([]types.ExtractedItem, error) {
	if c.config.OllamaHost == "" {
		return nil, fmt.Errorf("ollama host not configured")
	}

	basePrompt := fmt.Sprintf(`You are an extractor for personal thought-capture messages. Given the message below and its category "%s", extract structured items.

For each item:
- type: "task", "idea", or "journal"
- text: the exact extracted text
- target: the file it should go to (e.g., "Later.md", "brain/some-idea.md", "journal/current.md")

Respond with ONLY a JSON array: [{"type": "...", "text": "...", "target": "..."}]

Message: %s`, category, text)

	var items []types.ExtractedItem
	if err := c.callOllamaWithRetry(ctx, "qwen3:8b", basePrompt, func(raw string) error {
		return json.Unmarshal([]byte(raw), &items)
	}); err != nil {
		return nil, err
	}

	// TODO: set ExtractedItem.Confidence from the model's own confidence once the
	// field is added to types.ExtractedItem (another task owns that change).
	return items, nil
}

// criticResponse is the JSON shape the critic model returns.
type criticResponse struct {
	Missed []string `json:"missed"`
	Reclassified []struct {
		OldType string `json:"old_type"`
		NewType string `json:"new_type"`
		Text    string `json:"text"`
	} `json:"reclassified"`
	Splittable []struct {
		Original string   `json:"original"`
		Parts    []string `json:"parts"`
	} `json:"splittable"`
}

// criticOllama calls the local Ollama for criticism and applies the response.
func (c *DefaultClient) criticOllama(ctx context.Context, text string, items []types.ExtractedItem) ([]types.ExtractedItem, error) {
	if c.config.OllamaHost == "" {
		return nil, fmt.Errorf("ollama host not configured")
	}

	itemsJSON, _ := json.Marshal(items)
	basePrompt := fmt.Sprintf(`You are a critic reviewing extracted items from a thought-capture message. For each item, check:
1. Was anything missed? Return "missed": ["item text"]
2. Was anything misclassified? Return "reclassified": [{"old_type": "...", "new_type": "...", "text": "..."}]
3. Can any item be split? Return "splittable": [{"original": "...", "parts": ["..."]}]

Return a JSON object with these keys. If nothing needs correction, return: {"missed": [], "reclassified": [], "splittable": []}

Original message: %s

Extracted items: %s`, text, string(itemsJSON))

	var cr criticResponse
	if err := c.callOllamaWithRetry(ctx, "qwen3:8b", basePrompt, func(raw string) error {
		return json.Unmarshal([]byte(raw), &cr)
	}); err != nil {
		return nil, err
	}

	return applyCritic(items, cr), nil
}

// applyCritic applies the critic's feedback to the extracted items:
// appends missed items, reclassifies items, and splits splittable items.
func applyCritic(items []types.ExtractedItem, cr criticResponse) []types.ExtractedItem {
	result := make([]types.ExtractedItem, len(items))
	copy(result, items)

	// Apply reclassifications — change Type and update Target when the
	// new type implies a different target file.
	for _, r := range cr.Reclassified {
		for i := range result {
			if result[i].Text == r.Text && result[i].Type == r.OldType {
				result[i].Type = r.NewType
				switch r.NewType {
				case "task":
					result[i].Target = "Later.md"
				case "idea":
					result[i].Target = "brain/" + sanitizeFilename(r.Text) + ".md"
				case "journal":
					result[i].Target = "journal/" + currentMonthFile()
				}
			}
		}
	}

	// Apply splittable — replace the original with its parts, preserving
	// the original item's Type and (where sensible) Target.
	var expanded []types.ExtractedItem
	for _, item := range result {
		split := false
		for _, s := range cr.Splittable {
			if item.Text == s.Original {
				for _, part := range s.Parts {
					newItem := item
					newItem.Text = part
					if item.Type == "idea" {
						newItem.Target = "brain/" + sanitizeFilename(part) + ".md"
					}
					expanded = append(expanded, newItem)
				}
				split = true
				break
			}
		}
		if !split {
			expanded = append(expanded, item)
		}
	}
	result = expanded

	// Append missed items, routing them by detected type.
	for _, m := range cr.Missed {
		itemType := detectTypeFromText(m)
		target := "Chat.md"
		switch itemType {
		case "task":
			target = "Later.md"
		case "idea":
			target = "brain/" + sanitizeFilename(m) + ".md"
		case "journal":
			target = "journal/" + currentMonthFile()
		}
		result = append(result, types.ExtractedItem{
			Type:   itemType,
			Text:   m,
			Target: target,
		})
	}

	return result
}

// stripThinkBlocks removes <think>...</think> reasoning blocks that Qwen 3
// emits even when format:json is set. Handles multiple and unclosed blocks.
func stripThinkBlocks(s string) string {
	for {
		start := strings.Index(s, "<think>")
		if start < 0 {
			break
		}
		rel := strings.Index(s[start:], "</think>")
		if rel < 0 {
			// Unclosed block — strip from <think> to end.
			s = s[:start]
			break
		}
		end := start + rel + len("</think>")
		s = s[:start] + s[end:]
	}
	return s
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