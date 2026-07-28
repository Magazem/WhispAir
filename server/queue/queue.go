// Package queue provides a durable, crash-safe work queue for pipeline items.
//
// The previous implementation was a buffered channel that dropped items when
// full and lost everything on restart, while the raw audio sat on disk with
// nothing pointing at it. For a capture system whose entire value is not losing
// a thought, that was the highest-risk gap in the design.
//
// Every item is written to <dataDir>/inbox/raw/<id>.json before Enqueue
// reports success, so a crash at any point after that leaves the work
// recoverable by Replay. Completed items move to inbox/done, permanently failed
// ones to inbox/failed alongside the error that killed them.
package queue

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/Magazem/WhispAir/types"
	"go.uber.org/zap"
)

const (
	dirRaw    = "raw"
	dirDone   = "done"
	dirFailed = "failed"
)

// Queue is a durable FIFO of pipeline items backed by the filesystem.
type Queue struct {
	baseDir string
	logger  *zap.Logger

	mu sync.Mutex
}

// New creates a Queue rooted at <dataDir>/inbox, creating its directories.
func New(dataDir string, logger *zap.Logger) (*Queue, error) {
	if logger == nil {
		logger = zap.NewNop()
	}
	q := &Queue{
		baseDir: filepath.Join(dataDir, "inbox"),
		logger:  logger,
	}
	for _, d := range []string{dirRaw, dirDone, dirFailed} {
		if err := os.MkdirAll(filepath.Join(q.baseDir, d), 0o755); err != nil {
			return nil, fmt.Errorf("failed to create queue dir %s: %w", d, err)
		}
	}
	return q, nil
}

func (q *Queue) pathFor(dir, id string) string {
	return filepath.Join(q.baseDir, dir, sanitizeID(id)+".json")
}

// Persist durably records an item as pending work. It returns an error rather
// than dropping the item; callers must treat a failure here as a failure to
// accept the message.
func (q *Queue) Persist(item types.QueueItem) error {
	q.mu.Lock()
	defer q.mu.Unlock()

	data, err := json.MarshalIndent(item, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal queue item %s: %w", item.ID, err)
	}

	path := q.pathFor(dirRaw, item.ID)

	// Write to a temp file and rename, so a crash mid-write cannot leave a
	// half-written entry that Replay would fail to parse.
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("failed to write queue item %s: %w", item.ID, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("failed to commit queue item %s: %w", item.ID, err)
	}

	q.logger.Debug("queue item persisted", zap.String("id", item.ID), zap.String("path", path))
	return nil
}

// Complete marks an item as successfully processed, moving it to inbox/done.
func (q *Queue) Complete(item types.QueueItem) error {
	q.mu.Lock()
	defer q.mu.Unlock()

	src := q.pathFor(dirRaw, item.ID)
	dst := q.pathFor(dirDone, item.ID)
	if err := os.Rename(src, dst); err != nil {
		if os.IsNotExist(err) {
			// Already moved, or replayed from a previous run. Not an error.
			return nil
		}
		return fmt.Errorf("failed to complete queue item %s: %w", item.ID, err)
	}
	return nil
}

// Fail records a permanent failure, moving the item to inbox/failed and
// recording the error next to it so the cause is not lost.
func (q *Queue) Fail(item types.QueueItem, cause error) error {
	q.mu.Lock()
	defer q.mu.Unlock()

	record := struct {
		Item  types.QueueItem `json:"item"`
		Error string          `json:"error"`
	}{Item: item}
	if cause != nil {
		record.Error = cause.Error()
	}

	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal failure record %s: %w", item.ID, err)
	}

	dst := q.pathFor(dirFailed, item.ID)
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		return fmt.Errorf("failed to write failure record %s: %w", item.ID, err)
	}

	// Best effort: remove the pending entry now that the failure is recorded.
	if err := os.Remove(q.pathFor(dirRaw, item.ID)); err != nil && !os.IsNotExist(err) {
		q.logger.Warn("failed to remove pending entry after failure",
			zap.String("id", item.ID), zap.Error(err))
	}

	q.logger.Warn("queue item failed permanently",
		zap.String("id", item.ID), zap.Error(cause), zap.String("record", dst))
	return nil
}

// Replay returns every item still pending in inbox/raw, oldest first by
// filename. Called on startup so a restart resumes rather than forgets.
func (q *Queue) Replay() ([]types.QueueItem, error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	dir := filepath.Join(q.baseDir, dirRaw)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read queue dir: %w", err)
	}

	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)

	items := make([]types.QueueItem, 0, len(names))
	for _, name := range names {
		path := filepath.Join(dir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			q.logger.Warn("skipping unreadable queue entry",
				zap.String("path", path), zap.Error(err))
			continue
		}
		var item types.QueueItem
		if err := json.Unmarshal(data, &item); err != nil {
			q.logger.Warn("skipping unparseable queue entry",
				zap.String("path", path), zap.Error(err))
			continue
		}
		items = append(items, item)
	}

	if len(items) > 0 {
		q.logger.Info("replaying persisted queue items", zap.Int("count", len(items)))
	}
	return items, nil
}

// PendingCount reports how many items are still awaiting processing.
func (q *Queue) PendingCount() int {
	items, err := q.Replay()
	if err != nil {
		return 0
	}
	return len(items)
}

// sanitizeID keeps IDs safe as filenames without collapsing distinct IDs.
func sanitizeID(id string) string {
	if id == "" {
		return "unknown"
	}
	out := make([]rune, 0, len(id))
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '-', r == '_', r == '.':
			out = append(out, r)
		default:
			out = append(out, '_')
		}
	}
	return string(out)
}
