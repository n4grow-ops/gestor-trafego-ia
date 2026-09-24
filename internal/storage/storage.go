// Package storage guarda arquivos no Supabase Storage (bucket privado), com a chave de serviço.
package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Client struct {
	URL  string
	Key  string // chave service_role: só no servidor
	HTTP *http.Client
}

func New(url, key string) *Client {
	return &Client{URL: strings.TrimRight(url, "/"), Key: key, HTTP: &http.Client{Timeout: 2 * time.Minute}}
}

// EnsureBucket cria o bucket privado se ele ainda não existir.
func (c *Client) EnsureBucket(ctx context.Context, bucket string) error {
	body, _ := json.Marshal(map[string]any{"id": bucket, "name": bucket, "public": false})
	res, err := c.do(ctx, http.MethodPost, "/storage/v1/bucket", "application/json", body)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	msg, _ := io.ReadAll(io.LimitReader(res.Body, 2048))
	if res.StatusCode/100 == 2 || strings.Contains(strings.ToLower(string(msg)), "already exists") || strings.Contains(string(msg), "Duplicate") {
		return nil
	}
	return fmt.Errorf("storage: criar bucket: status %d: %s", res.StatusCode, strings.TrimSpace(string(msg)))
}

// Upload grava o arquivo no caminho dado dentro do bucket.
func (c *Client) Upload(ctx context.Context, bucket, path, contentType string, data []byte) error {
	res, err := c.do(ctx, http.MethodPost, "/storage/v1/object/"+bucket+"/"+path, contentType, data)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode/100 != 2 {
		msg, _ := io.ReadAll(io.LimitReader(res.Body, 2048))
		return fmt.Errorf("storage: upload: status %d: %s", res.StatusCode, strings.TrimSpace(string(msg)))
	}
	io.Copy(io.Discard, io.LimitReader(res.Body, 1<<20))
	return nil
}

func (c *Client) do(ctx context.Context, method, path, contentType string, body []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.URL+path, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("storage: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.Key)
	req.Header.Set("apikey", c.Key)
	req.Header.Set("Content-Type", contentType)
	res, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("storage: %w", err)
	}
	return res, nil
}
