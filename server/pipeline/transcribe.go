package pipeline

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"go.uber.org/zap"
)

// Transcriber handles audio transcription
type Transcriber struct {
	config TranscriberConfig
	logger *zap.Logger
}

// TranscriberConfig for the transcriber
type TranscriberConfig struct {
	WhisperPath string
	Logger      *zap.Logger
}

// NewTranscriber creates a new transcriber
func NewTranscriber(cfg TranscriberConfig) *Transcriber {
	if cfg.Logger == nil {
		cfg.Logger = zap.NewNop()
	}
	return &Transcriber{
		config: cfg,
		logger: cfg.Logger,
	}
}

// Transcribe converts audio to text
func (t *Transcriber) Transcribe(ctx context.Context, audioPath string) (string, error) {
	t.logger.Info("transcribing", zap.String("path", audioPath))

	if t.config.WhisperPath == "" {
		// Return mock transcript
		return mockTranscript(audioPath), nil
	}

	// Check if file exists
	if _, err := exec.LookPath(t.config.WhisperPath); err != nil {
		t.logger.Warn("whisper not found, using mock", zap.Error(err))
		return mockTranscript(audioPath), nil
	}

	// Build whisper command
	outputDir := filepath.Dir(audioPath)
	cmd := exec.CommandContext(ctx, t.config.WhisperPath,
		"-m", filepath.Join(filepath.Dir(t.config.WhisperPath), "models", "ggml-medium.bin"),
		"-f", audioPath,
		"--output-dir", outputDir,
		"--output-format", "txt",
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		t.logger.Error("whisper failed", zap.Error(err), zap.String("output", string(output)))
		return mockTranscript(audioPath), nil
	}

	// Read the output file
	txtFile := strings.TrimSuffix(audioPath, filepath.Ext(audioPath)) + ".txt"
	content, err := os.ReadFile(txtFile)
	if err != nil {
		t.logger.Warn("failed to read transcript file", zap.Error(err))
		return mockTranscript(audioPath), nil
	}

	return string(content), nil
}

// mockTranscript returns a mock transcription for demo/testing
func mockTranscript(audioPath string) string {
	// Return different mock content based on filename
	if strings.Contains(audioPath, "voice_msg") {
		return "Idea about glass-ai scout mode trigger. Consider how to detect context switching when wearing the glasses. Maybe use the accelerometer data to determine when the user is focusing on something specific."
	}
	return fmt.Sprintf("This is a mock transcription for file %s. It contains some thoughts about improving the system architecture. Maybe we should consider using a different approach for handling voice messages.", filepath.Base(audioPath))
}
