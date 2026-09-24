package storage_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/storage"
)

func TestUploadAndEnsureBucket(t *testing.T) {
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer srv" || r.Header.Get("apikey") != "srv" {
			t.Errorf("sem chave em %s", r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		calls = append(calls, r.Method+" "+r.URL.Path+" "+r.Header.Get("Content-Type")+" "+string(body))
		switch r.URL.Path {
		case "/storage/v1/bucket":
			w.WriteHeader(http.StatusBadRequest) // já existe
			io.WriteString(w, `{"statusCode":"409","error":"Duplicate","message":"The resource already exists"}`)
		default:
			io.WriteString(w, `{"Key":"creatives/x"}`)
		}
	}))
	defer srv.Close()
	s := storage.New(srv.URL, "srv")
	ctx := context.Background()
	if err := s.EnsureBucket(ctx, "creatives"); err != nil {
		t.Fatalf("bucket existente não é erro: %v", err)
	}
	if err := s.Upload(ctx, "creatives", "org/1/a.jpg", "image/jpeg", []byte("IMG")); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 || !strings.Contains(calls[0], `"public":false`) ||
		calls[1] != "POST /storage/v1/object/creatives/org/1/a.jpg image/jpeg IMG" {
		t.Fatalf("chamadas = %q", calls)
	}
}

func TestUploadError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	if err := storage.New(srv.URL, "k").Upload(context.Background(), "b", "p", "image/png", []byte("x")); err == nil {
		t.Fatal("esperava erro")
	}
}

func TestDownloadAndDelete(t *testing.T) {
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		switch {
		case r.Method == http.MethodGet:
			io.WriteString(w, "IMG")
		case strings.HasSuffix(r.URL.Path, "sumiu.jpg"):
			w.WriteHeader(http.StatusBadRequest)
			io.WriteString(w, `{"statusCode":"404","error":"not_found","message":"Object not found"}`)
		default:
			io.WriteString(w, `[]`)
		}
	}))
	defer srv.Close()
	s := storage.New(srv.URL, "k")
	ctx := context.Background()
	data, err := s.Download(ctx, "creatives", "org/1/a.jpg")
	if err != nil || string(data) != "IMG" {
		t.Fatalf("download = %q %v", data, err)
	}
	if err := s.Delete(ctx, "creatives", "org/1/a.jpg"); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, "creatives", "org/1/sumiu.jpg"); err != nil {
		t.Fatalf("arquivo que já não existe não é erro: %v", err)
	}
	want := []string{"GET /storage/v1/object/authenticated/creatives/org/1/a.jpg", "DELETE /storage/v1/object/creatives/org/1/a.jpg", "DELETE /storage/v1/object/creatives/org/1/sumiu.jpg"}
	if strings.Join(calls, "|") != strings.Join(want, "|") {
		t.Fatalf("chamadas = %q", calls)
	}
}
