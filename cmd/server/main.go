package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/Magazem/WhispAir/server"
	"github.com/Magazem/WhispAir/server/plugins"
	"github.com/Magazem/WhispAir/server/sync"
	"github.com/gin-gonic/gin"
	"github.com/spf13/viper"
	"go.uber.org/zap"
)

func main() {
	// Load configuration
	viper.SetConfigName("config")
	viper.SetConfigType("json")
	viper.AddConfigPath("/etc/memoire")
	viper.AddConfigPath("$HOME/.memoire")
	viper.AddConfigPath(".")

	// Environment variables override
	viper.SetEnvPrefix("MEMOIRE")
	viper.AutomaticEnv()

	// Defaults
	viper.SetDefault("server.port", "8080")
	viper.SetDefault("server.host", "0.0.0.0")
	viper.SetDefault("data.dir", "$HOME/memoire-data")
	viper.SetDefault("log.level", "info")
	viper.SetDefault("ollama.host", "http://localhost:11434")
	viper.SetDefault("tg.bot_api_url", "http://localhost:8081")
	// One pipeline worker by default: the target GPU holds a single 8B model.
	viper.SetDefault("pipeline.workers", 1)
	// Mocks are off unless explicitly enabled. Mock output is
	// indistinguishable from real captured thought once it reaches the notes.
	viper.SetDefault("mock.enabled", false)

	// Load .env if present
	if _, err := os.Stat(".env"); err == nil {
		viper.SetConfigFile(".env")
		viper.SetConfigType("env")
		_ = viper.MergeInConfig()
	}

	// Try to read config file (JSON)
	if err := viper.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			fmt.Fprintf(os.Stderr, "Config error: %v\n", err)
		}
	}

	// Initialize logger
	logLevel := viper.GetString("log.level")
	var zapLevel zap.AtomicLevel
	if err := zapLevel.UnmarshalText([]byte(logLevel)); err != nil {
		zapLevel = zap.NewAtomicLevelAt(zap.InfoLevel)
	}

	zapConfig := zap.NewProductionConfig()
	zapConfig.Level = zapLevel
	logger, err := zapConfig.Build()
	if err != nil {
		panic(fmt.Sprintf("failed to initialize logger: %v", err))
	}
	defer logger.Sync()

	// Resolve and create data directory.
	//
	// Viper does not expand shell variables and filepath.Abs only resolves
	// against the CWD, so the old code turned the "$HOME/memoire-data" default
	// into a directory LITERALLY NAMED "$HOME". That is how 11 personal notes
	// ended up committed into the repository.
	dataDir, err := expandHome(viper.GetString("data.dir"))
	if err != nil {
		logger.Fatal("failed to resolve data dir", zap.Error(err))
	}
	dataDir, err = filepath.Abs(dataDir)
	if err != nil {
		logger.Fatal("failed to resolve data dir", zap.Error(err))
	}

	// Warn if the artifact of the old bug is still lying around, so its
	// contents are not silently orphaned.
	if st, statErr := os.Stat("$HOME"); statErr == nil && st.IsDir() {
		logger.Warn("found a literal '$HOME' directory left by an earlier bug; "+
			"your notes may be in there rather than in the real data dir",
			zap.String("stray", mustAbs("$HOME")),
			zap.String("real_data_dir", dataDir),
		)
	}

	subdirs := []string{
		"media", "media/transcripts", "brain", "journal",
		"habits", "training", "archive",
	}
	for _, sd := range subdirs {
		os.MkdirAll(filepath.Join(dataDir, sd), 0755)
	}

	// Create initial files
	ensureFile(filepath.Join(dataDir, "Chat.md"), "# Chat\n\n")
	ensureFile(filepath.Join(dataDir, "Review.md"), "# Review\n\nAI-extracted items awaiting review. Empty = inbox zero.\n\n")
	ensureFile(filepath.Join(dataDir, "Later.md"), "# Later\n\nTasks and actionable items.\n\n")
	ensureFile(filepath.Join(dataDir, "Read.md"), "# Read\n\n")
	ensureFile(filepath.Join(dataDir, "Watch.md"), "# Watch\n\n")
	ensureFile(filepath.Join(dataDir, "Shop.md"), "# Shop\n\n")

	logger.Info("starting memoire server",
		zap.String("version", "0.1.0"),
		zap.String("host", viper.GetString("server.host")),
		zap.String("port", viper.GetString("server.port")),
		zap.String("data_dir", dataDir),
	)

	// Mock mode must be opted into explicitly, and must be impossible to miss
	// in the logs when it is on.
	allowMock := viper.GetBool("mock.enabled") || os.Getenv("MEMOIRE_MOCK") == "1"
	if allowMock {
		logger.Warn("MOCK MODE ENABLED - transcription and LLM output are FABRICATED " +
			"and will be written to your notes; do not use this for real capture")
	}

	whisperModel := viper.GetString("whisper.model")
	if whisperModel == "" {
		whisperModel = os.Getenv("WHISPER_MODEL")
	}

	// Initialize server
	srv := server.New(server.Config{
		DataDir:      dataDir,
		Logger:       logger,
		BotToken:     viper.GetString("tg.bot_token"),
		BotAPIURL:    viper.GetString("tg.bot_api_url"),
		OllamaHost:   viper.GetString("ollama.host"),
		WhisperPath:  viper.GetString("whisper.path"),
		WhisperModel: whisperModel,
		ClaudeKey:    viper.GetString("claude.api_key"),
		AllowMock:    allowMock,
		Workers:      viper.GetInt("pipeline.workers"),
	})

	// Register plugins
	plugins.RegisterPlugins(srv)

	// Initialize sync API
	syncAPI := sync.New(dataDir, logger)

	// Setup HTTP router
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(gin.Recovery())
	router.Use(loggingMiddleware(logger))

	// CORS middleware for PWA access
	router.Use(corsMiddleware())

	// Health check
	router.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok", "version": "0.1.0"})
	})

	// Telegram webhook
	router.POST("/webhook/telegram", srv.HandleTelegramWebhook)

	// Voice upload endpoint (for iOS shortcut)
	router.POST("/voice", srv.HandleVoiceUpload)

	// Video upload endpoint
	router.POST("/video", srv.HandleVideoUpload)

	// Sync API routes
	syncGroup := router.Group("/sync")
	{
		syncGroup.GET("/files", syncAPI.ListFiles)
		syncGroup.GET("/file/*path", syncAPI.GetFile)
		syncGroup.PUT("/file/*path", syncAPI.PutFile)
		syncGroup.POST("/file/*path", syncAPI.AppendFile)
		syncGroup.DELETE("/file/*path", syncAPI.DeleteFile)

		// Correction logging endpoint
		syncGroup.POST("/correction", syncAPI.LogCorrection)
	}

	// Start background pipeline
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go srv.StartPipeline(ctx)

	// Start HTTP server
	addr := fmt.Sprintf("%s:%s",
		viper.GetString("server.host"),
		viper.GetString("server.port"),
	)
	httpServer := &http.Server{
		Addr:    addr,
		Handler: router,
	}

	go func() {
		logger.Info("listening", zap.String("addr", addr))
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Fatal("server failed", zap.Error(err))
		}
	}()

	// Wait for shutdown signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	logger.Info("shutting down...")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		logger.Error("server shutdown error", zap.Error(err))
	}

	logger.Info("server stopped")
}

