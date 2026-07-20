package pipeline

import (
	"context"
	"strings"

	"github.com/Magazem/WhispAir/types"
	"go.uber.org/zap"
)

// Classifier handles message classification
type Classifier struct {
	logger *zap.Logger
}

// NewClassifier creates a new classifier
func NewClassifier(logger *zap.Logger) *Classifier {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Classifier{logger: logger}
}

// ClassifyMessage classifies a message by type and confidence
func (c *Classifier) ClassifyMessage(ctx context.Context, text string) types.Classification {
	c.logger.Debug("classifying message", zap.String("text", text))

	// Use keyword-based classification (mock for now)
	return classifyByKeywords(text)
}

// classifyByKeywords does simple keyword-based classification
func classifyByKeywords(text string) types.Classification {
	textLower := strings.ToLower(text)

	taskKeywords := []string{"todo", "task", "do this", "buy", "order", "call", "send", "finish", "complete", "schedule"}
	ideaKeywords := []string{"idea", "think", "maybe", "what if", "consider", "hunch", "insight", "concept"}
	journalKeywords := []string{"felt", "today", "yesterday", "experience", "realized", "learned", "was thinking"}

	taskScore := countMatches(textLower, taskKeywords)
	ideaScore := countMatches(textLower, ideaKeywords)
	journalScore := countMatches(textLower, journalKeywords)

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

	// Check for mixed content
	scores := map[string]int{"task": taskScore, "idea": ideaScore, "journal": journalScore}
	nonZero := 0
	for _, s := range scores {
		if s > 0 {
			nonZero++
		}
	}
	if nonZero >= 2 {
		category = "mixed"
		confidence = 0.6
	}

	return types.Classification{
		Category:   category,
		Confidence: confidence,
	}
}

func countMatches(text string, keywords []string) int {
	count := 0
	for _, kw := range keywords {
		if strings.Contains(text, kw) {
			count++
		}
	}
	return count
}
