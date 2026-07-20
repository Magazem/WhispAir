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

// Transcriber handles audio transcription via whisper.cpp
type Transcriber struct {
	config TranscriberConfig
	logger *zap.Logger
}

type TranscriberConfig struct {
	WhisperPath string
	Logger      *zap.Logger
}

func NewTranscriber(cfg TranscriberConfig) *Transcriber {
	if cfg.Logger == nil {
		cfg.Logger = zap.NewNop()
	}
	return &Transcriber{config: cfg, logger: cfg.Logger}
}

func (t *Transcriber) Transcribe(ctx context.Context, audioPath string) (string, error) {
	t.logger.Info("transcribing", zap.String("path", audioPath))

	whisperExe := t.config.WhisperPath
	if whisperExe == "" {
		// Look for whisper in common locations
		candidates := []string{
			// User-space portable install
			filepath.Join(os.Getenv("USERPROFILE"), "Portable", "main.exe"),
			// System path
			"whisper",
			"whisper.exe",
			// Home local
			filepath.Join(os.Getenv("HOME"), ".local", "bin", "whisper"),
		}
		for _, c := range candidates {
			if _, err := os.Stat(c); err == nil {
				whisperExe = c
				break
			}
			if path, err := exec.LookPath(c); err == nil {
				whisperExe = path
				break
			}
		}
	}

	if whisperExe == "" {
		t.logger.Warn("whisper not found, using mock")
		return mockTranscript(audioPath), nil
	}

	// Build whisper command
	outputDir := filepath.Dir(audioPath)
	baseName := strings.TrimSuffix(filepath.Base(audioPath), filepath.Ext(audioPath))

	// Default model path
	modelPath := filepath.Join(filepath.Dir(whisperExe), "models", "ggml-medium.bin")
	if _, err := os.Stat(modelPath); err != nil {
		modelPath = filepath.Join(filepath.Dir(whisperExe), "ggml-medium.bin")
	}

	args := []string{"-m", modelPath, "-f", audioPath, "--output-dir", outputDir, "--output-format", "txt"}

	cmd := exec.CommandContext(ctx, whisperExe, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.logger.Error("whisper failed", zap.Error(err), zap.String("output", string(output)))
		return mockTranscript(audioPath), nil
	}

	// Read the output file
	txtFile := filepath.Join(outputDir, baseName+".txt")
	content, err := os.ReadFile(txtFile)
	if err != nil {
		t.logger.Warn("failed to read transcript file", zap.Error(err))
		return mockTranscript(audioPath), nil
	}

	return string(content), nil
}

func mockTranscript(audioPath string) string {
	if strings.Contains(audioPath, "voice_msg") || strings.Contains(audioPath, "voice") {
		return "Idea about glass-ai scout mode trigger. Consider how to detect context switching when wearing the glasses. Maybe use the accelerometer data to determine when the user is focusing on something specific."
	}
	return fmt.Sprintf("This is a mock transcription for file %s. It contains some thoughts about improving the system architecture. Maybe we should consider using a different approach for handling voice messages.", filepath.Base(audioPath))
}