func loggingMiddleware(logger *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path

		c.Next()

		latency := time.Since(start)
		logger.Info("request",
			zap.String("method", c.Request.Method),
			zap.String("path", path),
			zap.Int("status", c.Writer.Status()),
			zap.Duration("latency", latency),
		)
	}
}

func corsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "*")
		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}

		c.Next()
	}
}

// expandHome expands a leading "$HOME", "${HOME}" or "~" in a path.
//
// This is the fix for the bug that created a directory literally named
// "$HOME": neither viper nor filepath.Abs performs this expansion.
func expandHome(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("empty path")
	}

	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		if h := os.Getenv("HOME"); h != "" {
			home = h
		} else if h := os.Getenv("USERPROFILE"); h != "" {
			home = h
		} else {
			return "", fmt.Errorf("cannot resolve home directory: %w", err)
		}
	}

	for _, prefix := range []string{"$HOME", "${HOME}", "~"} {
		if path == prefix {
			return home, nil
		}
		if strings.HasPrefix(path, prefix+"/") || strings.HasPrefix(path, prefix+"\\") {
			return filepath.Join(home, path[len(prefix)+1:]), nil
		}
	}

	// Expand any remaining environment variables (e.g. $XDG_DATA_HOME).
	return os.ExpandEnv(path), nil
}

func mustAbs(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return p
}

func ensureFile(path, header string) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		os.WriteFile(path, []byte(header), 0644)
	}
}
