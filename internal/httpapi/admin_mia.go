package httpapi

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/agent/attendant"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/agent/contract"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/llm"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/mia"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
)

// MiaTester é a Atendente usada pelo bot (attendant.Agent), para "testar a Mia" sem WhatsApp.
type MiaTester interface {
	Reply(ctx context.Context, in attendant.Input) (contract.AttendantTurn, llm.Usage, error)
}

// MiaSpeaker gera a amostra de voz pelo mesmo caminho do bot (TTS do OpenRouter).
type MiaSpeaker struct {
	Speaker      llm.Speaker
	Model        string
	DefaultVoice string // TTS_VOICE
}

func (a *Admin) registerMia(mux *http.ServeMux) {
	mux.HandleFunc("GET /admin/api/mia", a.only(true, a.miaGet))
	mux.HandleFunc("POST /admin/api/mia", a.only(true, a.miaSave))
	mux.HandleFunc("POST /admin/api/mia/restore", a.only(true, a.miaRestore))
	mux.HandleFunc("POST /admin/api/mia/revert", a.only(true, a.miaRevert))
	mux.HandleFunc("POST /admin/api/mia/test", a.only(true, a.miaTest))
	mux.HandleFunc("POST /admin/api/mia/voice-sample", a.only(true, a.miaVoiceSample))
}

// miaState é tudo o que a aba Mia precisa para abrir.
func (a *Admin) miaState(ctx context.Context) (map[string]any, error) {
	cfg, version, err := a.Store.MiaCurrent(ctx)
	if errors.Is(err, mia.ErrNoConfig) {
		cfg, err = mia.Defaults(), nil
	}
	if err != nil {
		return nil, err
	}
	cfg = cfg.Normalize()
	versions, err := a.Store.MiaVersions(ctx, 30)
	if err != nil {
		return nil, err
	}
	snaps, err := a.Store.ConversationSnapshots(ctx)
	if err != nil {
		return nil, err
	}
	// Clientes por etapa: a etapa ativa de cada conversa, com a mesma regra que monta o prompt.
	counts := map[string]int{}
	for _, s := range snaps {
		if s.State == store.StateNeedsHuman {
			counts["needs_human"]++
			continue
		}
		if st := cfg.StageFor(s.State, s.Fields); st != nil {
			counts[st.Key]++
		} else {
			counts["state:"+s.State]++
		}
	}
	envVoice := ""
	if a.MiaVoice != nil {
		envVoice = a.MiaVoice.DefaultVoice
	}
	return map[string]any{
		"config": cfg, "version": version, "defaults": mia.Defaults(), "text_specs": mia.TextSpecs,
		"voices": mia.Voices, "states": mia.States, "stage_counts": counts, "versions": versions,
		"test_enabled": a.MiaTest != nil, "voice_enabled": a.MiaVoice != nil, "env_voice": envVoice,
	}, nil
}

func (a *Admin) miaGet(w http.ResponseWriter, r *http.Request, m store.TeamMember, _ adminSession) {
	st, err := a.miaState(r.Context())
	if a.storeError(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, st)
}

type miaSaveBody struct {
	Config  mia.Config `json:"config"`
	Base    int        `json:"base_version"`
	Note    string     `json:"note"`
	Version int        `json:"version"` // só no revert
}

