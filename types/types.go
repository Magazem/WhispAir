package types

import "time"

// Ensure time is used
var _ = time.Now

// QueueItem represents a message waiting for processing
type QueueItem struct {
	ID        string    `json:"id"`
	Type      string    `json:"type"`   // "text", "voice", "video"
	Source    string    `json:"source"` // "telegram", "ios_shortcut"
	MediaPath string    `json:"media_path,omitempty"`
	Text      string    `json:"text,omitempty"`
	Timestamp time.Time `json:"timestamp"`
	ChatID    int64     `json:"chat_id,omitempty"`
	MessageID int       `json:"message_id,omitempty"`
}

// Classification of a message
type Classification struct {
	Category   string  `json:"category"`
	Confidence float64 `json:"confidence"`
}

// ExtractedItem represents a single extracted piece of content
type ExtractedItem struct {
	Type   string `json:"type"`
	Text   string `json:"text"`
	Target string `json:"target"`
	// Confidence carries the classifier's confidence for this item.
	// Zero means "unknown" and is omitted from AI markers.
	Confidence float64 `json:"confidence"`
}

// PipelineResult from the pipeline processing
type PipelineResult struct {
	RawText        string          `json:"raw_text"`
	Classification Classification  `json:"classification"`
	Items          []ExtractedItem `json:"items"`
	Confidence     float64         `json:"confidence"`
}
