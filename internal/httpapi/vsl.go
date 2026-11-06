package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
)

// VSLVideoID é o vídeo do YouTube da página inicial. Trocar aqui troca a página e a estatística
// (o admin mostra os números do vídeo atual).
const VSLVideoID = "B-mCESD6gtg"

var (
	uuidRe   = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	sourceRe = regexp.MustCompile(`[^a-z0-9._-]+`)
)

func (s *Signup) videoID() string {
	if s.VSLVideo != "" {
		return s.VSLVideo
	}
	return VSLVideoID
}

// vslEvent recebe o que a página inicial manda enquanto a pessoa vê a VSL: abriu a página (view),
// deu play, progresso (faixas assistidas e ponto mais longe) e clique para começar no WhatsApp.
// Público e sem login: tudo é validado e limitado, e nada além da estatística depende disso.
func (s *Signup) vslEvent(w http.ResponseWriter, r *http.Request) {
	if s.Store == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	var b struct {
		Session  string  `json:"s"`
		Visitor  string  `json:"v"`
		Event    string  `json:"e"`
		Duration float64 `json:"d"`
		Max      float64 `json:"t"`
		Mask     int     `json:"m"`
		Watched  float64 `json:"w"`
		Source   string  `json:"src"`
	}
	dec := json.NewDecoder(io.LimitReader(r.Body, 1024))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&b); err != nil || !uuidRe.MatchString(b.Session) || !uuidRe.MatchString(b.Visitor) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalido"})
		return
	}
	switch b.Event {
	case "view", "play", "tick", "cta":
	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalido"})
		return
	}
	if b.Duration < 0 || b.Duration > 36000 || b.Max < 0 || b.Mask < 0 || b.Mask >= 1<<store.VSLBuckets || b.Watched < 0 || b.Watched > 36000 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalido"})
		return
	}
	dur, maxS := int(b.Duration), int(b.Max)
	if dur > 0 && maxS > dur {
		maxS = dur
	}
	if maxS > 36000 {
		maxS = 36000
	}
	ev := store.VSLEvent{
		Session: b.Session, Visitor: b.Visitor, VideoID: s.videoID(), Device: deviceOf(r.UserAgent()),
		Source: cleanSource(b.Source), Played: b.Event == "play" || b.Event == "tick" || b.Mask != 0,
		CTA: b.Event == "cta", Duration: dur, Max: maxS, Mask: b.Mask, Watched: int(b.Watched),
	}
	if err := s.Store.RecordVSL(r.Context(), ev, time.Now()); err != nil {
		s.Log.Error("vsl: falha ao gravar", "erro", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "falha"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// deviceOf separa celular, tablet e computador pelo User-Agent (o servidor decide, não a página).
func deviceOf(ua string) string {
	switch {
	case ua == "":
		return ""
	case strings.Contains(ua, "iPad") || strings.Contains(ua, "Tablet"):
		return "tablet"
	case strings.Contains(ua, "Mobi") || strings.Contains(ua, "Android") || strings.Contains(ua, "iPhone"):
		return "mobile"
	default:
		return "desktop"
	}
}

// cleanSource guarda de onde a pessoa veio (utm_source ou o site que linkou), só letras simples.
func cleanSource(s string) string {
	s = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(s)), "www.")
	s = strings.Trim(sourceRe.ReplaceAllString(s, "-"), "-")
	if len(s) > 40 {
		s = s[:40]
	}
	return s
}
