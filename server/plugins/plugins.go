package plugins

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Magazem/WhispAir/types"
	"go.uber.org/zap"
)

// Plugin interface for extending the server
type Plugin interface {
	Name() string
	CanHandle(msgType string) bool
	PreProcess(item *types.QueueItem) error
	PostProcess(item *types.QueueItem, result *types.PipelineResult) error
}

// registry holds all registered plugins
var registry []Plugin

// Register adds a plugin to the global registry
func Register(p Plugin) {
	registry = append(registry, p)
}

// GetRegistry returns all registered plugins
func GetRegistry() []Plugin {
	return registry
}

func init() {
	Register(&VoicePlugin{})
	Register(&VideoPlugin{})
	Register(&ExtractorPlugin{})
}

// VoicePlugin handles voice messages
type VoicePlugin struct {
	logger *zap.Logger
}

// Name returns the plugin name
func (p *VoicePlugin) Name() string {
	return "voice"
}

// CanHandle returns true for voice messages
func (p *VoicePlugin) CanHandle(msgType string) bool {
	return msgType == "voice"
}

// PreProcess saves raw audio and creates queue entry
func (p *VoicePlugin) PreProcess(item *types.QueueItem) error {
	if item.MediaPath == "" {
		return fmt.Errorf("no media path for voice item")
	}

	// Ensure media directory exists
	mediaDir := filepath.Dir(item.MediaPath)
	if err := os.MkdirAll(mediaDir, 0755); err != nil {
		return fmt.Errorf("failed to create media dir: %w", err)
	}

	p.logger.Info("voice pre-process",
		zap.String("id", item.ID),
		zap.String("path", item.MediaPath),
	)

	return nil
}

// PostProcess handles post-processing
func (p *VoicePlugin) PostProcess(item *types.QueueItem, result *types.PipelineResult) error {
	p.logger.Info("voice post-process",
		zap.String("id", item.ID),
		zap.Int("items", len(result.Items)),
	)
	return nil
}

// VideoPlugin handles video messages
type VideoPlugin struct {
	logger *zap.Logger
}

// Name returns the plugin name
func (p *VideoPlugin) Name() string {
	return "video"
}

// CanHandle returns true for video messages
func (p *VideoPlugin) CanHandle(msgType string) bool {
	return msgType == "video"
}

// PreProcess saves video and extracts audio
func (p *VideoPlugin) PreProcess(item *types.QueueItem) error {
	if item.MediaPath == "" {
		return fmt.Errorf("no media path for video item")
	}

	mediaDir := filepath.Dir(item.MediaPath)
	if err := os.MkdirAll(mediaDir, 0755); err != nil {
		return fmt.Errorf("failed to create media dir: %w", err)
	}

	p.logger.Info("video pre-process",
		zap.String("id", item.ID),
		zap.String("path", item.MediaPath),
	)

	return nil
}

// PostProcess handles post-processing
func (p *VideoPlugin) PostProcess(item *types.QueueItem, result *types.PipelineResult) error {
	p.logger.Info("video post-process",
		zap.String("id", item.ID),
		zap.Int("items", len(result.Items)),
	)
	return nil
}

// ExtractorPlugin orchestrates the LLM pipeline
type ExtractorPlugin struct {
	logger *zap.Logger
}

// Name returns the plugin name
func (p *ExtractorPlugin) Name() string {
	return "extractor"
}

// CanHandle returns true for all types
func (p *ExtractorPlugin) CanHandle(msgType string) bool {
	return true
}

// PreProcess is a no-op
func (p *ExtractorPlugin) PreProcess(item *types.QueueItem) error {
	return nil
}

// PostProcess logs extraction results
func (p *ExtractorPlugin) PostProcess(item *types.QueueItem, result *types.PipelineResult) error {
	p.logger.Info("extraction complete",
		zap.String("id", item.ID),
		zap.Int("items", len(result.Items)),
		zap.Float64("confidence", result.Confidence),
	)
	return nil
}

// Register registers all memoire plugins
func RegisterPlugins(srv interface {
	RegisterPlugin(Plugin)
	Logger() *zap.Logger
}) {
	logger := srv.Logger()
	srv.RegisterPlugin(&VoicePlugin{logger: logger})
	srv.RegisterPlugin(&VideoPlugin{logger: logger})
	srv.RegisterPlugin(&ExtractorPlugin{logger: logger})
}

// Ensure plugins implement the Plugin interface
var _ Plugin = (*VoicePlugin)(nil)
var _ Plugin = (*VideoPlugin)(nil)
var _ Plugin = (*ExtractorPlugin)(nil)

// Ensure time is used
var _ = time.Now
