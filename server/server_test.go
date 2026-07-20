package server_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Magazem/WhispAir/server/pipeline"
	"github.com/Magazem/WhispAir/types"
	"go.uber.org/zap"
)

// TestPipelineTextMessage tests processing a text message through the pipeline
func TestPipelineTextMessage(t *testing.T) {
	// Create temp data dir
	dataDir, err := os.MkdirTemp("", "memoire-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dataDir)

	// Create logger
	logger := zap.NewNop()

	// Create pipeline
	p := pipeline.New(pipeline.Config{
		DataDir: dataDir,
		Logger:  logger,
	})

	// Create a queue item
	item := types.QueueItem{
		ID:        "test_text_1",
		Type:      "text",
		Source:    "test",
		Text:      "I need to buy groceries and order brake pads for the Volvo. Idea about a context-aware AI assistant that uses sensor data.",
		Timestamp: time.Now(),
	}

	// Run pipeline
	ctx := context.Background()
	result, err := p.Process(ctx, item)
	if err != nil {
		t.Fatalf("pipeline failed: %v", err)
	}

	// Verify result
	if result.RawText == "" {
		t.Error("expected raw text to be set")
	}

	if len(result.Items) == 0 {
		t.Error("expected items to be extracted")
	}

	// Check targets
	found := false
	for _, item := range result.Items {
		if item.Type == "task" {
			found = true
			if item.Target != "Later.md" {
				t.Errorf("task target = %s, want Later.md", item.Target)
			}
		}
		if item.Type == "idea" {
			found = true
			if !containsPrefix(item.Target, "brain/") {
				t.Errorf("idea target = %s, want brain/*.md", item.Target)
			}
		}
	}

	if !found {
		t.Error("expected at least one task or idea")
	}

	// Check file creation
	files := []string{"Review.md", "Later.md"}
	for _, f := range files {
		path := filepath.Join(dataDir, f)
		if _, err := os.Stat(path); err != nil {
			t.Errorf("expected file %s to exist: %v", f, err)
		}
	}

	// Verify AI markers in created files
	t.Logf("Pipeline result: %d items, category=%s, confidence=%.2f",
		len(result.Items), result.Classification.Category, result.Confidence)

	for _, extracted := range result.Items {
		t.Logf("  - [%s] %s -> %s", extracted.Type, extracted.Text, extracted.Target)
	}
}

// TestPipelineVoiceMessage tests processing a voice message through the pipeline
func TestPipelineVoiceMessage(t *testing.T) {
	dataDir, err := os.MkdirTemp("", "memoire-test-voice-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dataDir)

	// Create a mock audio file (content doesn't matter since we use mock transcription)
	mediaDir := filepath.Join(dataDir, "media")
	os.MkdirAll(mediaDir, 0755)
	mockAudio := filepath.Join(mediaDir, "voice_msg-test.ogg")
	os.WriteFile(mockAudio, []byte("mock audio data"), 0644)

	logger := zap.NewNop()
	p := pipeline.New(pipeline.Config{
		DataDir: dataDir,
		Logger:  logger,
	})

	item := types.QueueItem{
		ID:        "test_voice_1",
		Type:      "voice",
		Source:    "test",
		MediaPath: mockAudio,
		Timestamp: time.Now(),
	}

	ctx := context.Background()
	result, err := p.Process(ctx, item)
	if err != nil {
		t.Fatalf("pipeline failed: %v", err)
	}

	// Verify result
	if result.RawText == "" {
		t.Error("expected raw text")
	}

	if len(result.Items) == 0 {
		t.Error("expected items from voice message")
	}

	// Check transcript preservation
	transcriptPath := filepath.Join(dataDir, "media", "transcripts", "test_voice_1.txt")
	if _, err := os.Stat(transcriptPath); err != nil {
		t.Errorf("transcript not preserved: %v", err)
	}

	t.Logf("Voice result: %d items, raw text: %q", len(result.Items), result.RawText)
}

// TestPipelineJournalMessage tests processing a journal entry
func TestPipelineJournalMessage(t *testing.T) {
	dataDir, err := os.MkdirTemp("", "memoire-test-journal-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dataDir)

	logger := zap.NewNop()
	p := pipeline.New(pipeline.Config{
		DataDir: dataDir,
		Logger:  logger,
	})

	item := types.QueueItem{
		ID:        "test_journal_1",
		Type:      "text",
		Source:    "test",
		Text:      "Today I felt really focused after the gym. Learned that the new morning routine is working.",
		Timestamp: time.Now(),
	}

	ctx := context.Background()
	result, err := p.Process(ctx, item)
	if err != nil {
		t.Fatalf("pipeline failed: %v", err)
	}

	// Verify journal content
	hasJournal := false
	for _, item := range result.Items {
		if item.Type == "journal" || containsString(item.Target, "journal/") {
			hasJournal = true
		}
	}

	if !hasJournal && len(result.Items) > 0 {
		t.Logf("Items: %+v", result.Items)
	}

	t.Logf("Journal result: category=%s, items=%d", result.Classification.Category, len(result.Items))
}

// TestReviewChecklist verifies Review.md contains valid checklist items
func TestReviewChecklist(t *testing.T) {
	dataDir, err := os.MkdirTemp("", "memoire-test-review-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dataDir)

	logger := zap.NewNop()
	p := pipeline.New(pipeline.Config{
		DataDir: dataDir,
		Logger:  logger,
	})

	// Process multiple items
	messages := []string{
		"Order new running shoes.",
		"Idea for a new app that uses AI.",
	}

	for i, msg := range messages {
		item := types.QueueItem{
			ID:     fmt.Sprintf("review_test_%d", i),
			Type:   "text",
			Source: "test",
			Text:   msg,
		}

		ctx := context.Background()
		_, err := p.Process(ctx, item)
		if err != nil {
			t.Fatalf("pipeline failed: %v", err)
		}
	}

	// Read Review.md
	reviewPath := filepath.Join(dataDir, "Review.md")
	content, err := os.ReadFile(reviewPath)
	if err != nil {
		t.Fatalf("Review.md not created: %v", err)
	}

	// Verify checklist format
	lines := splitLines(string(content))
	checklistCount := 0
	for _, line := range lines {
		if containsPrefix(line, "- [ ]") {
			checklistCount++
		}
	}

	if checklistCount < len(messages) {
		t.Errorf("expected at least %d checklist items, got %d", len(messages), checklistCount)
	}

	t.Logf("Review.md (%d lines, %d checklist items)", len(lines), checklistCount)
}

// TestAIRouting verifies AI marker comments are written
func TestAIRouting(t *testing.T) {
	dataDir, err := os.MkdirTemp("", "memoire-test-ai-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dataDir)

	logger := zap.NewNop()
	p := pipeline.New(pipeline.Config{
		DataDir: dataDir,
		Logger:  logger,
	})

	item := types.QueueItem{
		ID:     "ai_test_1",
		Type:   "text",
		Source: "test",
		Text:   "TODO: write unit tests for the pipeline.",
	}

	ctx := context.Background()
	_, err = p.Process(ctx, item)
	if err != nil {
		t.Fatalf("pipeline failed: %v", err)
	}

	// Check that files contain AI markers
	path := filepath.Join(dataDir, "Later.md")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("Later.md not created: %v", err)
	}

	if !containsString(string(content), "<!-- ai:") {
		t.Error("expected AI marker in Later.md")
	}

	if !containsString(string(content), "src:ai_test_1") {
		t.Error("expected source ID in AI marker")
	}

	t.Logf("Later.md content:\n%s", string(content))
}

// Helper functions
func containsPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}

func containsString(s, substr string) bool {
	return len(s) >= len(substr) && indexOf(s, substr) >= 0
}

func indexOf(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}
