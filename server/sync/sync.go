package sync

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// Server handles sync API requests
type Server struct {
	dataDir string
	logger  *zap.Logger
}

// New creates a new sync API server
func New(dataDir string, logger *zap.Logger) *Server {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Server{
		dataDir: dataDir,
		logger:  logger,
	}
}

// cleanPath strips leading whitespace/slashes and resolves the path safely
func (s *Server) resolvePath(urlPath string) (string, error) {
	// Strip leading slashes and whitespace
	urlPath = strings.TrimLeft(urlPath, "/ \t")
	if urlPath == "" {
		return "", fmt.Errorf("empty path")
	}

	// filepath.Clean normalizes the path (converts / to \ on Windows, removes ..)
	cleanPath := filepath.Clean(urlPath)
	fullPath := filepath.Join(s.dataDir, cleanPath)

	// Ensure we're still within dataDir (prevent path traversal)
	if !strings.HasPrefix(fullPath, s.dataDir) {
		return "", fmt.Errorf("path escapes data directory")
	}

	return fullPath, nil
}

// ListFiles returns a list of all files in the data directory
func (s *Server) ListFiles(c *gin.Context) {
	var files []FileInfo

	if err := filepath.Walk(s.dataDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // Skip errors
		}
		if info.IsDir() {
			return nil
		}

		rel, err := filepath.Rel(s.dataDir, path)
		if err != nil {
			return nil
		}

		// Skip hidden directories
		if strings.HasPrefix(rel, ".") || strings.Contains(rel, "\\.") || strings.Contains(rel, "/.") {
			return nil
		}

		// Normalize to forward slashes in response
		rel = strings.ReplaceAll(rel, "\\", "/")

		files = append(files, FileInfo{
			Path:    rel,
			Size:    info.Size(),
			ModTime: info.ModTime(),
		})

		return nil
	}); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"files": files})
}

// GetFile returns the contents of a file
func (s *Server) GetFile(c *gin.Context) {
	filePath := c.Param("path")
	if filePath == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "path required"})
		return
	}

	fullPath, err := s.resolvePath(filePath)
	if err != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
		return
	}

	content, err := os.ReadFile(fullPath)
	if err != nil {
		if os.IsNotExist(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": "file not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.Header("Content-Type", "text/markdown; charset=utf-8")
	c.String(http.StatusOK, string(content))
}

// PutFile writes content to a file (full replacement)
func (s *Server) PutFile(c *gin.Context) {
	filePath := c.Param("path")
	if filePath == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "path required"})
		return
	}

	fullPath, err := s.resolvePath(filePath)
	if err != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
		return
	}

	if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	body, err := c.GetRawData()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return
	}

	if err := os.WriteFile(fullPath, body, 0644); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// AppendFile appends content to a file
func (s *Server) AppendFile(c *gin.Context) {
	filePath := c.Param("path")
	if filePath == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "path required"})
		return
	}

	fullPath, err := s.resolvePath(filePath)
	if err != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
		return
	}

	if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	body, err := c.GetRawData()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return
	}

	f, err := os.OpenFile(fullPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer f.Close()

	if _, err := f.Write(body); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// DeleteFile deletes a file
func (s *Server) DeleteFile(c *gin.Context) {
	filePath := c.Param("path")
	if filePath == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "path required"})
		return
	}

	fullPath, err := s.resolvePath(filePath)
	if err != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
		return
	}

	if err := os.Remove(fullPath); err != nil {
		if os.IsNotExist(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": "file not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"ok": true, "deleted": true})
}

// LogCorrection logs an edit/correction made to AI content (for fine-tuning)
func (s *Server) LogCorrection(c *gin.Context) {
	var correction struct {
		Original  string `json:"original" binding:"required"`
		Corrected string `json:"corrected" binding:"required"`
		FilePath  string `json:"file_path" binding:"required"`
		Timestamp string `json:"timestamp"`
	}

	if err := c.BindJSON(&correction); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if correction.Timestamp == "" {
		correction.Timestamp = time.Now().UTC().Format(time.RFC3339)
	}

	trainingDir := filepath.Join(s.dataDir, "training")
	os.MkdirAll(trainingDir, 0755)

	correctionsFile := filepath.Join(trainingDir, "corrections.jsonl")
	f, err := os.OpenFile(correctionsFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer f.Close()

	line := fmt.Sprintf(`{"timestamp":"%s","file_path":"%s","original":%q,"corrected":%q}`+"\n",
		correction.Timestamp, correction.FilePath,
		correction.Original, correction.Corrected)

	if _, err := f.WriteString(line); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// FileInfo represents a file in the system
type FileInfo struct {
	Path    string    `json:"path"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"mod_time"`
}
