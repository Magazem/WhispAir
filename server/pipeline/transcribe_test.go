package pipeline

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"go.uber.org/zap"
)

// writeFakeWhisper creates an executable that mimics whisper.cpp: it parses
// -of <prefix> and writes "<prefix>.txt" containing body, then exits 0. If
// body is empty the script exits 1 instead, to simulate a failing binary.
func writeFakeWhisper(t *testing.T, dir, body string, shouldFail bool) string {
	t.Helper()

	if runtime.GOOS == "windows" {
		path := filepath.Join(dir, "fakewhisper.cmd")
		var script string
		if shouldFail {
			script = "@echo off\r\nexit /b 1\r\n"
		} else {
			// Walk the args to find -of and capture the following value.
			script = "@echo off\r\n" +
				":loop\r\n" +
				"if \"%~1\"==\"\" goto done\r\n" +
				"if \"%~1\"==\"-of\" set PREFIX=%~2\r\n" +
				"shift\r\n" +
				"goto loop\r\n" +
				":done\r\n" +
				"echo " + body + "> \"%PREFIX%.txt\"\r\n" +
				"exit /b 0\r\n"
		}
		if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
			t.Fatalf("write fake whisper: %v", err)
		}
		return path
	}

	path := filepath.Join(dir, "fakewhisper.sh")
	var script string
	if shouldFail {
		script = "#!/bin/sh\nexit 1\n"
	} else {
		script = "#!/bin/sh\n" +
			"prefix=\"\"\n" +
			"while [ $# -gt 0 ]; do\n" +
			"  if [ \"$1\" = \"-of\" ]; then prefix=\"$2\"; fi\n" +
			"  shift\n" +
			"done\n" +
			"printf '%s\\n' '" + body + "' > \"$prefix.txt\"\n" +
			"exit 0\n"
	}
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake whisper: %v", err)
	}
	return path
}

func writeDummyModel(t *testing.T, dir string) string {
	t.Helper()
	p := filepath.Join(dir, "ggml-test.bin")
	if err := os.WriteFile(p, []byte("not a real model"), 0o644); err != nil {
		t.Fatalf("write model: %v", err)
	}
	return p
}

func writeDummyAudio(t *testing.T, dir string) string {
	t.Helper()
	p := filepath.Join(dir, "voice_msg-abc.wav")
	if err := os.WriteFile(p, []byte("fake audio"), 0o644); err != nil {
		t.Fatalf("write audio: %v", err)
	}
	return p
}

// TestTranscribeSuccessReadsPrefixDerivedFile is the core round-trip: the file
// the transcriber reads back must be the one derived from the prefix it passed
// to -of. The original bug was that these two disagreed.
func TestTranscribeSuccessReadsPrefixDerivedFile(t *testing.T) {
	dir := t.TempDir()
	audio := writeDummyAudio(t, dir)
	model := writeDummyModel(t, dir)
	exe := writeFakeWhisper(t, dir, "hello from whisper", false)

	tr := NewTranscriber(TranscriberConfig{
		WhisperPath: exe,
		ModelPath:   model,
		AllowMock:   false,
		Logger:      zap.NewNop(),
	})

	got, err := tr.Transcribe(context.Background(), audio)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(got, "hello from whisper") {
		t.Errorf("expected transcript from the -of derived file, got %q", got)
	}

	// The file must live at <audio-without-ext>.txt.
	want := strings.TrimSuffix(audio, filepath.Ext(audio)) + ".txt"
	if _, err := os.Stat(want); err != nil {
		t.Errorf("expected transcript at %s: %v", want, err)
	}
}

func TestTranscribeMissingModelReturnsError(t *testing.T) {
	dir := t.TempDir()
	audio := writeDummyAudio(t, dir)
	exe := writeFakeWhisper(t, dir, "should not run", false)

	tr := NewTranscriber(TranscriberConfig{
		WhisperPath: exe,
		ModelPath:   filepath.Join(dir, "does-not-exist.bin"),
		AllowMock:   false,
		Logger:      zap.NewNop(),
	})

	got, err := tr.Transcribe(context.Background(), audio)
	if err == nil {
		t.Fatalf("expected an error for a missing model, got transcript %q", got)
	}
	if got != "" {
		t.Errorf("expected empty transcript on failure, got %q", got)
	}
	if !strings.Contains(err.Error(), "model") {
		t.Errorf("error should mention the model, got: %v", err)
	}
}

func TestTranscribeMissingBinaryReturnsError(t *testing.T) {
	dir := t.TempDir()
	audio := writeDummyAudio(t, dir)

	tr := NewTranscriber(TranscriberConfig{
		WhisperPath: filepath.Join(dir, "no-such-whisper"),
		ModelPath:   writeDummyModel(t, dir),
		AllowMock:   false,
		Logger:      zap.NewNop(),
	})

	if _, err := tr.Transcribe(context.Background(), audio); err == nil {
		t.Fatal("expected an error when the whisper binary is missing")
	}
}

func TestTranscribeExecFailureReturnsError(t *testing.T) {
	dir := t.TempDir()
	audio := writeDummyAudio(t, dir)
	model := writeDummyModel(t, dir)
	exe := writeFakeWhisper(t, dir, "", true) // exits 1

	tr := NewTranscriber(TranscriberConfig{
		WhisperPath: exe,
		ModelPath:   model,
		AllowMock:   false,
		Logger:      zap.NewNop(),
	})

	got, err := tr.Transcribe(context.Background(), audio)
	if err == nil {
		t.Fatalf("expected an error when whisper exits nonzero, got %q", got)
	}
	if got != "" {
		t.Errorf("expected empty transcript on exec failure, got %q", got)
	}
}

