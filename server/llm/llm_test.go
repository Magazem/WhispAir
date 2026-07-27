package llm_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Magazem/WhispAir/server/llm"
	"github.com/Magazem/WhispAir/types"
	"go.uber.org/zap"
)

// requestRecorder captures the bodies of all requests made to a stub server.
type requestRecorder struct {
	mu     sync.Mutex
	bodies []map[string]interface{}
	status int
}

func (r *requestRecorder) add(body map[string]interface{}) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.bodies = append(r.bodies, body)
}

func (r *requestRecorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.bodies)
}

func (r *requestRecorder) last() map[string]interface{} {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.bodies[len(r.bodies)-1]
}

// newStubServer creates an httptest server that delegates to handler and
// records each request body into the recorder.
func newStubServer(handler func(w http.ResponseWriter, r *http.Request, rec *requestRecorder), rec *requestRecorder) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bodyBytes, _ := io.ReadAll(r.Body)
		var body map[string]interface{}
		_ = json.Unmarshal(bodyBytes, &body)
		rec.add(body)
		handler(w, r, rec)
	}))
}

// helper to build a client pointing at a stub server.
func newTestClient(serverURL string, allowMock bool) llm.Client {
	return llm.NewClientWithOptions(llm.Config{
		OllamaHost: serverURL,
		Logger:     zap.NewNop(),
		AllowMock:  allowMock,
	})
}

// TestClassifySendsFormatJSON verifies classifyOllama sends "format": "json".
func TestClassifySendsFormatJSON(t *testing.T) {
	rec := &requestRecorder{}
	server := newStubServer(func(w http.ResponseWriter, r *http.Request, rec *requestRecorder) {
		w.WriteHeader(200)
		io.WriteString(w, `{"response": "{\"category\":\"task\",\"confidence\":0.9\"}"}`)
	}, rec)
	defer server.Close()

	client := newTestClient(server.URL, false)
	_, err := client.Classify(context.Background(), "buy groceries")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if rec.count() != 1 {
		t.Fatalf("expected 1 request, got %d", rec.count())
	}
	if rec.last()["format"] != "json" {
		t.Errorf("expected format=json in request body, got %v", rec.last()["format"])
	}
}

// TestExtractSendsFormatJSON verifies extractOllama sends "format": "json".
func TestExtractSendsFormatJSON(t *testing.T) {
	rec := &requestRecorder{}
	server := newStubServer(func(w http.ResponseWriter, r *http.Request, rec *requestRecorder) {
		w.WriteHeader(200)
		io.WriteString(w, `{"response": "[{\"type\":\"task\",\"text\":\"buy groceries\",\"target\":\"Later.md\"}]"}`)
	}, rec)
	defer server.Close()

	client := newTestClient(server.URL, false)
	_, err := client.Extract(context.Background(), "buy groceries", "task")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if rec.last()["format"] != "json" {
		t.Errorf("expected format=json in request body, got %v", rec.last()["format"])
	}
}

// TestCriticSendsFormatJSON verifies criticOllama sends "format": "json".
func TestCriticSendsFormatJSON(t *testing.T) {
	rec := &requestRecorder{}
	server := newStubServer(func(w http.ResponseWriter, r *http.Request, rec *requestRecorder) {
		w.WriteHeader(200)
		io.WriteString(w, `{"response": "{\"missed\":[],\"reclassified\":[],\"splittable\":[]}"}`)
	}, rec)
	defer server.Close()

	client := newTestClient(server.URL, false)
	items := []types.ExtractedItem{{Type: "task", Text: "buy groceries", Target: "Later.md"}}
	_, err := client.Critic(context.Background(), "buy groceries", items)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if rec.last()["format"] != "json" {
		t.Errorf("expected format=json in request body, got %v", rec.last()["format"])
	}
}

// TestRetryLoop verifies the retry helper makes 3 attempts and feeds the parse
// error back into the prompt on each retry.
func TestRetryLoop(t *testing.T) {
	rec := &requestRecorder{}
	attempt := 0
	server := newStubServer(func(w http.ResponseWriter, r *http.Request, rec *requestRecorder) {
		attempt++
		w.WriteHeader(200)
		if attempt < 3 {
			// Return malformed JSON for the first two attempts.
			io.WriteString(w, `{"response": "this is not valid json"}`)
		} else {
			// Return valid JSON on the third attempt.
			io.WriteString(w, `{"response": "{\"category\":\"idea\",\"confidence\":0.8\"}"}`)
		}
	}, rec)
	defer server.Close()

	client := newTestClient(server.URL, false)
	result, err := client.Classify(context.Background(), "test message")
	if err != nil {
		t.Fatalf("unexpected error after retry: %v", err)
	}

	if attempt != 3 {
		t.Errorf("expected 3 attempts, got %d", attempt)
	}

	// The second and third prompts should contain the parse error from the
	// previous attempt.
	for i := 1; i < rec.count(); i++ {
		prompt := rec.bodies[i]["prompt"].(string)
		if !strings.Contains(prompt, "invalid JSON") {
			t.Errorf("attempt %d prompt should contain 'invalid JSON', got: %s", i+1, prompt)
		}
		if !strings.Contains(prompt, "this is not valid json") {
			t.Errorf("attempt %d prompt should contain previous raw response, got: %s", i+1, prompt)
		}
	}

	if result.Category != "idea" {
		t.Errorf("expected category=idea from successful 3rd attempt, got %s", result.Category)
	}
}

