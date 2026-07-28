package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/Magazem/WhispAir/server/llm"
	"github.com/Magazem/WhispAir/server/pipeline"
	"github.com/Magazem/WhispAir/server/plugins"
	"github.com/Magazem/WhispAir/server/queue"
	"github.com/Magazem/WhispAir/types"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// Config holds all server configuration
type Config struct {
	DataDir     string
	Logger      *zap.Logger
	BotToken    string
	BotAPIURL   string
	OllamaHost  string
	WhisperPath string
	// WhisperModel is the path to the ggml model file. Empty falls back to
	// $WHISPER_MODEL and then conventional locations.
	WhisperModel string
	ClaudeKey    string
	// AllowMock permits mock transcription and mock LLM output. It MUST
	// default to false: mock output is indistinguishable from real captured
	// thought once it reaches the notes.
	AllowMock bool
	// Workers is the number of concurrent pipeline workers. Defaults to 1.
	// The target hardware holds a single 8B model in 6GB of VRAM, so more
	// than one concurrent extract means GPU thrash or OOM. The project's
	// stated priority is correctness over latency.
	Workers int
}

// Server is the main memoire server
type Server struct {
	config    Config
	logger    *zap.Logger
	dataDir   string
	plugins   []plugins.Plugin
	pipeline  *pipeline.Pipeline
	llmClient llm.Client
	queue     chan types.QueueItem
	durable   *queue.Queue
	wg        sync.WaitGroup
}

// New creates a new Server instance
func New(cfg Config) *Server {
	logger := cfg.Logger
	if logger == nil {
		logger = zap.NewNop()
	}

	if cfg.Workers < 1 {
		cfg.Workers = 1
	}

	s := &Server{
		config:  cfg,
		logger:  logger,
		dataDir: cfg.DataDir,
		queue:   make(chan types.QueueItem, 100),
	}

	// Durable queue. If it cannot be created we log loudly and continue with
	// the in-memory channel only, rather than refusing to accept messages.
	durable, err := queue.New(cfg.DataDir, logger)
	if err != nil {
		logger.Error("failed to initialise durable queue; items will not survive restart",
			zap.Error(err))
	} else {
		s.durable = durable
	}

	// Initialize LLM client. Use the options constructor so AllowMock is
	// explicit rather than defaulted.
	s.llmClient = llm.NewClientWithOptions(llm.Config{
		OllamaHost: cfg.OllamaHost,
		ClaudeKey:  cfg.ClaudeKey,
		Logger:     logger,
		AllowMock:  cfg.AllowMock,
	})

	// Initialize pipeline
	s.pipeline = pipeline.New(pipeline.Config{
		DataDir:      cfg.DataDir,
		Logger:       logger,
		OllamaHost:   cfg.OllamaHost,
		WhisperPath:  cfg.WhisperPath,
		WhisperModel: cfg.WhisperModel,
		AllowMock:    cfg.AllowMock,
		LLMClient:    s.llmClient,
	})

	return s
}

// Logger returns the server's logger
func (s *Server) Logger() *zap.Logger {
	return s.logger
}

// RegisterPlugin adds a plugin to the server
func (s *Server) RegisterPlugin(p plugins.Plugin) {
	s.plugins = append(s.plugins, p)
}

// HandleTelegramWebhook processes incoming Telegram updates
func (s *Server) HandleTelegramWebhook(c *gin.Context) {
	var update TelegramUpdate
	if err := c.BindJSON(&update); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid update"})
		return
	}

	// Process message
	if update.Message != nil {
		s.handleTelegramMessage(update.Message)
	}

	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// HandleVoiceUpload handles direct voice uploads (from iOS shortcut)
func (s *Server) HandleVoiceUpload(c *gin.Context) {
	file, header, err := c.Request.FormFile("audio")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "no audio file"})
		return
	}
	defer file.Close()

	// Save to media directory
	mediaDir := filepath.Join(s.dataDir, "media")
	os.MkdirAll(mediaDir, 0755)

	filename := fmt.Sprintf("upload_%d_%s", time.Now().Unix(), header.Filename)
	mediaPath := filepath.Join(mediaDir, filename)

	out, err := os.Create(mediaPath)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save file"})
		return
	}
	defer out.Close()

	if _, err := io.Copy(out, file); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to write file"})
		return
	}

	// Queue for processing
	item := types.QueueItem{
		ID:        fmt.Sprintf("voice_%d", time.Now().UnixNano()),
		Type:      "voice",
		Source:    "ios_shortcut",
		MediaPath: mediaPath,
		Timestamp: time.Now(),
	}

	if err := s.enqueue(item); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to queue item"})
		return
	}

	c.JSON(http.StatusAccepted, gin.H{"status": "queued", "id": item.ID})
}

