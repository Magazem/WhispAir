package pipeline

import (
	"context"
	"strings"
	"time"

	"github.com/Magazem/WhispAir/server/llm"
	"github.com/Magazem/WhispAir/types"
	"go.uber.org/zap"
)

type Extractor struct {
	logger  *zap.Logger
	llm     llm.Client
	prompts *PromptLoader
	// AllowFallback controls whether an LLM failure silently falls back
	// to keyword extraction. When false, LLM errors are returned to the
	// caller instead of being swallowed.
	AllowFallback bool
}

func NewExtractor(logger *zap.Logger, l llm.Client, p *PromptLoader) *Extractor {
	return &Extractor{logger: logger, llm: l, prompts: p}
}

// SetAllowFallback sets whether LLM failures fall back to keyword extraction.
func (e *Extractor) SetAllowFallback(allow bool) {
	e.AllowFallback = allow
}

func (e *Extractor) Extract(ctx context.Context, text string, category string) ([]types.ExtractedItem, error) {
	if e.llm != nil {
		items, err := e.llm.Extract(ctx, text, category)
		if err == nil {
			return items, nil
		}
		if !e.AllowFallback {
			e.logger.Warn("LLM extract failed, fallback disabled", zap.Error(err))
			return nil, err
		}
		e.logger.Warn("LLM extract failed, using fallback", zap.Error(err))
	}
	return extractByKeywords(text, category), nil
}

type Critic struct {
	logger *zap.Logger
	llm    llm.Client
	// AllowFallback controls whether an LLM failure silently returns the
	// unrefined items. When false, LLM errors are returned to the caller.
	AllowFallback bool
}

func NewCritic(logger *zap.Logger, l llm.Client) *Critic {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Critic{logger: logger, llm: l}
}

// SetAllowFallback sets whether LLM failures fall back to unrefined items.
func (c *Critic) SetAllowFallback(allow bool) {
	c.AllowFallback = allow
}

func (c *Critic) Review(ctx context.Context, text string, items []types.ExtractedItem) ([]types.ExtractedItem, error) {
	if c.llm != nil {
		refined, err := c.llm.Critic(ctx, text, items)
		if err == nil {
			return refined, nil
		}
		if !c.AllowFallback {
			c.logger.Warn("LLM critic failed, fallback disabled", zap.Error(err))
			return nil, err
		}
		c.logger.Warn("LLM critic failed, using fallback", zap.Error(err))
	}
	return items, nil
}

func extractByKeywords(text string, category string) []types.ExtractedItem {
	var items []types.ExtractedItem
	for _, s := range splitSentences(text) {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}

		t := category
		target := "Chat.md"
		switch category {
		case "task":
			target = "Later.md"
		case "idea":
			target = "brain/" + sanitizeFilename(s) + ".md"
		case "journal":
			target = "journal/" + time.Now().Format("2006.01") + " " + monthName(time.Now().Month()) + ".md"
		case "mixed":
			t = classifyByKeywords(s).Category
			switch t {
			case "task":
				target = "Later.md"
			case "idea":
				target = "brain/" + sanitizeFilename(s) + ".md"
			case "journal":
				target = "journal/" + time.Now().Format("2006.01") + " " + monthName(time.Now().Month()) + ".md"
			}
		}
		items = append(items, types.ExtractedItem{Type: t, Text: s, Target: target})
	}
	return items
}

func splitSentences(text string) []string {
	if text == "" {
		return nil
	}
	var out []string
	start, hasContent := 0, false
	for i := 0; i < len(text); i++ {
		switch text[i] {
		case '.', '!', '?':
			if hasContent {
				out = append(out, text[start:i+1])
			}
			start, hasContent = i+1, false
		case ' ', '\t', '\n':
		default:
			hasContent = true
		}
	}
	if start < len(text) && hasContent {
		out = append(out, text[start:])
	}
	return out
}

func sanitizeFilename(s string) string {
	var sb strings.Builder
	for _, r := range s {
		c := byte(r)
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_':
			sb.WriteByte(c)
		case c == ' ':
			sb.WriteByte('-')
		}
		if sb.Len() > 50 {
			break
		}
	}
	if sb.Len() == 0 {
		return "untitled"
	}
	return sb.String()
}

func monthName(m time.Month) string {
	names := []string{"January", "February", "March", "April", "May", "June", "July", "August", "September", "October", "November", "December"}
	i := int(m) - 1
	if i < 0 || i > 11 {
		i = 0
	}
	return names[i]
}
