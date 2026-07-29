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
	// ModelPath points at a ggml model file. When empty the transcriber falls
	// back to $WHISPER_MODEL and then to a small set of conventional locations.
	ModelPath string
	// AllowMock permits returning fabricated transcript text when real
	// transcription is impossible. It MUST default to false: a mock transcript
	// is indistinguishable from a real thought once it reaches the notes.
	AllowMock bool
	Logger    *zap.Logger
}

func NewTranscriber(cfg TranscriberConfig) *Transcriber {
	if cfg.Logger == nil {
		cfg.Logger = zap.NewNop()
	}
	return &Transcriber{config: cfg, logger: cfg.Logger}
}

// Transcribe converts the audio at audioPath to text using whisper.cpp.
//
// Every failure returns a non-nil error. Mock text is only ever returned when
// AllowMock is set, and always with a loud warning, so that a broken pipeline
// can never quietly invent content and write it into the user's notes.
func (t *Transcriber) Transcribe(ctx context.Context, audioPath string) (string, error) {
	t.logger.Info("transcribing", zap.String("path", audioPath))

	whisperExe, err := t.resolveWhisper()
	if err != nil {
		return t.mockOrFail(audioPath, err)
	}

	modelPath, err := t.resolveModel(whisperExe)
	if err != nil {
		return t.mockOrFail(audioPath, err)
	}

	// Derive the output prefix once and reuse it for both the -of flag and the
	// read-back path, so the two can never disagree. whisper.cpp writes
	// "<prefix>.txt" when -otxt is set.
	outputPrefix := strings.TrimSuffix(audioPath, filepath.Ext(audioPath))
	txtFile := outputPrefix + ".txt"

	args := []string{
		"-m", modelPath,
		"-f", audioPath,
		// Auto-detect the language. whisper.cpp defaults to English, which
		// would mangle this project's Arabic notes.
		"-l", "auto",
		"-otxt",
		"-of", outputPrefix,
	}

	cmd := exec.CommandContext(ctx, whisperExe, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.logger.Error("whisper failed",
			zap.Error(err),
			zap.String("exe", whisperExe),
			zap.String("model", modelPath),
			zap.String("output", string(output)),
		)
		return t.mockOrFail(audioPath, fmt.Errorf("whisper exec failed: %w", err))
	}

	content, err := os.ReadFile(txtFile)
	if err != nil {
		return t.mockOrFail(audioPath, fmt.Errorf("failed to read transcript %s: %w", txtFile, err))
	}

	text := strings.TrimSpace(string(content))
	if text == "" {
		return t.mockOrFail(audioPath, fmt.Errorf("whisper produced an empty transcript at %s", txtFile))
	}

	return text, nil
}

// resolveWhisper locates the whisper.cpp CLI binary.
func (t *Transcriber) resolveWhisper() (string, error) {
	if t.config.WhisperPath != "" {
		if _, err := os.Stat(t.config.WhisperPath); err == nil {
			return t.config.WhisperPath, nil
		}
		if p, err := exec.LookPath(t.config.WhisperPath); err == nil {
			return p, nil
		}
		return "", fmt.Errorf("configured whisper path %q not found", t.config.WhisperPath)
	}

	for _, c := range []string{"whisper-cli", "whisper", "main"} {
		if p, err := exec.LookPath(c); err == nil {
			return p, nil
		}
	}

	for _, c := range []string{
		filepath.Join(homeDir(), ".local", "bin", "whisper"),
		filepath.Join(homeDir(), "whisper.cpp", "build", "bin", "whisper-cli"),
	} {
		if _, err := os.Stat(c); err == nil {
			return c, nil
		}
	}

	return "", fmt.Errorf("whisper binary not found: set WHISPER_PATH or put whisper-cli on PATH")
}

// resolveModel locates the ggml model file.
//
// The previous implementation looked only in filepath.Dir(whisperExe)/models,
// which resolves to ~/.local/bin/models when WHISPER_PATH is the symlink that
// SETUP.md creates - while the model is actually downloaded to
// ~/whisper.cpp/models. That mismatch made every real transcription fail.
func (t *Transcriber) resolveModel(whisperExe string) (string, error) {
	if t.config.ModelPath != "" {
		if _, err := os.Stat(t.config.ModelPath); err == nil {
			return t.config.ModelPath, nil
		}
		return "", fmt.Errorf("configured model path %q not found", t.config.ModelPath)
	}

	if env := os.Getenv("WHISPER_MODEL"); env != "" {
		if _, err := os.Stat(env); err == nil {
			return env, nil
		}
		return "", fmt.Errorf("WHISPER_MODEL=%q not found", env)
	}

	exeDir := filepath.Dir(whisperExe)
	candidates := []string{
		filepath.Join(homeDir(), "whisper.cpp", "models", "ggml-medium.bin"),
		// build/bin/whisper-cli -> ../../models
		filepath.Join(exeDir, "..", "..", "models", "ggml-medium.bin"),
		filepath.Join(exeDir, "models", "ggml-medium.bin"),
		filepath.Join(exeDir, "ggml-medium.bin"),
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return filepath.Clean(c), nil
		}
	}

	return "", fmt.Errorf("no whisper model found; set whisper.model or WHISPER_MODEL (looked in %v)", candidates)
}

// mockOrFail returns mock text only when explicitly permitted, and otherwise
// propagates the error. This is the single chokepoint that keeps fabricated
// transcripts out of the user's notes.
func (t *Transcriber) mockOrFail(audioPath string, cause error) (string, error) {
	if !t.config.AllowMock {
		return "", cause
	}
	t.logger.Warn("MOCK TRANSCRIPT - not real audio; output is fabricated",
		zap.String("path", audioPath),
		zap.Error(cause),
	)
	return mockTranscript(audioPath), nil
}

func homeDir() string {
	if h, err := os.UserHomeDir(); err == nil && h != "" {
		return h
	}
	if h := os.Getenv("HOME"); h != "" {
		return h
	}
	return os.Getenv("USERPROFILE")
}

func mockTranscript(audioPath string) string {
	if strings.Contains(audioPath, "voice_msg") || strings.Contains(audioPath, "voice") {
		return "Idea about glass-ai scout mode trigger. Consider how to detect context switching when wearing the glasses. Maybe use the accelerometer data to determine when the user is focusing on something specific."
	}
	return fmt.Sprintf("This is a mock transcription for file %s. It contains some thoughts about improving the system architecture. Maybe we should consider using a different approach for handling voice messages.", filepath.Base(audioPath))
}