// HandleVideoUpload handles video uploads
func (s *Server) HandleVideoUpload(c *gin.Context) {
	file, header, err := c.Request.FormFile("video")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "no video file"})
		return
	}
	defer file.Close()

	// Save to media directory
	mediaDir := filepath.Join(s.dataDir, "media")
	os.MkdirAll(mediaDir, 0755)

	filename := fmt.Sprintf("video_%d_%s", time.Now().Unix(), header.Filename)
	mediaPath := filepath.Join(mediaDir, filename)

	out, err := os.Create(mediaPath)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save file"})
		return
	}
	defer out.Close()

	if _, err := io.Copy(out, file); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to write file"})
		return
	}

	// Queue for processing
	item := types.QueueItem{
		ID:        fmt.Sprintf("video_%d", time.Now().UnixNano()),
		Type:      "video",
		Source:    c.PostForm("source"),
		MediaPath: mediaPath,
		Timestamp: time.Now(),
	}

	if item.Source == "" {
		item.Source = "api"
	}

	if err := s.enqueue(item); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to queue item"})
		return
	}

	c.JSON(http.StatusAccepted, gin.H{"status": "queued", "id": item.ID})
}

// StartPipeline starts the background processing pipeline.
//
// Work runs on a fixed pool of Workers goroutines (default 1) instead of one
// unbounded goroutine per item. Each item triggers an 8B extract plus an 8B
// critic pass, and the target GPU holds exactly one such model, so unbounded
// fan-out meant thrash or OOM under any burst.
func (s *Server) StartPipeline(ctx context.Context) {
	workers := s.config.Workers
	if workers < 1 {
		workers = 1
	}
	s.logger.Info("starting background pipeline", zap.Int("workers", workers))

	// Resume anything left pending by a previous run before taking new work.
	s.replayPending()

	for i := 0; i < workers; i++ {
		s.wg.Add(1)
		go func(worker int) {
			defer s.wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case item := <-s.queue:
					s.logger.Debug("worker picked up item",
						zap.Int("worker", worker), zap.String("id", item.ID))
					s.processItem(ctx, item)
				}
			}
		}(i)
	}

	<-ctx.Done()
	s.logger.Info("pipeline shutting down; draining in-flight work")
	s.wg.Wait()
	s.logger.Info("pipeline drained")
}

// replayPending re-queues every item the durable queue still lists as pending.
func (s *Server) replayPending() {
	if s.durable == nil {
		return
	}
	items, err := s.durable.Replay()
	if err != nil {
		s.logger.Error("failed to replay persisted queue", zap.Error(err))
		return
	}
	for _, item := range items {
		select {
		case s.queue <- item:
			s.logger.Info("replayed queue item", zap.String("id", item.ID))
		default:
			// Channel full: it stays on disk and will be picked up by the
			// next replay. Nothing is lost.
			s.logger.Warn("replay deferred, queue full; item remains persisted",
				zap.String("id", item.ID))
		}
	}
}

// processItem runs the full pipeline on a queue item
func (s *Server) processItem(ctx context.Context, item types.QueueItem) {
	s.logger.Info("processing item",
		zap.String("id", item.ID),
		zap.String("type", item.Type),
		zap.String("source", item.Source),
	)

	// Let plugins handle pre-processing
	for _, p := range s.plugins {
		if p.CanHandle(item.Type) {
			if err := p.PreProcess(&item); err != nil {
				s.logger.Error("plugin pre-process error",
					zap.String("plugin", p.Name()),
					zap.Error(err),
				)
			}
		}
	}

	// Run the pipeline
	result, err := s.pipeline.Process(ctx, item)
	if err != nil {
		s.logger.Error("pipeline error",
			zap.String("id", item.ID),
			zap.Error(err),
		)
		// Record the failure durably. The media file and the failure record
		// both survive, so the item can be retried once the cause is fixed.
		if s.durable != nil {
			if ferr := s.durable.Fail(item, err); ferr != nil {
				s.logger.Error("failed to record queue failure",
					zap.String("id", item.ID), zap.Error(ferr))
			}
		}
		return
	}

	// Let plugins handle post-processing
	for _, p := range s.plugins {
		if p.CanHandle(item.Type) {
			if err := p.PostProcess(&item, result); err != nil {
				s.logger.Error("plugin post-process error",
					zap.String("plugin", p.Name()),
					zap.Error(err),
				)
			}
		}
	}

	if s.durable != nil {
		if cerr := s.durable.Complete(item); cerr != nil {
			s.logger.Warn("failed to mark queue item complete",
				zap.String("id", item.ID), zap.Error(cerr))
		}
	}

	s.logger.Info("item processed",
		zap.String("id", item.ID),
		zap.Int("items_extracted", len(result.Items)),
	)
}

