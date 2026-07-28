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
	// WhisperModel is the path to the ggml model file.
	WhisperModel string
	// AllowMock permits mock transcription and mock LLM output. Defaults to
	// false so that a broken dependency surfaces as an error instead of
	// fabricated content in the user's notes.
	AllowMock bool
	LLMClient llm.Client
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

	extractor := NewExtractor(logger, cfg.LLMClient, prompts)
	critic := NewCritic(logger, cfg.LLMClient)

	// Keyword fallback is a form of fabrication: it turns an LLM failure into
	// plausible-looking output. Only permit it when mocks are explicitly on.
	extractor.SetAllowFallback(cfg.AllowMock)
	critic.SetAllowFallback(cfg.AllowMock)

	return &Pipeline{
		config:  cfg,
		logger:  logger,
		dataDir: cfg.DataDir,
		transcriber: NewTranscriber(TranscriberConfig{
			WhisperPath: cfg.WhisperPath,
			ModelPath:   cfg.WhisperModel,
			AllowMock:   cfg.AllowMock,
			Logger:      logger,
		}),
		classifier: NewClassifier(logger, cfg.LLMClient, prompts),
		extractor:  extractor,
		critic:     critic,
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
			// Abort rather than continuing with placeholder text. The old code
			// substituted "[voice message]" and carried on, so a failed
			// transcription still produced extracted items and wrote them to
			// the user's notes. The media file is preserved for a retry.
			p.logger.Error("transcription failed; aborting item",
				zap.String("id", item.ID),
				zap.String("media", item.MediaPath),
				zap.Error(err),
			)
			return nil, &pipelineError{msg: "transcription failed: " + err.Error()}
		}
		// Preserve the raw transcript before anything else touches it.
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

	// Stamp the real classification confidence onto each item so the AI
	// markers carry a true number instead of the hardcoded 0.85 they used to.
	for i := range refinedItems {
		if refinedItems[i].Confidence == 0 {
			refinedItems[i].Confidence = classification.Confidence
		}
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