// TestExhaustedRetriesReturnError verifies that when all 3 attempts fail and
// AllowMock is false, an error is returned (not mock data).
func TestExhaustedRetriesReturnError(t *testing.T) {
	rec := &requestRecorder{}
	server := newStubServer(func(w http.ResponseWriter, r *http.Request, rec *requestRecorder) {
		w.WriteHeader(200)
		io.WriteString(w, `{"response": "not valid json"}`)
	}, rec)
	defer server.Close()

	client := newTestClient(server.URL, false)
	_, err := client.Classify(context.Background(), "test message")
	if err == nil {
		t.Fatal("expected error after 3 failed attempts, got nil")
	}

	if rec.count() != 3 {
		t.Errorf("expected 3 attempts, got %d", rec.count())
	}
}

// TestExhaustedRetriesReturnMockWhenAllowed verifies that when AllowMock is
// true, mock data is returned after retries are exhausted.
func TestExhaustedRetriesReturnMockWhenAllowed(t *testing.T) {
	rec := &requestRecorder{}
	server := newStubServer(func(w http.ResponseWriter, r *http.Request, rec *requestRecorder) {
		w.WriteHeader(200)
		io.WriteString(w, `{"response": "not valid json"}`)
	}, rec)
	defer server.Close()

	client := newTestClient(server.URL, true)
	result, err := client.Classify(context.Background(), "buy groceries")
	if err != nil {
		t.Fatalf("expected mock fallback with no error, got: %v", err)
	}

	// mockClassify should classify "buy groceries" as a task.
	if result.Category != "task" {
		t.Errorf("expected mock category=task, got %s", result.Category)
	}
}

// TestCriticAppliesResponse verifies the critic correctly applies missed,
// reclassified, and splittable feedback.
func TestCriticAppliesResponse(t *testing.T) {
	rec := &requestRecorder{}
	server := newStubServer(func(w http.ResponseWriter, r *http.Request, rec *requestRecorder) {
		w.WriteHeader(200)
		// The critic says: one item was missed, one was misclassified, one can be split.
		response := `{"response": "{\"missed\":[\"new missed item\"],\"reclassified\":[{\"old_type\":\"task\",\"new_type\":\"idea\",\"text\":\"reclassify me\"}],\"splittable\":[{\"original\":\"split this into two\",\"parts\":[\"part a\",\"part b\"]}]}"}`
		io.WriteString(w, response)
	}, rec)
	defer server.Close()

	client := newTestClient(server.URL, false)
	items := []types.ExtractedItem{
		{Type: "task", Text: "reclassify me", Target: "Later.md"},
		{Type: "idea", Text: "split this into two", Target: "brain/split-this-into-two.md"},
	}

	result, err := client.Critic(context.Background(), "original message", items)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify reclassified item.
	foundReclassified := false
	for _, item := range result {
		if item.Text == "reclassify me" {
			if item.Type != "idea" {
				t.Errorf("expected reclassified item type=idea, got %s", item.Type)
			}
			if item.Target != "brain/reclassify-me.md" {
				t.Errorf("expected reclassified item target=brain/reclassify-me.md, got %s", item.Target)
			}
			foundReclassified = true
		}
	}
	if !foundReclassified {
		t.Error("reclassified item not found in result")
	}

	// Verify missed item was added.
	foundMissed := false
	for _, item := range result {
		if item.Text == "new missed item" {
			foundMissed = true
		}
	}
	if !foundMissed {
		t.Error("missed item not found in result")
	}

	// Verify splittable item was replaced by its parts.
	foundPartA := false
	foundPartB := false
	for _, item := range result {
		if item.Text == "part a" {
			foundPartA = true
		}
		if item.Text == "part b" {
			foundPartB = true
		}
		if item.Text == "split this into two" {
			t.Error("original splittable item should have been replaced by its parts")
		}
	}
	if !foundPartA || !foundPartB {
		t.Errorf("expected both split parts, got partA=%v partB=%v", foundPartA, foundPartB)
	}
}

// TestStripThinkBlocks verifies that <think> reasoning blocks are stripped.
func TestStripThinkBlocks(t *testing.T) {
	// We test this indirectly via the classify call: the stub returns a
	// response wrapped in <think> blocks and we verify it parses correctly.
	rec := &requestRecorder{}
	server := newStubServer(func(w http.ResponseWriter, r *http.Request, rec *requestRecorder) {
		w.WriteHeader(200)
		io.WriteString(w, `{"response": "<think>\nI need to classify this.\n</think>{\"category\":\"journal\",\"confidence\":0.7\"}"}`)
	}, rec)
	defer server.Close()

	client := newTestClient(server.URL, false)
	result, err := client.Classify(context.Background(), "today I felt great")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Category != "journal" {
		t.Errorf("expected category=journal after stripping think block, got %s", result.Category)
	}
}

// TestNewClientKeepsOriginalSignature verifies the original NewClient
// constructor still works and produces a client with AllowMock=false.
func TestNewClientKeepsOriginalSignature(t *testing.T) {
	rec := &requestRecorder{}
	server := newStubServer(func(w http.ResponseWriter, r *http.Request, rec *requestRecorder) {
		w.WriteHeader(200)
		io.WriteString(w, `{"response": "not valid json"}`)
	}, rec)
	defer server.Close()

	// Use the original constructor signature.
	client := llm.NewClient(server.URL, "", zap.NewNop())

	// With AllowMock=false (the default), errors should propagate.
	_, err := client.Classify(context.Background(), "test")
	if err == nil {
		t.Fatal("expected error with AllowMock=false, got nil")
	}
}