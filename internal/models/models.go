package models

import (
	"encoding/json"
	"time"
)

type Batch struct {
	ID             int64           `json:"id"`
	RawText        string          `json:"raw_text"`
	SourceName     string          `json:"source_name"`
	SourceFile     string          `json:"source_file,omitempty"`
	Status         string          `json:"status"`
	TotalItems     int             `json:"total_items"`
	BuyCount       int             `json:"buy_count"`
	SellCount      int             `json:"sell_count"`
	UnknownCount   int             `json:"unknown_count"`
	DuplicateCount int             `json:"duplicate_count"`
	ErrorCode      string          `json:"error_code,omitempty"`
	ErrorMessage   string          `json:"error_message,omitempty"`
	CreatedAt      time.Time       `json:"created_at"`
	CompletedAt    *time.Time      `json:"completed_at,omitempty"`
	Items          []IngestionItem `json:"items,omitempty"`
}

type BatchSummary struct {
	ID             int64      `json:"id"`
	RawTextPreview string     `json:"raw_text_preview"`
	SourceName     string     `json:"source_name"`
	Status         string     `json:"status"`
	TotalItems     int        `json:"total_items"`
	BuyCount       int        `json:"buy_count"`
	SellCount      int        `json:"sell_count"`
	UnknownCount   int        `json:"unknown_count"`
	DuplicateCount int        `json:"duplicate_count"`
	ErrorCode      string     `json:"error_code,omitempty"`
	ErrorMessage   string     `json:"error_message,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	CompletedAt    *time.Time `json:"completed_at,omitempty"`
}

type IngestionItem struct {
	ID                       int64           `json:"id"`
	BatchID                  int64           `json:"batch_id"`
	ItemIndex                int             `json:"item_index"`
	RawText                  string          `json:"raw_text"`
	DetectedType             string          `json:"detected_type"`
	ClassificationConfidence float64         `json:"classification_confidence"`
	ProcessingStatus         string          `json:"processing_status"`
	ExtractionJSON           json.RawMessage `json:"extraction_json"`
	Title                    string          `json:"title"`
	EntityID                 *int64          `json:"entity_id,omitempty"`
	WasDuplicate             bool            `json:"was_duplicate"`
	CreatedAt                time.Time       `json:"created_at"`
}

type StructuredResponse struct {
	Items []StructuredItem `json:"items"`
}

type StructuredItem struct {
	ItemIndex                int             `json:"item_index"`
	RawText                  string          `json:"raw_text"`
	DetectedType             string          `json:"detected_type"`
	ClassificationConfidence float64         `json:"classification_confidence"`
	Title                    string          `json:"title"`
	StructuredData           json.RawMessage `json:"structured_data"`
}
