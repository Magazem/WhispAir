package pipeline

import (
	"context"

	"github.com/Magazem/WhispAir/types"
	"go.uber.org/zap"
)

// Critic does a second pass reviewing extracted items
type Critic struct {
	logger *zap.Logger
}

// NewCritic creates a new critic
func NewCritic(logger *zap.Logger) *Critic {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Critic{logger: logger}
}

// Critic reviews extracted items and may refine them
func (c *Critic) Critic(ctx context.Context, text string, items []types.ExtractedItem) ([]types.ExtractedItem, error) {
	c.logger.Debug("critic pass", zap.String("text", text), zap.Int("items", len(items)))

	// For now, return items as-is
	// A full implementation would call the LLM to critique and refine
	return items, nil
}
