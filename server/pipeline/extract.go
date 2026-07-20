package pipeline

import (
	"context"
	"fmt"
	"time"

	"github.com/Magazem/WhispAir/types"
	"go.uber.org/zap"
)

// Extractor handles extracting structured items from text
type Extractor struct {
	logger *zap.Logger
}

// NewExtractor creates a new extractor
func NewExtractor(logger *zap.Logger) *Extractor {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Extractor{logger: logger}
}

// Extract items from text based on category
func (e *Extractor) Extract(ctx context.Context, text string, category string) ([]types.ExtractedItem, error) {
	e.logger.Debug("extracting", zap.String("category", category), zap.String("text", text))

	items := []types.ExtractedItem{}

	// Split into sentences
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
			// Classify each sentence
			cl := classifyByKeywords(s)
			itemType = cl.Category
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

	return items, nil
}

// Helper functions
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
	// Add remaining text if there is content
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
