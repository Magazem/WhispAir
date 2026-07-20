package pipeline

import (
	"context"
	"os"
	"path/filepath"

	"github.com/Magazem/WhispAir/server/llm"
	"github.com/Magazem/WhispAir/types"
	"go.uber.org/zap"
)

// Config for the pipeline
type Config struct {
	DataDir     string
	Logger      *zap.Logger
	OllamaHost  string
	WhisperPath string
	LLMClient   llm.Client
}

// Pipeline orchestrates the full processing pipeline
type Pipeline struct {
	config      Config
	logger      *zap.Logger
	dataDir     string
	transcriber *Transcriber
	classifier  *Classifier
	extractor   *Extractor
	critic      *Critic
	router      *Router
}

// New creates a new Pipeline
func New(cfg Config) *Pipeline {
	logger := cfg.Logger
	if logger == nil {
		logger = zap.NewNop()
	}

	dataDir := cfg.DataDir

	return &Pipeline{
		config:  cfg,
		logger:  logger,
		dataDir: dataDir,
		transcriber: NewTranscriber(TranscriberConfig{
			WhisperPath: cfg.WhisperPath,
			Logger:      logger,
		}),
		classifier: NewClassifier(logger),
		extractor:  NewExtractor(logger),
		critic:     NewCritic(logger),
		router:     NewRouter(dataDir, logger),
	}
}

// Process runs the full pipeline on an item
func (p *Pipeline) Process(ctx context.Context, item types.QueueItem) (*types.PipelineResult, error) {
	p.logger.Info("pipeline start",
		zap.String("id", item.ID),
		zap.String("type", item.Type),
	)

	result := &types.PipelineResult{
		Items: []types.ExtractedItem{},
	}

	// Step 1: Get text (transcribe if needed)
	var text string
	var err error

	switch item.Type {
	case "voice", "video":
		// Transcribe audio
		text, err = p.transcriber.Transcribe(ctx, item.MediaPath)
		if err != nil {
			// Log but don't fail - use fallback text
			p.logger.Error("transcription failed, using fallback",
				zap.Error(err),
				zap.String("id", item.ID),
			)
			text = "[voice message]"
		}

		// Save transcript for posterity
		if saveErr := p.saveTranscript(item.ID, text); saveErr != nil {
			p.logger.Warn("failed to save transcript", zap.Error(saveErr))
		}

	case "text":
		text = item.Text

	default:
		return nil, unsupportedTypeError(item.Type)
	}

	result.RawText = text

	// Step 2: Classify
	classification := p.classifier.ClassifyMessage(ctx, text)
	result.Classification = classification

	// Step 3: Extract
	items, err := p.extractor.Extract(ctx, text, classification.Category)
	if err != nil {
		return nil, extractionError(err)
	}

	// Step 4: Cognitive critic pass (refine)
	refinedItems, err := p.critic.Critic(ctx, text, items)
	if err != nil {
		p.logger.Warn("critic pass failed, using unrefined", zap.Error(err))
		refinedItems = items
	}

	// Step 5: Route items to target files and Review.md
	if err := p.router.Route(ctx, item, refinedItems); err != nil {
		return nil, routingError(err)
	}

	result.Items = refinedItems
	result.Confidence = classification.Confidence

	p.logger.Info("pipeline complete",
		zap.String("id", item.ID),
		zap.Int("items", len(result.Items)),
		zap.Float64("confidence", result.Confidence),
	)

	return result, nil
}

// saveTranscript preserves the raw transcript forever
func (p *Pipeline) saveTranscript(id, text string) error {
	dir := filepath.Join(p.dataDir, "media", "transcripts")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	filename := id + ".txt"
	return os.WriteFile(filepath.Join(dir, filename), []byte(text), 0644)
}

func unsupportedTypeError(t string) error {
	return &pipelineError{msg: "unsupported item type: " + t}
}

func extractionError(err error) error {
	return &pipelineError{msg: "extraction failed: " + err.Error()}
}

func routingError(err error) error {
	return &pipelineError{msg: "routing failed: " + err.Error()}
}

type pipelineError struct {
	msg string
}

func (e *pipelineError) Error() string {
	return e.msg
}
