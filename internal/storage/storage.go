// Package storage guarda arquivos no Supabase Storage (bucket privado), com a chave de serviço.
package storage

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
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

// Download lê o arquivo do bucket privado.
func (c *Client) Download(ctx context.Context, bucket, path string) ([]byte, error) {
	res, err := c.do(ctx, http.MethodGet, "/storage/v1/object/authenticated/"+bucket+"/"+path, "", nil)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode/100 != 2 {
		msg, _ := io.ReadAll(io.LimitReader(res.Body, 2048))
		return nil, fmt.Errorf("storage: download: status %d: %s", res.StatusCode, strings.TrimSpace(string(msg)))
	}
	return io.ReadAll(io.LimitReader(res.Body, maxDownload))
}

// Delete apaga o arquivo do bucket. Arquivo que já não existe conta como apagado.
func (c *Client) Delete(ctx context.Context, bucket, path string) error {
	res, err := c.do(ctx, http.MethodDelete, "/storage/v1/object/"+bucket+"/"+path, "", nil)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	msg, _ := io.ReadAll(io.LimitReader(res.Body, 2048))
	if res.StatusCode/100 == 2 || res.StatusCode == http.StatusNotFound || strings.Contains(string(msg), "not_found") {
		return nil
	}
	return fmt.Errorf("storage: delete: status %d: %s", res.StatusCode, strings.TrimSpace(string(msg)))
}

// SignedURL devolve um link temporário (expiresIn em segundos) para ler um arquivo do bucket privado.
func (c *Client) SignedURL(ctx context.Context, bucket, path string, expiresIn int) (string, error) {
	body, _ := json.Marshal(map[string]any{"expiresIn": expiresIn})
	res, err := c.do(ctx, http.MethodPost, "/storage/v1/object/sign/"+bucket+"/"+path, "application/json", body)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	msg, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
	if res.StatusCode/100 != 2 {
		return "", fmt.Errorf("storage: link: status %d: %s", res.StatusCode, strings.TrimSpace(string(msg)))
	}
	var out struct {
		SignedURL string `json:"signedURL"`
	}
	if err := json.Unmarshal(msg, &out); err != nil || out.SignedURL == "" {
		return "", fmt.Errorf("storage: link: resposta sem signedURL: %s", strings.TrimSpace(string(msg)))
	}
	return c.URL + "/storage/v1" + out.SignedURL, nil
}

// TempLinks sobe cada arquivo com nome aleatório no bucket e devolve um link temporário (ttl em
// segundos). Serve a conta oficial do WhatsApp, que só manda imagem por link.
func (c *Client) TempLinks(bucket string, ttl int) func(ctx context.Context, data []byte, mime string) (string, error) {
	return func(ctx context.Context, data []byte, mime string) (string, error) {
		var b [16]byte
		if _, err := rand.Read(b[:]); err != nil {
			return "", fmt.Errorf("storage: %w", err)
		}
		ext := ".bin"
		switch mime {
		case "image/jpeg":
			ext = ".jpg"
		case "image/png":
			ext = ".png"
		case "image/webp":
			ext = ".webp"
		}
		path := time.Now().UTC().Format("2006-01-02") + "/" + hex.EncodeToString(b[:]) + ext
		if err := c.Upload(ctx, bucket, path, mime, data); err != nil {
			return "", err
		}
		return c.SignedURL(ctx, bucket, path, ttl)
	}
}

// maxDownload limita a leitura (vídeos do WhatsApp ficam bem abaixo disso).
const maxDownload = 100 << 20

func (c *Client) do(ctx context.Context, method, path, contentType string, body []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.URL+path, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("storage: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.Key)
	req.Header.Set("apikey", c.Key)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	res, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("storage: %w", err)
	}
	return res, nil
}
