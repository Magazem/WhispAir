package queue_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Magazem/WhispAir/server/queue"
	"github.com/Magazem/WhispAir/types"
	"go.uber.org/zap"
)

func newQueue(t *testing.T) (*queue.Queue, string) {
	t.Helper()
	dir := t.TempDir()
	q, err := queue.New(dir, zap.NewNop())
	if err != nil {
		t.Fatalf("queue.New: %v", err)
	}
	return q, dir
}

func sampleItem(id string) types.QueueItem {
	return types.QueueItem{
		ID:        id,
		Type:      "voice",
		Source:    "test",
		MediaPath: "/tmp/audio.ogg",
		Timestamp: time.Unix(1700000000, 0),
	}
}

func TestPersistWritesFileBeforeReturning(t *testing.T) {
	q, dir := newQueue(t)

	if err := q.Persist(sampleItem("tg_1_2")); err != nil {
		t.Fatalf("Persist: %v", err)
	}

	path := filepath.Join(dir, "inbox", "raw", "tg_1_2.json")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected persisted entry at %s: %v", path, err)
	}
}

func TestReplayReturnsPendingItems(t *testing.T) {
	q, _ := newQueue(t)

	for _, id := range []string{"a1", "a2", "a3"} {
		if err := q.Persist(sampleItem(id)); err != nil {
			t.Fatalf("Persist %s: %v", id, err)
		}
	}

	items, err := q.Replay()
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if len(items) != 3 {
		t.Fatalf("expected 3 replayed items, got %d", len(items))
	}

	// Fields must survive the round-trip.
	if items[0].ID != "a1" || items[0].Type != "voice" || items[0].MediaPath != "/tmp/audio.ogg" {
		t.Errorf("item did not round-trip: %+v", items[0])
	}
}

// TestReplaySurvivesRestart is the point of the whole package: a brand new
// Queue over the same directory must still see the pending work.
func TestReplaySurvivesRestart(t *testing.T) {
	dir := t.TempDir()

	q1, err := queue.New(dir, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	if err := q1.Persist(sampleItem("survivor")); err != nil {
		t.Fatal(err)
	}

	// Simulate a process restart.
	q2, err := queue.New(dir, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	items, err := q2.Replay()
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ID != "survivor" {
		t.Fatalf("expected the pending item to survive restart, got %+v", items)
	}
}

func TestCompleteRemovesFromPending(t *testing.T) {
	q, dir := newQueue(t)
	item := sampleItem("done_me")

	if err := q.Persist(item); err != nil {
		t.Fatal(err)
	}
	if err := q.Complete(item); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	items, err := q.Replay()
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Errorf("expected no pending items after Complete, got %d", len(items))
	}

	if _, err := os.Stat(filepath.Join(dir, "inbox", "done", "done_me.json")); err != nil {
		t.Errorf("expected the item in inbox/done: %v", err)
	}
}

func TestCompleteIsIdempotent(t *testing.T) {
	q, _ := newQueue(t)
	item := sampleItem("twice")

	if err := q.Persist(item); err != nil {
		t.Fatal(err)
	}
	if err := q.Complete(item); err != nil {
		t.Fatalf("first Complete: %v", err)
	}
	if err := q.Complete(item); err != nil {
		t.Errorf("second Complete should be a no-op, got: %v", err)
	}
}

func TestFailRecordsErrorAndClearsPending(t *testing.T) {
	q, dir := newQueue(t)
	item := sampleItem("bad_one")

	if err := q.Persist(item); err != nil {
		t.Fatal(err)
	}
	cause := errors.New("transcription failed: no model")
	if err := q.Fail(item, cause); err != nil {
		t.Fatalf("Fail: %v", err)
	}

	failedPath := filepath.Join(dir, "inbox", "failed", "bad_one.json")
	data, err := os.ReadFile(failedPath)
	if err != nil {
		t.Fatalf("expected a failure record at %s: %v", failedPath, err)
	}
	if !contains(string(data), "no model") {
		t.Errorf("failure record should preserve the cause, got: %s", data)
	}

	items, err := q.Replay()
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Errorf("a failed item must not stay pending, got %d", len(items))
	}
}

func TestSanitizedIDsDoNotEscapeTheQueueDir(t *testing.T) {
	q, dir := newQueue(t)

	// An id containing separators must not write outside inbox/raw.
	if err := q.Persist(sampleItem("../../escape")); err != nil {
		t.Fatalf("Persist: %v", err)
	}

	entries, err := os.ReadDir(filepath.Join(dir, "inbox", "raw"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected exactly 1 entry inside inbox/raw, got %d", len(entries))
	}
	if _, err := os.Stat(filepath.Join(dir, "escape.json")); err == nil {
		t.Error("id was not sanitized: file escaped the queue directory")
	}
}

func TestReplaySkipsUnparseableEntries(t *testing.T) {
	q, dir := newQueue(t)

	if err := q.Persist(sampleItem("good")); err != nil {
		t.Fatal(err)
	}
	// Drop a corrupt file next to the good one.
	bad := filepath.Join(dir, "inbox", "raw", "corrupt.json")
	if err := os.WriteFile(bad, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}

	items, err := q.Replay()
	if err != nil {
		t.Fatalf("Replay should tolerate a corrupt entry, got: %v", err)
	}
	if len(items) != 1 || items[0].ID != "good" {
		t.Fatalf("expected only the good item, got %+v", items)
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (func() bool {
		for i := 0; i+len(needle) <= len(haystack); i++ {
			if haystack[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}
