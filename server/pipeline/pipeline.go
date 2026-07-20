package pipeline

import (
	"context"
	"os"
	"path/filepath"

	"github.com/Magazem/WhispAir/server/llm"
	"github.com/Magazem/WhispAir/types"
	"go.uber.org/zap"
)

type Config struct {
	DataDir     string
	Logger      *zap.Logger
	OllamaHost  string
	WhisperPath string
	LLMClient   llm.Client
}

type Pipeline struct {
	config      Config
	logger      *zap.Logger
	dataDir     string
	transcriber *Transcriber
	classifier  *Classifier
	extractor   *Extractor
	critic      *Critic
	router      *Router
	prompts     *PromptLoader
	llm         llm.Client
}

func New(cfg Config) *Pipeline {
	logger := cfg.Logger
	if logger == nil {
		logger = zap.NewNop()
	}

	// Initialize prompt loader
	promptsPath := filepath.Join("server", "pipeline", "prompts.yaml")
	if _, err := os.Stat(promptsPath); err != nil {
		// Fallback to current directory
		promptsPath = "prompts.yaml"
	}
	prompts := NewPromptLoader(promptsPath, logger)

	return &Pipeline{
		config:      cfg,
		logger:      logger,
		dataDir:     cfg.DataDir,
		transcriber: NewTranscriber(TranscriberConfig{WhisperPath: cfg.WhisperPath, Logger: logger}),
		classifier:  NewClassifier(logger, cfg.LLMClient, prompts),
		extractor:   NewExtractor(logger, cfg.LLMClient, prompts),
		critic:      NewCritic(logger, cfg.LLMClient),
		router:     NewRouter(cfg.DataDir, logger),
		prompts:    prompts,
		llm:        cfg.LLMClient,
	}
}

func (p *Pipeline) Process(ctx context.Context, item types.QueueItem) (*types.PipelineResult, error) {
	result := &types.PipelineResult{Items: []types.ExtractedItem{}}

	// Step 1: Get text (transcribe if needed)
	var text string
	var err error

	switch item.Type {
	case "voice", "video":
		text, err = p.transcriber.Transcribe(ctx, item.MediaPath)
		if err != nil {
			p.logger.Error("transcription failed", zap.Error(err))
			text = "[voice message]"
		}
		if saveErr := p.saveTranscript(item.ID, text); saveErr != nil {
			p.logger.Warn("failed to save transcript", zap.Error(saveErr))
		}
	case "text":
		text = item.Text
	default:
		return nil, &pipelineError{msg: "unsupported item type: " + item.Type}
	}

	result.RawText = text

	// Step 2: Classify
	classification := p.classifier.ClassifyMessage(ctx, text)
	result.Classification = classification

	// Step 3: Extract
	items, err := p.extractor.Extract(ctx, text, classification.Category)
	if err != nil {
		return nil, &pipelineError{msg: "extraction failed: " + err.Error()}
	}

	// Step 4: Critic
	refinedItems, err := p.critic.Review(ctx, text, items)
	if err != nil {
		p.logger.Warn("critic failed, using unrefined", zap.Error(err))
		refinedItems = items
	}

	// Step 5: Route
	if err := p.router.Route(ctx, item, refinedItems); err != nil {
		return nil, &pipelineError{msg: "routing failed: " + err.Error()}
	}

	result.Items = refinedItems
	result.Confidence = classification.Confidence
	return result, nil
}

func (p *Pipeline) saveTranscript(id, text string) error {
	dir := filepath.Join(p.dataDir, "media", "transcripts")
	os.MkdirAll(dir, 0755)
	return os.WriteFile(filepath.Join(dir, id+".txt"), []byte(text), 0644)
}

type pipelineError struct{ msg string }

func (e *pipelineError) Error() string { return e.msg }
