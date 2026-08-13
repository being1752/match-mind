package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"matchmind/internal/models"
)

type Client struct {
	baseURL string
	http    *http.Client
}

func NewClient(baseURL string, timeout time.Duration) *Client {
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), http: &http.Client{Timeout: timeout}}
}

func (c *Client) Structure(ctx context.Context, content string) (models.StructuredResponse, string, error) {
	body, _ := json.Marshal(map[string]string{"content": content})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/structure", bytes.NewReader(body))
	if err != nil {
		return models.StructuredResponse{}, "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return models.StructuredResponse{}, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		var problem struct {
			Detail any `json:"detail"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&problem)
		if problem.Detail != nil {
			return models.StructuredResponse{}, "", fmt.Errorf("AI service returned %s: %v", resp.Status, problem.Detail)
		}
		return models.StructuredResponse{}, "", fmt.Errorf("AI service returned %s", resp.Status)
	}
	var envelope struct {
		Items     []models.StructuredItem `json:"items"`
		ModelName string                  `json:"model_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return models.StructuredResponse{}, "", err
	}
	if len(envelope.Items) == 0 {
		return models.StructuredResponse{}, "", fmt.Errorf("AI service returned no items")
	}
	return models.StructuredResponse{Items: envelope.Items}, envelope.ModelName, nil
}

type EmbeddingResponse struct {
	Vectors    [][]float32 `json:"vectors"`
	ModelName  string      `json:"model_name"`
	Dimensions int         `json:"dimensions"`
}

func (c *Client) Embed(ctx context.Context, texts []string) (EmbeddingResponse, error) {
	body, err := json.Marshal(map[string]any{"texts": texts})
	if err != nil {
		return EmbeddingResponse{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/embeddings", bytes.NewReader(body))
	if err != nil {
		return EmbeddingResponse{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return EmbeddingResponse{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		var problem struct {
			Detail string `json:"detail"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&problem)
		return EmbeddingResponse{}, fmt.Errorf("embedding service returned %s: %s", resp.Status, problem.Detail)
	}
	var result EmbeddingResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return EmbeddingResponse{}, err
	}
	if len(result.Vectors) != len(texts) || result.Dimensions != 1024 {
		return EmbeddingResponse{}, fmt.Errorf("invalid embedding response")
	}
	return result, nil
}
