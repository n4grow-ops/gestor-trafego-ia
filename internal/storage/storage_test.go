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

func TestSignedURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if r.Method != http.MethodPost || r.URL.Path != "/storage/v1/object/sign/previews/a/b.jpg" || string(body) != `{"expiresIn":600}` {
			t.Errorf("%s %s %s", r.Method, r.URL.Path, body)
		}
		io.WriteString(w, `{"signedURL":"/object/sign/previews/a/b.jpg?token=T"}`)
	}))
	defer srv.Close()
	got, err := storage.New(srv.URL, "srv").SignedURL(context.Background(), "previews", "a/b.jpg", 600)
	if err != nil {
		t.Fatal(err)
	}
	if got != srv.URL+"/storage/v1/object/sign/previews/a/b.jpg?token=T" {
		t.Fatalf("url = %q", got)
	}
}

func TestTempLinks(t *testing.T) {
	var uploaded string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/storage/v1/object/sign/previews/"):
			io.WriteString(w, `{"signedURL":"/object/sign/previews/x.jpg?token=T"}`)
		case strings.HasPrefix(r.URL.Path, "/storage/v1/object/previews/"):
			body, _ := io.ReadAll(r.Body)
			uploaded = r.URL.Path + " " + r.Header.Get("Content-Type") + " " + string(body)
			io.WriteString(w, `{}`)
		default:
			t.Errorf("rota inesperada %s", r.URL.Path)
		}
	}))
	defer srv.Close()
	link, err := storage.New(srv.URL, "srv").TempLinks("previews", 60)(context.Background(), []byte("IMG"), "image/jpeg")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(uploaded, ".jpg image/jpeg IMG") || !strings.Contains(link, "token=T") {
		t.Fatalf("upload = %q link = %q", uploaded, link)
	}
}
