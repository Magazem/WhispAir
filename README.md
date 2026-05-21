# Memoire — A Private Smart Thought-Capture System

> Quality over speed. Privacy over convenience. Your first brain over the second one.

**Memoire** is a personal, local-first thought-capture system built on a fork of [`files.md`](https://github.com/osmoscraft/files.md). It captures thoughts, voice notes, and videos from multiple inputs, intelligently organizes them using local AI models, and surfaces them for review—all without ever leaving your hardware.

---

## Philosophy

This is not a product for mass adoption. It is a **personal instrument** designed around a single optimization target: **quality**.

- **Latency is irrelevant; correctness is everything.** Memoire chooses the larger model, the second LLM pass, and slower batch processing over speed shortcuts.
- **Privacy is non-negotiable.** Nothing leaves your hardware in v1. The Claude API exists only as an opt-in safety net per message.
- **The system serves your first brain.** It captures and pre-organizes without trying to think for you, summarize away rough edges, or become a substitute for reflection.
- **Auditability is built in.** Every AI-touched item carries a marker comment. One `grep` shows you everything the LLM wrote.

---

## How It Works

### The Big Picture

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

### Data Flow: Voice Note to Organized Idea

1. **Capture:** Send a voice note via Telegram or capture video from Meta Ray-Bans (auto-forwarded via iPhone shortcut)
2. **Transcription:** Whisper.cpp (running on GPU) converts audio to text with timestamps
3. **Triage:** A tiny classifier (Qwen 3 0.6B) tags the message category with confidence
4. **Extraction:** A larger model (Qwen 3 8B/14B) extracts structured pieces:
   - Tasks → `Later.md`
   - Ideas → `brain/`
   - Journal entries → `journal/`
5. **Critique:** The same model runs a second pass, finding missed items, misclassifications, or splittable pieces
6. **Review:** Each extracted item gets a checklist line in `Review.md` with a link to where it was written
7. **Organize:** You click through the review checklist, edit/move/delete as needed, tick when satisfied

Every item touched by the LLM carries an **AI marker comment**:
```markdown
<!-- ai:2026-05-21T18:23 src:msg-abc conf:0.92 -->
```
These are invisible in rendered Markdown but greppable: `grep -r "<!-- ai:" ~/memoire-data/` shows everything the LLM ever wrote.

---

## System Architecture

### Server Components (Ubuntu PC: i5-7400 + GTX 1060)

| Component | Technology | Job |
|-----------|-----------|-----|
| **Bot binary** | Go (forked files.md + plugins) | Telegram interface, media storage, sync API |
| **Transcription** | Whisper.cpp on GPU | Audio → text with timestamps |
| **Classification** | Ollama + Qwen 3 0.6B | Category detection + confidence |
| **Extraction** | Ollama + Qwen 3 8B/14B | Structured output, multi-pass |
| **Storage** | Local disk (~/memoire-data/) | Source of truth for all files |
| **Bot API** | Self-hosted Telegram Bot API server | Lifts file size limit from 20 MB to 2 GB |

### Client Surfaces

- **iPhone PWA:** Capture + light review via NordVPN Meshnet
- **Laptop PWA:** Command center for curation via files.md's existing sync API

### Data Directory Structure

```
~/memoire-data/
├── Chat.md                      # Default text dump
├── Review.md                    # AI-extracted items awaiting review (checklist)
├── Later.md                     # Tasks
├── Read.md / Watch.md / Shop.md # Your capture files
├── brain/                       # Ideas (one per file)
├── journal/
│   └── 2026.05 May.md
├── habits/
├── media/
│   ├── voice_msg-abc.ogg
│   ├── glasses_2026-05-21_1823.mp4
│   └── transcripts/             # Raw transcripts (preserved forever)
│       └── 2026-05-21_1823.txt
├── training/
│   └── corrections.jsonl        # Your edits logged for fine-tuning
├── archive/
└── config.json
```

---

## LLM Strategy

### Model Selection

| Role | Model | Size | Why |
|------|-------|------|-----|
| **Classifier** | Qwen 3 0.6B | ~400 MB | Fast; runs in parallel with big model |
| **Extractor** | Qwen 3 8B-Instruct, Q4 | ~5 GB | Best multilingual (Arabic + English), strong JSON output |
| **Extractor (stretch)** | Qwen 3 14B, Q4 | ~9 GB | Use if 8B misses nuance in your data |
| **Critic** | Same as extractor | — | One model, different prompt |
| **Escape hatch** | Claude Haiku via API | — | ~$0.001/message; privacy-preserving opt-in |

**Why Qwen 3:** Better multilingual support than Phi-4 (which is English-skewed) and native Arabic handling. Llama 3.3 is solid, but Qwen leads specifically for mixed-language notes.

### Quality Investments

1. **Multi-pass extraction:** First pass extracts, critic asks "what's missed/misclassified/splittable," final pass refines. Doubles processing time; meaningfully improves quality.
2. **Batch processing:** Instead of one-at-a-time, process the day's messages together. The model sees themes, deduplicates, links related ideas.
3. **JSON schema enforcement:** Ollama forced-JSON mode with up to 3 retries if parsing fails. Failures are observed, never silent.
4. **Per-category prompts:** Classifier picks category → category-specific prompt runs ("you are extracting tasks…"). Sharper output than one mega-prompt.
5. **Original transcripts preserved:** Raw audio always kept under `media/transcripts/`. Re-run extraction anytime without loss.

### Long-Term: Fine-Tuning on Your Corrections

Every time you edit, move, or delete an AI item in the PWA, the before/after is logged to `training/corrections.jsonl`. After ~3 months of real use, you'll have a corpus to fine-tune a 3B model on **your** patterns, vocabulary, and categories. A fine-tuned 3B on your specific task can outperform a general-purpose 14B.

---

## Deployment & Operations

### Quick Deploy (from laptop)

```bash
make build      # GOOS=linux GOARCH=amd64 go build -o memoire ./cmd/server
make deploy     # rsync binary + prompts.yaml to server, restart systemd
make logs       # tail -f server logs over SSH
make rollback   # restore previous binary, restart
make prompts    # rsync prompts.yaml + signal reload (no restart)
```

The server runs under `systemd` with instant restart. Previous binary kept as `memoire.prev` for one-command rollback. Data and config are never touched by deploys.

### Secrets

Stored in `/etc/memoire/.env` on server only (never in git):
```env
TG_BOT_TOKEN=...
TG_BOT_API_URL=http://localhost:8081
OLLAMA_HOST=http://localhost:11434
WHISPER_PATH=/usr/local/bin/whisper-cli
CLAUDE_API_KEY=...  # Optional; only used if fallback to Claude
```

---

## Getting Started

### Phase 0: Foundation (one evening, you + hardware)

- Install Ubuntu Server 24.04 on a spare PC
- Install NVIDIA drivers + CUDA 12.x
- Install Ollama: `ollama pull qwen3:8b qwen3:0.6b`
- Install Whisper.cpp with CUDA support
- Install NordVPN, enable Meshnet, link the PC and your iPhone
- Self-host Telegram Bot API server (Docker image available)
- Verify every piece with `curl` before any application code touches them

### Phase 1: Fork & Run Vanilla (half day)

- Clone this repo to your laptop
- Build and deploy to server
- Get vanilla files.md running under systemd
- Configure Telegram bot, send a test message
- Verify PWA sync from iPhone via Meshnet

### Phase 2: Voice Pipeline (1–2 days)

- Add `voice.go` plugin to intercept Telegram voice messages
- Wire up Whisper.cpp transcription
- Wire up Ollama (dump transcripts to Chat.md, no extraction yet)
- Validate transcription quality on real voice notes

### Phase 3: LLM Extraction (2–3 days)

- Add classify → extract → critic → route pipeline
- Implement Review.md checklist generation
- Implement AI marker comments
- Validate on a week of real messages, tune prompts

### Phase 4: Video Pipeline (1–2 days)

- iOS Shortcut watches the Glasses album, POSTs to server via Meshnet
- `video.go` plugin accepts uploads, extracts audio, runs same pipeline
- Video kept in media/, linked from extracted items

### Phase 5: Quality Investments (ongoing)

- Multi-pass refinement on low-confidence items
- Batch processing every few hours
- Correction logging surface in PWA
- Fine-tuning when corpus is large enough (~3 months)

---

## Key Design Decisions

| Decision | Rationale |
|----------|-----------|
| Fork files.md via Plugin interface | Native, single binary, no orchestration layer |
| Host on extra PC with GPU | Owned hardware, enables local Whisper + LLM, zero per-use costs |
| Ubuntu Server 24.04 LTS | Headless, NVIDIA drivers, low overhead |
| Local storage only | Privacy, simplicity, sovereignty |
| Whisper.cpp on GPU | Zero per-message cost, fast enough for real-time |
| Ollama + Claude escape hatch | Privacy-first with quality safety net |
| LLM routes to target files | Maximum organizational value per message |
| Review.md checklist | Dump-and-walk-away ergonomics, inbox-zero model |
| AI marker comments | Transparent auditability via grep |
| NordVPN Meshnet | Already subscribed, WireGuard, free, no public exposure |
| Self-hosted Telegram Bot API | Lifts file limit to 2 GB (from 20 MB) |
| Multi-pass LLM with critic | Quality wins; latency is fine |

---

## File Structure

```
memoire/
├── cmd/
│   └── server/                  # files.md original entrypoint
├── server/
│   ├── bot.go                   # files.md original
│   ├── sync/                    # files.md original
│   ├── plugins/
│   │   ├── voice.go             # NEW: voice message handler
│   │   ├── video.go             # NEW: video handler
│   │   └── extractor.go         # NEW: LLM pipeline orchestration
│   ├── pipeline/
│   │   ├── transcribe.go        # NEW: Whisper.cpp wrapper
│   │   ├── classify.go          # NEW: category detection
│   │   ├── extract.go           # NEW: structured extraction
│   │   ├── critic.go            # NEW: refinement pass
│   │   ├── route.go             # NEW: write to target files
│   │   └── prompts.yaml         # NEW: hot-reloadable prompts
│   └── llm/
│       ├── interface.go         # NEW: LLM abstraction
│       ├── ollama.go            # NEW: local Ollama client
│       └── claude.go            # NEW: Claude API fallback
├── deploy/
│   ├── Makefile
│   ├── memoire.service
│   └── deploy.sh
└── .env.example
```

---

## Glossary

| Term | Meaning |
|------|---------|
| **Review.md** | Single checklist file listing every AI-extracted item; your daily inbox |
| **AI marker** | HTML comment (`<!-- ai:2026-05-21T18:23 src:msg-abc conf:0.92 -->`) attached to LLM-written content; invisible in render, greppable on disk |
| **Glasses album** | Dedicated iPhone Photos album where Meta AI app auto-saves Ray-Ban recordings |
| **Meshnet** | NordVPN's WireGuard-based P2P mesh; enables iPhone ↔ server from anywhere |
| **Plugin interface** | files.md's extension point; how Memoire adds features without forking the core |
| **Correction log** | `training/corrections.jsonl`; every PWA edit to AI content, becomes fine-tuning data |
| **Critic pass** | Second LLM call that reviews first extraction before final JSON |
| **Whisper.cpp** | Local, GPU-accelerated speech-to-text (no API, zero per-message cost) |
| **Ollama** | Local LLM runtime; hosts Qwen 3 models for classification and extraction |

---

## Scope & Future

### Explicitly out of scope for v1

- **n8n:** No role initially. Go handles everything inline. Revisit if you need visual workflow editing or cross-system integrations (Notion, calendar, webhooks).
- **Backups:** Future — server-side cron rsyncs to external HDD nightly; optional encrypted offsite backup later.
- **Multi-user:** This is for you. Explicitly excluded.
- **Mobile editing UX:** Use files.md's existing PWA. If review-on-the-go becomes painful, revisit then.
- **Glasses live streaming:** Separate research thread. When ready, it can POST audio chunks to the same `/voice` webhook, everything downstream works.

---

## License & Attribution

Memoire is built on [**files.md**](https://github.com/osmoscraft/files.md) by osmoscraft. All new code (plugins, pipeline, LLM layers, deploy tooling) is written as extensions via the Plugin interface, keeping the fork minimal and the core maintainable.

---

*This README is a companion to the system plan. See `memoire-system-plan.md` for the complete specification with architecture diagrams, data flow details, and phase-by-phase build guidance.*
