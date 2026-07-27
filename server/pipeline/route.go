package pipeline

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/Magazem/WhispAir/types"
	"go.uber.org/zap"
)

// Router handles writing items to target files and Review.md
type Router struct {
	dataDir string
	logger  *zap.Logger
	mu      sync.Mutex
}

// NewRouter creates a new router
func NewRouter(dataDir string, logger *zap.Logger) *Router {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Router{
		dataDir: dataDir,
		logger:  logger,
	}
}

// Route writes items to their target files and adds to Review.md
func (r *Router) Route(ctx context.Context, item types.QueueItem, items []types.ExtractedItem) error {
	// Ensure directories
	dirs := []string{
		filepath.Join(r.dataDir, "brain"),
		filepath.Join(r.dataDir, "journal"),
		filepath.Join(r.dataDir, "training"),
		filepath.Join(r.dataDir, "habits"),
		filepath.Join(r.dataDir, "archive"),
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0755); err != nil {
			return fmt.Errorf("failed to create dir %s: %w", d, err)
		}
	}

	for _, extracted := range items {
		// Honour cancellation between items so a cancelled context
		// stops routing rather than silently writing partial output.
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		target := extracted.Target
		if target == "" {
			target = "Chat.md"
		}

		// Build content with AI marker. Confidence of 0 means "unknown";
		// omit the conf: segment entirely rather than printing conf:0.00.
		var marker string
		if extracted.Confidence > 0 {
			marker = fmt.Sprintf("<!-- ai:%s src:%s conf:%.2f -->\n\n%s\n\n",
				time.Now().UTC().Format("2006-01-02T15:04:05"),
				item.ID,
				extracted.Confidence,
				extracted.Text,
			)
		} else {
			marker = fmt.Sprintf("<!-- ai:%s src:%s -->\n\n%s\n\n",
				time.Now().UTC().Format("2006-01-02T15:04:05"),
				item.ID,
				extracted.Text,
			)
		}

		content := fmt.Sprintf("\n%s", marker)

		// Write to target file
		if err := r.writeToTarget(target, content); err != nil {
			return fmt.Errorf("failed to write to %s: %w", target, err)
		}

		// Add to Review.md
		if err := r.addToReview(extracted, target); err != nil {
			return fmt.Errorf("failed to add to review: %w", err)
		}
	}

	return nil
}

// writeToTarget appends content to the target file.
// Guarded by r.mu so concurrent appends cannot interleave or tear.
func (r *Router) writeToTarget(target, content string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	path := filepath.Join(r.dataDir, target)
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = f.WriteString(content)
	return err
}

// addToReview appends an item to the Review.md checklist.
// Guarded by r.mu so concurrent appends cannot interleave or tear.
func (r *Router) addToReview(item types.ExtractedItem, target string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	reviewPath := filepath.Join(r.dataDir, "Review.md")
	dir := filepath.Dir(reviewPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	line := fmt.Sprintf("- [ ] %s: %s \u2192 [%s]\n",
		capitalize(item.Type),
		item.Text,
		target,
	)

	f, err := os.OpenFile(reviewPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = f.WriteString(line)
	return err
}

// capitalize the first letter (safely handles UTF-8)
func capitalize(s string) string {
	if s == "" {
		return s
	}
	first := s[0]
	if first >= 'a' && first <= 'z' {
		first = first - 32
	}
	return string(first) + s[1:]
}