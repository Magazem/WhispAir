package plugins

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Magazem/WhispAir/types"
	"go.uber.org/zap"
)

type Plugin interface {
	Name() string
	CanHandle(msgType string) bool
	PreProcess(item *types.QueueItem) error
	PostProcess(item *types.QueueItem, result *types.PipelineResult) error
}

var registry []List

type List struct {
	name    string
	handler func(*Plugin) Plugin
}

func Register(p Plugin) {
	registry = append(registry, List{name: p.Name(), handler: func(pp *Plugin) Plugin { return *pp }})
}

func init() {
	Register(&VoicePlugin{})
	Register(&VideoPlugin{})
	Register(&ExtractorPlugin{})
}

// VoicePlugin handles voice messages
type VoicePlugin struct{ logger *zap.Logger }

func (p *VoicePlugin) Name() string               { return "voice" }
func (p *VoicePlugin) CanHandle(s string) bool     { return s == "voice" }
func (p *VoicePlugin) PreProcess(item *types.QueueItem) error {
	if item.MediaPath == "" {
		return fmt.Errorf("no media path for voice item")
	}
	mediaDir := filepath.Dir(item.MediaPath)
	if err := os.MkdirAll(mediaDir, 0755); err != nil {
		return fmt.Errorf("failed to create media dir: %w", err)
	}
	p.logger.Info("voice pre-process", zap.String("id", item.ID), zap.String("path", item.MediaPath))
	return nil
}
func (p *VoicePlugin) PostProcess(item *types.QueueItem, result *types.PipelineResult) error {
	p.logger.Info("voice post-process", zap.String("id", item.ID), zap.Int("items", len(result.Items)))
	return nil
}

// VideoPlugin handles video messages
type VideoPlugin struct{ logger *zap.Logger }

func (p *VideoPlugin) Name() string               { return "video" }
func (p *VideoPlugin) CanHandle(s string) bool     { return s == "video" }
func (p *VideoPlugin) PreProcess(item *types.QueueItem) error {
	if item.MediaPath == "" {
		return fmt.Errorf("no media path for video item")
	}
	mediaDir := filepath.Dir(item.MediaPath)
	if err := os.MkdirAll(mediaDir, 0755); err != nil {
		return fmt.Errorf("failed to create media dir: %w", err)
	}

	// Strip audio via ffmpeg
	p.logger.Info("extracting audio", zap.String("id", item.ID), zap.String("video", item.MediaPath))

	audioPath := strings.TrimSuffix(item.MediaPath, filepath.Ext(item.MediaPath)) + ".wav"

	cmd := exec.Command("ffmpeg", "-i", item.MediaPath, "-vn", "-acodec", "pcm_s16le", "-ar", "16000", "-ac", "1", "-y", audioPath)
	output, err := cmd.CombinedOutput()

	if err != nil {
		p.logger.Warn("ffmpeg extraction failed, using mock audio file",
			zap.Error(err),
			zap.String("output", string(output)),
		)
		return nil // Don't fail; transcriptor will use mock
	}

	item.MediaPath = audioPath
	return nil
}
func (p *VideoPlugin) PostProcess(item *types.QueueItem, result *types.PipelineResult) error {
	p.logger.Info("video post-process", zap.String("id", item.ID), zap.Int("items", len(result.Items)))
	return nil
}

// ExtractorPlugin
type ExtractorPlugin struct{ logger *zap.Logger }

func (p *ExtractorPlugin) Name() string               { return "extractor" }
func (p *ExtractorPlugin) CanHandle(s string) bool     { return true }
func (p *ExtractorPlugin) PreProcess(item *types.QueueItem) error { return nil }
func (p *ExtractorPlugin) PostProcess(item *types.QueueItem, result *types.PipelineResult) error {
	p.logger.Info("extraction complete", zap.String("id", item.ID), zap.Int("items", len(result.Items)), zap.Float64("confidence", result.Confidence))
	return nil
}

func RegisterPlugins(s interface {
	RegisterPlugin(Plugin)
	Logger() *zap.Logger
}) {
	logger := s.Logger()
	s.RegisterPlugin(&VoicePlugin{logger: logger})
	s.RegisterPlugin(&VideoPlugin{logger: logger})
	s.RegisterPlugin(&ExtractorPlugin{logger: logger})
}

var _ = time.Now
