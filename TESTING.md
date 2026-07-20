# Development & Testing Guide for Memoire

This document covers how to test the memoire system locally without full server infrastructure.

---

## Quick Demo

The server and pipeline are designed with **mock fallbacks** — if Ollama or Whisper aren't installed, they use keyword-based mock implementations.

### Run without infrastructure

```bash
# From repo root
go build -o memoire ./cmd/server

# Set a temporary data dir
export MEMOIRE_DATA_DIR=./test-data

# Run (will use mock transcription and mock classification/extraction)
./memoire
```

### Test the HTTP API

```bash
# 1. Health check
curl http://localhost:8080/health

# 2. Send a text message (simulating Telegram webhook)
curl -X POST http://localhost:8080/webhook/telegram \
  -H "Content-Type: application/json" \
  -d '{
    "update_id": 1,
    "message": {
      "message_id": 12345,
      "date": 1716300000,
      "chat": {"id": 1},
      "text": "I need to buy groceries and order brake pads for the Volvo. Idea about a context-aware AI assistant."
    }
  }'

# 3. List files
curl http://localhost:8080/sync/files

# 4. Get a specific file
curl http://localhost:8080/sync/file/Later.md

# 5. Read Review.md checklist
curl http://localhost:8080/sync/file/Review.md

# 6. Check all brain/*.md files
curl http://localhost:8080/sync/files

# 7. Log a correction for fine-tuning
curl -X POST http://localhost:8080/sync/correction \
  -H "Content-Type: application/json" \
  -d '{
    "original": "buy groceries",
    "corrected": "buy groceries for the week",
    "file_path": "Later.md"
  }'
```

### Upload voice (mock)

```bash
# Create a dummy audio file
echo "mock audio content" > /tmp/test.ogg

# Upload via voice endpoint
curl -X POST http://localhost:8080/voice \
  -F "audio=@/tmp/test.ogg"

# Check the transcript was saved
curl http://localhost:8080/sync/file/media/transcripts/voice_<timestamp>.txt
```

---

## Integration Tests

```bash
# Run all tests (creates temp dirs, processes messages, verifies output)
go test -v ./server/

# Run specific test
go test -v -run TestPipelineTextMessage ./server/
```

The test does:
1. Creates a temp data directory
2. Processes a sample text message through the pipeline
3. Verifies files are created (Later.md, Review.md, brain/*.md)
4. Verifies AI markers are written
5. Verifies checklist format in Review.md

### What the test verifies

- [x] Pipeline processes text messages without errors
- [x] Files are created in correct locations
- [x] AI marker comments are included
- [x] Checklist items added to Review.md
- [x] Multi-category messages produce correctly routed items

---

## What happens when you send "I need to buy groceries and call mom. Idea about AI assistant for journaling."

The pipeline:

1. **Classifies** as "mixed" (has both task and idea keywords)
2. **Extracts** three items:
   - `task: "buy groceries" → Later.md`
   - `task: "call mom" → Later.md`
   - `idea: "Idea about AI assistant for journaling" → brain/Idea-about-AI-assistant-for-journaling.md`
3. **Writes** each to its target file with AI markers:
   ```
   <!-- ai:2026-07-20T12:00:00 src:tg_1_12345 conf:0.85 -->
   buy groceries
   ```
4. **Adds** checklist lines to Review.md:
   ```
   - [ ] Task: buy groceries → [Later.md]
   - [ ] Task: call mom → [Later.md]
   - [ ] Idea: Idea about AI assistant for journaling → [brain/Idea-about-AI-assistant-for-journaling.md]
   ```

---

## Adding Ollama (for real LLM inference)

1. Install Ollama: https://ollama.ai
2. Pull models:
   ```bash
   ollama pull qwen3:0.6b
   ollama pull qwen3:8b
   ```
3. Start Ollama: `ollama serve`
4. The server will auto-detect and use it (no config needed if using default `http://localhost:11434`)

## Adding Whisper.cpp (for real transcription)

1. Install from https://github.com/ggerganov/whisper.cpp
2. Set `WHISPER_PATH=/path/to/whisper-cli`
3. Ensure the model is downloaded:
   ```bash
   ./models/gpt-2-medium.bin  # or your preferred model
   ```

---

## Troubleshooting

### "mock transcription" in logs
Means Whisper.cpp isn't installed/found. Install it or ignore (mock works for testing).

### "using mock" in classification logs
Means Ollama isn't available. Install Ollama + models or ignore (mock works for basic testing).

### Race conditions
Run with `go test -race ./...` to detect them.

---

## Design Principles for Testing

1. **Mock first** — Every external dependency has a mock fallback so you can test the pipeline without real infrastructure.
2. **Deterministic output** — Same input produces same output (makes tests reliable).
3. **No failure is silent** — Logged at WARN level if a dependency isn't available.
