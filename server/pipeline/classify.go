package pipeline

import (
	"context"
	"strings"

	"github.com/Magazem/WhispAir/server/llm"
	"github.com/Magazem/WhispAir/types"
	"go.uber.org/zap"
)

type Classifier struct {
	logger *zap.Logger
	llm    llm.Client
	prompts *PromptLoader
}

func NewClassifier(logger *zap.Logger, l llm.Client, p *PromptLoader) *Classifier {
	return &Classifier{logger: logger, llm: l, prompts: p}
}

func (c *Classifier) ClassifyMessage(ctx context.Context, text string) types.Classification {
	if c.llm != nil {
		cl, err := c.llm.Classify(ctx, text)
		if err == nil {
			return cl
		}
		c.logger.Warn("LLM classify failed, using keyword fallback", zap.Error(err))
	}
	return classifyByKeywords(text)
}

func classifyByKeywords(text string) types.Classification {
	tl := strings.ToLower(text)
	tasks := []string{"todo", "task", "do this", "buy", "order", "call", "send", "finish", "complete", "schedule"}
	ideas := []string{"idea", "think", "maybe", "what if", "consider", "hunch", "insight", "concept"}
	journals := []string{"felt", "today", "yesterday", "experience", "realized", "learned", "was thinking"}

	ts := countMatches(tl, tasks)
	is := countMatches(tl, ideas)
	js := countMatches(tl, journals)

	maxScore := ts
	cat := "task"
	conf := 0.6
	if is > maxScore { maxScore = is; cat = "idea" }
	if js > maxScore { maxScore = js; cat = "journal" }

	if maxScore > 0 {
		conf = 0.7 + float64(maxScore)*0.1
		if conf > 0.95 { conf = 0.95 }
	}

	// Mixed check
	nz := 0
	if ts > 0 { nz++ }
	if is > 0 { nz++ }
	if js > 0 { nz++ }
	if nz >= 2 { cat = "mixed"; conf = 0.6 }

	return types.Classification{Category: cat, Confidence: conf}
}

func countMatches(text string, kws []string) int {
	n := 0
	for _, k := range kws {
		if strings.Contains(text, k) { n++ }
	}
	return n
}
