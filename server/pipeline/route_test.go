package pipeline

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Magazem/WhispAir/types"
	"go.uber.org/zap"
)

// TestMarkerConfidenceOmittedWhenZero verifies that the AI marker leaves out
// the conf: segment entirely when confidence is zero, rather than printing
// the misleading conf:0.00.
func TestMarkerConfidenceOmittedWhenZero(t *testing.T) {
	dataDir := t.TempDir()
	logger := zap.NewNop()
	r := NewRouter(dataDir, logger)

	item := types.QueueItem{ID: "zero_conf"}
	items := []types.ExtractedItem{
		{Type: "task", Text: "buy milk", Target: "Later.md", Confidence: 0},
	}

	ctx := context.Background()
	if err := r.Route(ctx, item, items); err != nil {
		t.Fatalf("Route failed: %v", err)
	}

	content, err := os.ReadFile(filepath.Join(dataDir, "Later.md"))
	if err != nil {
		t.Fatalf("Later.md not created: %v", err)
	}

	got := string(content)
	if strings.Contains(got, "conf:") {
		t.Errorf("marker should omit conf: when confidence is 0, got:\n%s", got)
	}
	// Marker should still carry the source id and the text.
	if !strings.Contains(got, "src:zero_conf") {
		t.Errorf("marker should contain source id, got:\n%s", got)
	}
	if !strings.Contains(got, "buy milk") {
		t.Errorf("marker should contain item text, got:\n%s", got)
	}
}

// TestMarkerConfidencePresentWhenNonZero verifies the marker prints the real
// confidence value when one is set.
func TestMarkerConfidencePresentWhenNonZero(t *testing.T) {
	dataDir := t.TempDir()
	logger := zap.NewNop()
	r := NewRouter(dataDir, logger)

	item := types.QueueItem{ID: "real_conf"}
	items := []types.ExtractedItem{
		{Type: "idea", Text: "build a rocket", Target: "brain/build-a-rocket.md", Confidence: 0.92},
	}

	ctx := context.Background()
	if err := r.Route(ctx, item, items); err != nil {
		t.Fatalf("Route failed: %v", err)
	}

	content, err := os.ReadFile(filepath.Join(dataDir, "brain", "build-a-rocket.md"))
	if err != nil {
		t.Fatalf("target file not created: %v", err)
	}

	got := string(content)
	if !strings.Contains(got, "conf:0.92") {
		t.Errorf("marker should contain conf:0.92, got:\n%s", got)
	}
}

// TestRouteConcurrentWrites verifies that many goroutines calling Route
// concurrently produce exactly the expected number of well-formed Review.md
// lines with no torn (partially written) lines.
func TestRouteConcurrentWrites(t *testing.T) {
	dataDir := t.TempDir()
	logger := zap.NewNop()
	r := NewRouter(dataDir, logger)

	const n = 50
	var wg sync.WaitGroup
	wg.Add(n)

	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			item := types.QueueItem{ID: fmt.Sprintf("conc_%d", i)}
			items := []types.ExtractedItem{
				{Type: "task", Text: "concurrent task", Target: "Later.md", Confidence: 0.7},
			}
			ctx := context.Background()
			if err := r.Route(ctx, item, items); err != nil {
				t.Errorf("Route failed: %v", err)
			}
		}(i)
	}

	wg.Wait()

	reviewPath := filepath.Join(dataDir, "Review.md")
	content, err := os.ReadFile(reviewPath)
	if err != nil {
		t.Fatalf("Review.md not created: %v", err)
	}

	// Every line must be a complete, well-formed checklist line.
	lines := strings.Split(strings.TrimRight(string(content), "\n"), "\n")
	if len(lines) != n {
		t.Errorf("expected %d checklist lines, got %d", n, len(lines))
	}

	for i, line := range lines {
		if !strings.HasPrefix(line, "- [ ] ") {
			t.Errorf("line %d is not a well-formed checklist line: %q", i, line)
		}
		if !strings.HasSuffix(line, "]") {
			t.Errorf("line %d appears torn (no closing bracket): %q", i, line)
		}
		// A torn line would contain a partial arrow or bracket; verify the
		// full " → [" separator is present.
		if !strings.Contains(line, " → [") {
			t.Errorf("line %d is missing the arrow separator: %q", i, line)
		}
	}
}

// TestRouteTargetRouting verifies that each item type lands in its expected
// target file and that Review.md gets a checklist line for each.
func TestRouteTargetRouting(t *testing.T) {
	dataDir := t.TempDir()
	logger := zap.NewNop()
	r := NewRouter(dataDir, logger)

	item := types.QueueItem{ID: "route_types"}
	items := []types.ExtractedItem{
		{Type: "task", Text: "file the taxes", Target: "Later.md", Confidence: 0.8},
		{Type: "idea", Text: "gravity lens", Target: "brain/gravity-lens.md", Confidence: 0.6},
		{Type: "journal", Text: "felt focused today", Target: "journal/2026.07 July.md", Confidence: 0.5},
		{Type: "mixed", Text: "ambiguous thought", Target: "Chat.md", Confidence: 0.4},
	}

	ctx := context.Background()
	if err := r.Route(ctx, item, items); err != nil {
		t.Fatalf("Route failed: %v", err)
	}

	// Each target file must exist and contain the item text.
	for _, it := range items {
		path := filepath.Join(dataDir, it.Target)
		content, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("target file %s not created: %v", it.Target, err)
			continue
		}
		if !strings.Contains(string(content), it.Text) {
			t.Errorf("target file %s does not contain text %q", it.Target, it.Text)
		}
	}

	// Review.md must have exactly one checklist line per item.
	reviewPath := filepath.Join(dataDir, "Review.md")
	content, err := os.ReadFile(reviewPath)
	if err != nil {
		t.Fatalf("Review.md not created: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(content), "\n"), "\n")
	if len(lines) != len(items) {
		t.Errorf("expected %d checklist lines, got %d", len(items), len(lines))
	}
}

// TestRouteHonorsCancellation verifies that a cancelled context stops routing
// before writing all items.
func TestRouteHonorsCancellation(t *testing.T) {
	dataDir := t.TempDir()
	logger := zap.NewNop()
	r := NewRouter(dataDir, logger)

	item := types.QueueItem{ID: "cancel_me"}
	items := []types.ExtractedItem{
		{Type: "task", Text: "first", Target: "Later.md", Confidence: 0.9},
		{Type: "task", Text: "second", Target: "Later.md", Confidence: 0.9},
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before routing begins

	err := r.Route(ctx, item, items)
	if err == nil {
		t.Fatal("expected error from cancelled context, got nil")
	}
	if err != context.Canceled {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}

// TestRouteDefaultTarget verifies that an empty target falls back to Chat.md.
func TestRouteDefaultTarget(t *testing.T) {
	dataDir := t.TempDir()
	logger := zap.NewNop()
	r := NewRouter(dataDir, logger)

	item := types.QueueItem{ID: "default_tgt"}
	items := []types.ExtractedItem{
		{Type: "task", Text: "no target given", Target: "", Confidence: 0.3},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := r.Route(ctx, item, items); err != nil {
		t.Fatalf("Route failed: %v", err)
	}

	_, err := os.Stat(filepath.Join(dataDir, "Chat.md"))
	if err != nil {
		t.Errorf("expected Chat.md to exist as default target: %v", err)
	}
}
