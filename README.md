# WhispAir / Memoire — Private Smart Thought-Capture System

> Quality over speed. Privacy over convenience. Your first brain over the second one.

**Memoire** is a personal, local-first thought-capture system. It captures thoughts, voice notes, and videos from multiple inputs, intelligently organizes them using local AI models, and surfaces them for review—all without ever leaving your hardware.

---

## Quick Start

### Prerequisites

- Go 1.22+ installed
- A Telegram bot token (from [@BotFather](https://t.me/BotFather))
- (Optional) Ollama with Qwen 3 models for local LLM
- (Optional) Whisper.cpp for local transcription

### Build

```bash
go build -o memoire ./cmd/server
```

### Run

```bash
# Create data directory
mkdir -p ~/memoire-data

# Set environment variables
export MEMOIRE_DATA_DIR=~/memoire-data
export TG_BOT_TOKEN=your_token_here
export TG_BOT_API_URL=http://localhost:8081

# Run
./memoire
```

### Test

```bash
go test -v ./...
```

---

## Architecture

```
Your inputs (voice, video, Telegram)
        ↓
   [Server: Ubuntu PC]
        ↓
   Transcription (Whisper.cpp)
        ↓
   Classification (Qwen 3 0.6B)
        ↓
   Extraction (Qwen 3 8B/14B)
        ↓
   Critic Pass (same model, second prompt)
        ↓
   Smart routing to target files
        ↓
   Review checklist (Review.md)
        ↓
   [Your laptop or iPhone PWA]
   Edit → Organize → Done
```

---

## Project Structure

```
memoire/
├── cmd/
│   └── server/              # Main entrypoint
│       └── main.go
├── server/
│   ├── server.go            # Core server, queue, HTTP handlers
│   ├── server_test.go       # Integration tests
│   ├── sync/
│   │   └── sync.go          # Sync API for PWA
│   ├── plugins/
│   │   └── plugins.go       # Plugin interface + implementations
│   ├── pipeline/
│   │   ├── pipeline.go      # Pipeline orchestration
│   │   ├── classify.go      # Message classification
│   │   ├── extract.go       # Structured extraction
│   │   ├── transcribe.go    # Whisper.cpp wrapper
│   │   ├── prompts.go       # Hot-reloadable prompt loader
│   │   └── prompts.yaml     # All prompts (YAML)
│   └── llm/
│       └── llm.go           # LLM client (Ollama + Claude fallback)
├── deploy/
│   ├── Makefile             # Build, deploy, rollback
│   ├── memoire.service      # systemd unit
│   └── deploy.sh            # Deployment script
├── .env.example             # Environment variable template
├── go.mod
└── README.md
```

---

## API Endpoints

| Method | Path | Description |
|--------|------|-------------|
| GET | `/health` | Health check |
| POST | `/webhook/telegram` | Telegram bot webhook |
| POST | `/voice` | Upload voice (iOS shortcut) |
| POST | `/video` | Upload video |
| GET | `/sync/files` | List all files |
| GET | `/sync/file/*path` | Get file content |
| PUT | `/sync/file/*path` | Replace file content |
| POST | `/sync/file/*path` | Append to file |
| DELETE | `/sync/file/*path` | Delete file |
| POST | `/sync/correction` | Log correction for fine-tuning |

---

## Data Directory Structure

```
~/memoire-data/
├── Chat.md                      # Default text dump
├── Review.md                    # AI-extracted items awaiting review
├── Later.md                     # Tasks
├── Read.md / Watch.md / Shop.md # Capture files
├── brain/                       # Ideas (one per file)
├── journal/
│   └── 2026.07 July.md
├── habits/
├── media/
│   ├── voice_msg-abc.ogg
│   ├── glasses_2026-05-21_1823.mp4
│   └── transcripts/
│       └── 2026-05-21_1823.txt
├── training/
│   └── corrections.jsonl        # Fine-tuning corpus
├── archive/
└── config.json
```

---

## AI Marker Comments

Every item touched by the LLM carries an HTML comment marker:

```markdown
<!-- ai:2026-05-21T18:23 src:msg-abc conf:0.92 -->
```

These are invisible in rendered Markdown but greppable:
```bash
grep -r "<!-- ai:" ~/memoire-data/
```

---

## Deploy

### From laptop (cross-compile to Linux):

```bash
# Build and deploy
make build
make deploy

# View logs
make logs

# Rollback if needed
make rollback
```

### Manual deploy:

```bash
# Build for Linux
GOOS=linux GOARCH=amd64 go build -o memoire ./cmd/server

# Copy to server
scp memoire server/pipeline/prompts.yaml memoire@server:/opt/memoire/

# Restart
ssh memoire@server "sudo systemctl restart memoire"
```

---

## Configuration

Environment variables (or `.env` file):

| Variable | Description | Default |
|----------|-------------|---------|
| `MEMOIRE_SERVER_HOST` | Server bind address | `0.0.0.0` |
| `MEMOIRE_SERVER_PORT` | Server port | `8080` |
| `MEMOIRE_DATA_DIR` | Data directory | `~/memoire-data` |
| `MEMOIRE_LOG_LEVEL` | Log level | `info` |
| `TG_BOT_TOKEN` | Telegram bot token | — |
| `TG_BOT_API_URL` | Telegram Bot API URL | `http://localhost:8081` |
| `OLLAMA_HOST` | Ollama server URL | `http://localhost:11434` |
| `WHISPER_PATH` | Whisper CLI path | — |
| `CLAUDE_API_KEY` | Claude API key (optional) | — |

---

## Testing

```bash
# Run all tests
go test -v ./...

# Run specific test
go test -v -run TestPipelineTextMessage ./server/

# Run with race detector
go test -race ./...
```

---

## Pipeline Flow

1. **Capture** — Message arrives via Telegram webhook, voice upload, or video upload
2. **Store** — Raw media saved to `media/`, queue entry created
3. **Transcribe** — Whisper.cpp converts audio to text (for voice/video)
4. **Classify** — Tiny model (Qwen 3 0.6B) tags category + confidence
5. **Extract** — Larger model (Qwen 3 8B/14B) produces structured JSON
6. **Critic** — Second pass reviews extraction for missed/misclassified items
7. **Route** — Items written to target files with AI markers
8. **Review** — Checklist lines added to `Review.md`
9. **Confirm** — Bot reacts with 👌 in Telegram

---

## Quality Features

- **Multi-pass extraction** — First pass extracts, critic refines
- **Batch processing** — Day's messages processed together for cross-referencing
- **Schema-enforced JSON** — Ollama forced-JSON mode with retries
- **Per-category prompts** — Different prompts for tasks, ideas, journal
- **Transcript preservation** — Raw audio always kept, re-runnable
- **Correction logging** — Every edit becomes fine-tuning data
- **AI markers** — Full auditability via grep

---

## License

MIT License — see [LICENSE](LICENSE) for details.

Built on [files.md](https://github.com/osmoscraft/files.md) by osmoscraft.
All new code (plugins, pipeline, LLM layers, deploy tooling) written as extensions.
