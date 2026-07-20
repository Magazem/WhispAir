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
	ClaudeKey   string
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
	wg        sync.WaitGroup
}

// New creates a new Server instance
func New(cfg Config) *Server {
	logger := cfg.Logger
	if logger == nil {
		logger = zap.NewNop()
	}

	s := &Server{
		config:  cfg,
		logger:  logger,
		dataDir: cfg.DataDir,
		queue:   make(chan types.QueueItem, 100),
	}

	// Initialize LLM client
	s.llmClient = llm.NewClient(cfg.OllamaHost, cfg.ClaudeKey, logger)

	// Initialize pipeline
	s.pipeline = pipeline.New(pipeline.Config{
		DataDir:     cfg.DataDir,
		Logger:      logger,
		OllamaHost:  cfg.OllamaHost,
		WhisperPath: cfg.WhisperPath,
		LLMClient:   s.llmClient,
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

	s.enqueue(item)

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

	s.enqueue(item)

	c.JSON(http.StatusAccepted, gin.H{"status": "queued", "id": item.ID})
}

// StartPipeline starts the background processing pipeline
func (s *Server) StartPipeline(ctx context.Context) {
	s.logger.Info("starting background pipeline")

	for {
		select {
		case <-ctx.Done():
			s.logger.Info("pipeline shutting down")
			s.wg.Wait()
			return
		case item := <-s.queue:
			s.wg.Add(1)
			go func(item types.QueueItem) {
				defer s.wg.Done()
				s.processItem(ctx, item)
			}(item)
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

	s.logger.Info("item processed",
		zap.String("id", item.ID),
		zap.Int("items_extracted", len(result.Items)),
	)
}

// enqueue adds an item to the processing queue
func (s *Server) enqueue(item types.QueueItem) {
	select {
	case s.queue <- item:
		s.logger.Info("item queued", zap.String("id", item.ID))
	default:
		s.logger.Warn("queue full, dropping item", zap.String("id", item.ID))
	}
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

	s.enqueue(item)
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
	UpdateID int             `json:"update_id"`
	Message  *TelegramMessage `json:"message,omitempty"`
}

// TelegramMessage represents a Telegram message
type TelegramMessage struct {
	MessageID int    `json:"message_id"`
	Date      int    `json:"date"`
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
