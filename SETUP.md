# Memoire Setup Guide

Environment-by-environment setup instructions.
All installs here are **user-space** (no admin required).

---

## 1. Server (Ubuntu 24.04 LTS — i5-7400 + GTX 1060)

This is where the memoire binary runs 24/7. It's the heart of the system.

### Step 1: OS & Drivers

```bash
# Install Ubuntu Server 24.04 LTS
# Enable SSH during install so you can work from your laptop

# After first boot:
sudo apt update && sudo apt upgrade -y

# NVIDIA drivers (required for Whisper.cpp on GPU)
sudo apt install -y nvidia-driver-535 nvidia-utils-535
sudo reboot

# Verify
nvidia-smi  # Should show your GTX 1060

# CUDA (comes with drivers on Ubuntu 24.04, but check)
nvcc --version || echo "CUTI not in PATH — usually at /usr/local/cuda/bin/nvcc"
```

### Step 2: Go (user-space)

```bash
# Download Go (user-space install to ~/.local)
mkdir -p ~/.local
cd /tmp
wget https://go.dev/dl/go1.22.5.linux-amd64.tar.gz
tar -C ~/.local -xzf go1.22.5.linux-amd64.tar.gz

# Add to ~/.bashrc
echo 'export PATH=$HOME/.local/go/bin:$PATH' >> ~/.bashrc
source ~/.bashrc

# Verify
go version
```

### Step 3: Ollama (user-space)

```bash
# Download Ollama to ~/.local/bin
mkdir -p ~/.local/bin
cd ~/.local/bin
curl -fsSL https://ollama.com/install.sh | sh

# Start Ollama server (background)
ollama serve &

# Pull models
ollama pull qwen3:0.6b
ollama pull qwen3:8b

# Verify
curl http://localhost:11434/api/tags
```

### Step 4: Whisper.cpp (user-space)

```bash
# Clone whisper.cpp
cd ~
git clone https://github.com/ggerganov/whisper.cpp.git
cd whisper.cpp

# Build with CUDA
mkdir build && cd build
cmake -DGGML_CUDA=1 ..
make -j$(nproc)

# Download model
cd ..
bash ./models/download-ggml-model.sh medium

# Link binary to PATH
ln -sf ~/whisper.cpp/build/bin/whisper-cli ~/.local/bin/whisper

# Verify
whisper --help
```

### Step 5: Build and Deploy Memoire

```bash
# Clone your fork
git clone https://github.com/Magazem/WhispAir.git ~/memoire
cd ~/memoire

# Build
go build -o memoire ./cmd/server

# Create data directory
mkdir -p ~/memoire-data

# Create secrets
sudo mkdir -p /etc/memoire
sudo tee /etc/memoire/.env << 'EOF'
TG_BOT_TOKEN=your_bot_token_here
TG_BOT_API_URL=http://localhost:8081
OLLAMA_HOST=http://localhost:11434
WHISPER_PATH=/home/memoire/.local/bin/whisper
CLAUDE_API_KEY=
EOF
sudo chmod 600 /etc/memoire/.env
```

### Step 6: systemd Service (no admin — user systemd)

```bash
# Create user-level systemd directory
mkdir -p ~/.config/systemd/user

# Copy and edit service file
cp deploy/memoire.service ~/.config/systemd/user/
# Edit WorkingDirectory and ExecStart to use your paths

# Enable and start
systemctl --user daemon-reload
systemctl --user enable memoire
systemctl --user start memoire

# View logs
journalctl --user -u memoire -f
```

### Step 7: Telegram Bot

```bash
# 1. Create bot via @BotFather on Telegram
# 2. Copy the token to /etc/memoire/.env
# 3. Set webhook (after server is running):
curl -X POST https://api.telegram.org/bot<TOKEN>/setWebhook \
  -d "http://your-server-ip:8080/webhook/telegram"
```

### Step 8: Self-hosted Bot API (optional — lifts 20MB limit)

```bash
# Run local Bot API server for large files
# Official Docker image from Telegram:
docker run -d --name telegram-bot-api \
  -p 8081:8081 \
  -e TELEGRAM_API_ID=your_api_id \
  -e TELEGRAM_API_HASH=your_api_hash \
  aiogram/telegram-bot-api:latest
```