// enqueue accepts an item for processing.
//
// The item is persisted to disk BEFORE this returns, so it survives a crash or
// restart. It is never dropped: if the in-memory channel is full the item stays
// on disk and is picked up by the next replay. The previous implementation
// discarded items on a full channel with only a warning.
func (s *Server) enqueue(item types.QueueItem) error {
	if s.durable != nil {
		if err := s.durable.Persist(item); err != nil {
			s.logger.Error("failed to persist queue item; refusing to accept",
				zap.String("id", item.ID), zap.Error(err))
			return err
		}
	}

	select {
	case s.queue <- item:
		s.logger.Info("item queued", zap.String("id", item.ID))
	default:
		s.logger.Warn("in-memory queue full; item persisted and will be replayed",
			zap.String("id", item.ID))
	}
	return nil
}

// handleTelegramMessage processes a Telegram message
func (s *Server) handleTelegramMessage(msg *TelegramMessage) {
	item := types.QueueItem{
		ID:        fmt.Sprintf("tg_%d_%d", msg.Chat.ID, msg.MessageID),
		Source:    "telegram",
		Timestamp: time.Unix(int64(msg.Date), 0),
		ChatID:    msg.Chat.ID,
		MessageID: msg.MessageID,
	}

	// Determine message type
	switch {
	case msg.Text != "":
		item.Type = "text"
		item.Text = msg.Text

	case msg.Voice != nil:
		item.Type = "voice"
		// Download voice file
		if path, err := s.downloadTelegramFile(msg.Voice.FileID, "ogg"); err == nil {
			item.MediaPath = path
		} else {
			s.logger.Error("failed to download voice", zap.Error(err))
			return
		}

	case msg.Video != nil:
		item.Type = "video"
		if path, err := s.downloadTelegramFile(msg.Video.FileID, "mp4"); err == nil {
			item.MediaPath = path
		} else {
			s.logger.Error("failed to download video", zap.Error(err))
			return
		}

	default:
		s.logger.Debug("unsupported message type", zap.Int("message_id", msg.MessageID))
		return
	}

	if err := s.enqueue(item); err != nil {
		s.logger.Error("failed to queue telegram message",
			zap.String("id", item.ID), zap.Error(err))
	}
}

// downloadTelegramFile downloads a file from Telegram
func (s *Server) downloadTelegramFile(fileID string, ext string) (string, error) {
	// Get file path from Telegram API
	url := fmt.Sprintf("%s/bot%s/getFile?file_id=%s", s.config.BotAPIURL, s.config.BotToken, fileID)

	resp, err := http.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var result struct {
		OK     bool `json:"ok"`
		Result struct {
			FilePath string `json:"file_path"`
		} `json:"result"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}

	if !result.OK {
		return "", fmt.Errorf("telegram API error")
	}

	// Download the file
	fileURL := fmt.Sprintf("%s/file/bot%s/%s", s.config.BotAPIURL, s.config.BotToken, result.Result.FilePath)
	resp, err = http.Get(fileURL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	// Save to media directory
	mediaDir := filepath.Join(s.dataDir, "media")
	os.MkdirAll(mediaDir, 0755)

	filename := fmt.Sprintf("tg_%s.%s", fileID, ext)
	path := filepath.Join(mediaDir, filename)

	out, err := os.Create(path)
	if err != nil {
		return "", err
	}
	defer out.Close()

	if _, err := io.Copy(out, resp.Body); err != nil {
		return "", err
	}

	return path, nil
}

// TelegramUpdate represents a Telegram webhook update
type TelegramUpdate struct {
	UpdateID int              `json:"update_id"`
	Message  *TelegramMessage `json:"message,omitempty"`
}

// TelegramMessage represents a Telegram message
type TelegramMessage struct {
	MessageID int `json:"message_id"`
	Date      int `json:"date"`
	Chat      struct {
		ID int64 `json:"id"`
	} `json:"chat"`
	Text  string `json:"text,omitempty"`
	Voice *struct {
		FileID string `json:"file_id"`
	} `json:"voice,omitempty"`
	Video *struct {
		FileID string `json:"file_id"`
	} `json:"video,omitempty"`
}