func (a *Admin) miaWrite(w http.ResponseWriter, r *http.Request, m store.TeamMember, cfg mia.Config, base int, note, action string, data map[string]any) {
	v, err := a.Store.SaveMia(r.Context(), cfg, base, m.Email, note)
	if errors.Is(err, store.ErrConflict) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "versao_desatualizada"})
		return
	}
	if errors.Is(err, store.ErrInvalidInput) {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "dados_invalidos",
			"detail": strings.TrimPrefix(strings.TrimPrefix(err.Error(), store.ErrInvalidInput.Error()+": "), mia.ErrInvalid.Error()+": ")})
		return
	}
	if a.storeError(w, err) {
		return
	}
	a.Mia.Invalidate() // a api vê na hora; o worker, no próximo ciclo do cache
	if data == nil {
		data = map[string]any{}
	}
	data["version"], data["note"] = v, note
	a.log(r, m, action, "mia", strconv.Itoa(v), "Mia v"+strconv.Itoa(v), data)
	st, err := a.miaState(r.Context())
	if a.storeError(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (a *Admin) miaSave(w http.ResponseWriter, r *http.Request, m store.TeamMember, _ adminSession) {
	var b miaSaveBody
	if !decodeBigJSON(w, r, &b) {
		return
	}
	a.miaWrite(w, r, m, b.Config, b.Base, b.Note, "mia_save", nil)
}

func (a *Admin) miaRestore(w http.ResponseWriter, r *http.Request, m store.TeamMember, _ adminSession) {
	var b miaSaveBody
	if !decodeBigJSON(w, r, &b) {
		return
	}
	a.miaWrite(w, r, m, mia.Defaults(), b.Base, "restaurado o padrão", "mia_restore", nil)
}

func (a *Admin) miaRevert(w http.ResponseWriter, r *http.Request, m store.TeamMember, _ adminSession) {
	var b miaSaveBody
	if !decodeBigJSON(w, r, &b) {
		return
	}
	old, err := a.Store.MiaVersionConfig(r.Context(), b.Version)
	if a.storeError(w, err) {
		return
	}
	a.miaWrite(w, r, m, old, b.Base, "volta à versão "+strconv.Itoa(b.Version), "mia_revert", map[string]any{"from_version": b.Version})
}

// miaTest manda uma mensagem de teste para a Atendente com a configuração em edição. Não toca em
// conversa nem em WhatsApp; o custo entra no uso de IA (sem cliente).
func (a *Admin) miaTest(w http.ResponseWriter, r *http.Request, m store.TeamMember, _ adminSession) {
	if a.MiaTest == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "ia_desligada"})
		return
	}
	var b struct {
		Config  mia.Config `json:"config"`
		State   string     `json:"state"`
		Segment string     `json:"segment"`
		Message string     `json:"message"`
		History []struct {
			FromClient bool   `json:"from_client"`
			Text       string `json:"text"`
		} `json:"history"`
	}
	if !decodeBigJSON(w, r, &b) {
		return
	}
	msg := strings.TrimSpace(b.Message)
	if msg == "" || utf8.RuneCountInString(msg) > 1000 || len(b.History) > 20 {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "texto_invalido"})
		return
	}
	if a.Limiter != nil && !a.Limiter.Allow("mia-test:"+m.Email, 20) {
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "muitas_tentativas"})
		return
	}
	cfg := b.Config.Normalize()
	if err := cfg.Validate(); err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "dados_invalidos", "detail": err.Error()})
		return
	}
	state := b.State
	valid := false
	for _, s := range mia.States {
		valid = valid || s == state
	}
	if !valid {
		state = "idle"
	}
	in := attendant.Input{OrgName: "Cliente de teste", Niche: b.Segment, State: state, Message: msg, Config: &cfg,
		Segment: cfg.SegmentFor(b.Segment, "")}
	in.Missing = cfg.CampaignMissing(in.Fields)
	for _, h := range b.History {
		in.History = append(in.History, attendant.Turn{FromClient: h.FromClient, Text: truncate(h.Text, 1000)})
	}
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	turn, usage, err := a.MiaTest.Reply(ctx, in)
	if rerr := a.Store.RecordUsage(r.Context(), 0, "attendant", usage); rerr != nil {
		a.Log.Warn("painel: uso do teste da Mia não gravado", "erro", rerr)
	}
	if err != nil {
		a.Log.Error("painel: teste da Mia falhou", "erro", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "ia_falhou"})
		return
	}
	stage := ""
	if st := cfg.StageFor(state, in.Fields); st != nil {
		stage = st.Name
	}
	seg := ""
	if in.Segment != nil {
		seg = in.Segment.Name
	}
	a.log(r, m, "mia_test", "mia", "", "teste da Mia", map[string]any{"state": state, "segment": seg})
	writeJSON(w, http.StatusOK, map[string]any{"turn": turn, "stage": stage, "segment": seg, "cost_usd": usage.CostUSD})
}

// miaVoiceSample gera um áudio curto com a voz escolhida, pelo mesmo TTS do bot (gasta crédito).
func (a *Admin) miaVoiceSample(w http.ResponseWriter, r *http.Request, m store.TeamMember, _ adminSession) {
	if a.MiaVoice == nil || a.MiaVoice.Speaker == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "voz_desligada"})
		return
	}
	var b struct {
		Voice string `json:"voice"`
		Text  string `json:"text"`
	}
	if !decodeJSON(w, r, &b) {
		return
	}
	voice := b.Voice
	if voice == "" {
		voice = a.MiaVoice.DefaultVoice
	}
	ok := false
	for _, v := range mia.Voices {
		ok = ok || v == voice
	}
	if !ok {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "voz_invalida"})
		return
	}
	text := strings.TrimSpace(b.Text)
	if text == "" {
		text = "Oi! Eu sou a Mia, do Meu Anúncio. Vamos montar seus anúncios juntos?"
	}
	if utf8.RuneCountInString(text) > 200 {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "texto_invalido"})
		return
	}
	if a.Limiter != nil && !a.Limiter.Allow("mia-voice:"+m.Email, 6) {
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "muitas_tentativas"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	sp, err := a.MiaVoice.Speaker.Speak(ctx, a.MiaVoice.Model, voice, text)
	if rerr := a.Store.RecordUsage(r.Context(), 0, "tts", sp.Usage); rerr != nil {
		a.Log.Warn("painel: uso da amostra de voz não gravado", "erro", rerr)
	}
	if err != nil {
		a.Log.Error("painel: amostra de voz falhou", "voz", voice, "erro", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "voz_falhou"})
		return
	}
	a.log(r, m, "mia_voice_sample", "mia", "", voice, map[string]string{"voice": voice})
	writeJSON(w, http.StatusOK, map[string]string{"mime": sp.Mime, "audio": base64.StdEncoding.EncodeToString(sp.Audio)})
}

// decodeBigJSON é o decodeJSON com espaço para a configuração inteira da Mia (até 512 KB).
func decodeBigJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 512<<10)
	return decodeJSONLimit(w, r, v, 512<<10)
}