// TestNoMockLeaksWhenDisabled is the regression guard for the worst defect in
// the original code: fabricated transcript text reaching the caller (and from
// there the user's real notes) on every failure path.
func TestNoMockLeaksWhenDisabled(t *testing.T) {
	dir := t.TempDir()
	audio := writeDummyAudio(t, dir)

	cases := map[string]TranscriberConfig{
		"missing binary": {WhisperPath: filepath.Join(dir, "nope"), ModelPath: writeDummyModel(t, dir)},
		"missing model":  {WhisperPath: writeFakeWhisper(t, dir, "x", false), ModelPath: filepath.Join(dir, "nope.bin")},
		"exec failure":   {WhisperPath: writeFakeWhisper(t, dir, "", true), ModelPath: writeDummyModel(t, dir)},
	}

	for name, cfg := range cases {
		t.Run(name, func(t *testing.T) {
			cfg.AllowMock = false
			cfg.Logger = zap.NewNop()
			tr := NewTranscriber(cfg)

			got, err := tr.Transcribe(context.Background(), audio)
			if err == nil {
				t.Fatalf("expected an error, got %q", got)
			}
			if got != "" {
				t.Fatalf("mock text leaked with AllowMock=false: %q", got)
			}
			if strings.Contains(got, "glass-ai") {
				t.Fatalf("fabricated mock transcript leaked: %q", got)
			}
		})
	}
}

func TestAllowMockReturnsMockText(t *testing.T) {
	dir := t.TempDir()
	audio := writeDummyAudio(t, dir)

	tr := NewTranscriber(TranscriberConfig{
		WhisperPath: filepath.Join(dir, "no-such-whisper"),
		AllowMock:   true,
		Logger:      zap.NewNop(),
	})

	got, err := tr.Transcribe(context.Background(), audio)
	if err != nil {
		t.Fatalf("AllowMock should suppress the error, got: %v", err)
	}
	if got == "" {
		t.Fatal("expected mock text when AllowMock is true")
	}
}

// TestTranscribePassesLanguageAuto verifies the multilingual requirement: the
// language flag must be present, otherwise whisper.cpp defaults to English and
// mangles Arabic notes. The fake binary records the args it received.
func TestTranscribePassesLanguageAuto(t *testing.T) {
	dir := t.TempDir()
	audio := writeDummyAudio(t, dir)
	model := writeDummyModel(t, dir)

	argsFile := filepath.Join(dir, "args.txt")
	var exe string
	if runtime.GOOS == "windows" {
		exe = filepath.Join(dir, "recordargs.cmd")
		script := "@echo off\r\n" +
			"echo %* > \"" + argsFile + "\"\r\n" +
			":loop\r\n" +
			"if \"%~1\"==\"\" goto done\r\n" +
			"if \"%~1\"==\"-of\" set PREFIX=%~2\r\n" +
			"shift\r\n" +
			"goto loop\r\n" +
			":done\r\n" +
			"echo recorded> \"%PREFIX%.txt\"\r\n"
		if err := os.WriteFile(exe, []byte(script), 0o755); err != nil {
			t.Fatalf("write recorder: %v", err)
		}
	} else {
		exe = filepath.Join(dir, "recordargs.sh")
		script := "#!/bin/sh\n" +
			"echo \"$@\" > \"" + argsFile + "\"\n" +
			"prefix=\"\"\n" +
			"while [ $# -gt 0 ]; do\n" +
			"  if [ \"$1\" = \"-of\" ]; then prefix=\"$2\"; fi\n" +
			"  shift\n" +
			"done\n" +
			"printf 'recorded\\n' > \"$prefix.txt\"\n"
		if err := os.WriteFile(exe, []byte(script), 0o755); err != nil {
			t.Fatalf("write recorder: %v", err)
		}
	}

	tr := NewTranscriber(TranscriberConfig{
		WhisperPath: exe,
		ModelPath:   model,
		Logger:      zap.NewNop(),
	})

	if _, err := tr.Transcribe(context.Background(), audio); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	raw, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("read recorded args: %v", err)
	}
	args := string(raw)
	for _, want := range []string{"-l", "auto", "-otxt", "-of"} {
		if !strings.Contains(args, want) {
			t.Errorf("expected %q in whisper args, got: %s", want, args)
		}
	}
}

func TestResolveModelPrefersEnvVar(t *testing.T) {
	dir := t.TempDir()
	model := writeDummyModel(t, dir)
	t.Setenv("WHISPER_MODEL", model)

	tr := NewTranscriber(TranscriberConfig{Logger: zap.NewNop()})
	got, err := tr.resolveModel(filepath.Join(dir, "whisper"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != model {
		t.Errorf("expected %s, got %s", model, got)
	}
}

func TestResolveModelEnvVarMissingIsAnError(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("WHISPER_MODEL", filepath.Join(dir, "absent.bin"))

	tr := NewTranscriber(TranscriberConfig{Logger: zap.NewNop()})
	if _, err := tr.resolveModel(filepath.Join(dir, "whisper")); err == nil {
		t.Fatal("expected an error when WHISPER_MODEL points at a missing file")
	}
}