---

## 2. Laptop (Windows 10+)

For development, building, and reviewing via PWA.

### Step 1: Go

```powershell
# Using winget (no admin needed for user-space install)
winget install --id GoLang.Go --accept-source-agreements --accept-package-agreements

# Verify
go version
```

### Step 2: Build & Cross-compile

```powershell
# In the WhispAir directory

# Windows build (for local testing)
go build -o memoire.exe ./cmd/server

# Linux build (for deploying to server)
$env:GOOS = "linux"
$env:GOARCH = "amd64"
go build -o memoire ./cmd/server
```

### Step 3: Deploy to Server

```powershell
# Using rsync (from WSL/Git Bash)
rsync -avz memoire server/ myserver@192.168.1.100:/opt/memoire/

# Or using SCP
scp memoire server/ myserver@192.168.1.100:/opt/memoire/

# Restart on server
ssh myserver@192.168.1.100 "sudo systemctl restart memoire"
```

### Step 4: Make Deployments Easy

```bash
# On laptop, edit deploy/deploy.sh to set your server IP
# Then just:
make build && make deploy
```

---

## 3. iPhone (Capture + Light Review)

### Step 1: PWA (no App Store install)

1. Open Safari on iPhone
2. Navigate to your server's Meshnet URL (e.g., `http://100.x.x.x:8080`)
3. Tap Share → Add to Home Screen
4. The PWA works offline and syncs when on the same Meshnet

### Step 2: iOS Shortcut (Ray-Ban Video Upload)

Create a Shortcut that watches the "Glasses" album and uploads new videos:

1. Open Shortcuts app
2. Create new Shortcut:
   - **Trigger**: When photo is added to "Glasses" album
   - **Action**: Get File from Input
   - **Action**: URL (your server via Meshnet, e.g., `http://100.x.x.x:8080/video`)
   - **Action**: Get Contents of URL
     - Method: POST
     - Request Body: Form
     - File: Shortcut Input
     - Field name: `video`
3. Add to Automation: Run immediately when photo added

### Step 3: Voice Capture via Telegram

1. Send voice notes directly to your Telegram bot
2. The bot transcribes and extracts items automatically
3. No app install needed beyond Telegram

---

## 4. NordVPN Meshnet (Connecting Everything)

### Setup

1. Install NordVPN on all devices (server, laptop, iPhone)
2. Enable Meshnet in NordVPN settings
3. Link devices via NordVPN's Meshnet portal
4. Note the Meshnet IP for each device (100.x.x.x format)

### Usage

- Server Meshnet IP: `100.x.x.x` — use this from laptop PWA and iPhone shortcut
- Ports: 8080 (memoire), 8081 (Telegram Bot API)
- Meshnet works over WireGuard — traffic stays private

---

## Quick Reference

| Component | Port | Protocol | Purpose |
|-----------|------|----------|---------|
| Memoire HTTP | 8080 | HTTP | Webhook + Sync API |
| Ollama | 11434 | HTTP | Local LLM |
| Telegram Bot API | 8081 | HTTP | Lifts file limit |

### Common Commands

```bash
# Server
systemctl --user restart memoire
journalctl --user -u memoire -f

# Test locally
curl -s http://localhost:8080/health
curl -s -X POST http://localhost:8080/webhook/telegram \
  -d '{"update_id":1,"message":{"message_id":1,"date":1716300000,"chat":{"id":1},"text":"test"}}'

# View data
ls -la ~/memoire-data/
cat ~/memoire-data/Review.md
```

---

## Troubleshooting

**Symptom**: `dial tcp [::1]:11434: connect` in logs
**Cause**: Ollama not running on server
**Fix**: `ollama serve &` or `systemctl --user start ollama`

**Symptom**: Whisper.cpp not found
**Cause**: Binary not in PATH or not built
**Fix**: `which whisper` or build via CMake with CUDA support

**Symptom**: Telegram messages don't arrive
**Cause**: Webhook not set or wrong token
**Fix**: Call `setWebhook` again and check token in `/etc/memoire/.env`

**Symptom**: AI markers not visible in rendered markdown
**Cause**: Expected behavior — markers are `<!-- -->` HTML comments
**Fix**: Use `grep -r "<!-- ai:" ~/memoire-data/` to audit
