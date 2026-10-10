package meta

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// Publicação efetiva na Marketing API. Ordem: público (busca de local e interesses) → upload das mídias →
// campanha → conjunto → criativos e anúncios. Tudo nasce PAUSED. Falha depois da campanha criada apaga a
// campanha (a Meta apaga conjunto e anúncios junto).

const (
	maxAds          = 6
	maxInterests    = 10
	minCityRadiusKM = 17 // limite da Meta para raio em volta de cidade
	maxCityRadiusKM = 80
	videoPollEvery  = 5 * time.Second
	videoPolls      = 120 // ~10 minutos de processamento
	uploadTimeout   = 5 * time.Minute
	// Link de destino dos anúncios de clique para o WhatsApp (o clique abre a conversa com o número da Página).
	whatsAppLink = "https://api.whatsapp.com/send"
)

var _ Publisher = (*Client)(nil)

// apiError é o erro da Graph API com os códigos.
type apiError struct {
	Path      string
	Status    int
	Code      int
	Subcode   int
	Message   string
	UserTitle string
	UserMsg   string
}

func (e *apiError) Error() string {
	return fmt.Sprintf("meta: %s: HTTP %d código %d/%d: %s", e.Path, e.Status, e.Code, e.Subcode, e.Message)
}

// call faz a requisição com token e appsecret_proof. GET e DELETE levam tudo na query; POST, no corpo.
func (c *Client) call(ctx context.Context, method, path, token string, q url.Values, out any) error {
	q = c.auth(token, q)
	u := c.GraphURL + "/" + version + path
	var body io.Reader
	if method == http.MethodPost {
		body = strings.NewReader(q.Encode())
	} else {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return fmt.Errorf("meta: %s: requisição inválida", path)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	hc := c.HTTP
	if strings.HasSuffix(path, "/adimages") {
		hc = c.uploadHTTP()
	}
	return c.do(hc, req, path, out)
}

// uploadHTTP é o cliente das mídias: mesmo transporte, prazo maior.
func (c *Client) uploadHTTP() *http.Client {
	return &http.Client{Transport: c.HTTP.Transport, Timeout: uploadTimeout}
}

func (c *Client) do(hc *http.Client, req *http.Request, path string, out any) error {
	resp, err := hc.Do(req)
	if err != nil {
		if ctxErr := req.Context().Err(); ctxErr != nil {
			return ctxErr
		}
		// A URL tem token e segredo: o erro vai sem ela.
		return fmt.Errorf("meta: %s: falha de rede", path)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("meta: %s: resposta incompleta", path)
	}
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Error struct {
				Message   string `json:"message"`
				Code      int    `json:"code"`
				Subcode   int    `json:"error_subcode"`
				UserTitle string `json:"error_user_title"`
				UserMsg   string `json:"error_user_msg"`
			} `json:"error"`
		}
		_ = json.Unmarshal(raw, &e)
		return &apiError{Path: path, Status: resp.StatusCode, Code: e.Error.Code, Subcode: e.Error.Subcode,
			Message: e.Error.Message, UserTitle: e.Error.UserTitle, UserMsg: e.Error.UserMsg}
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("meta: %s: %w", path, err)
	}
	return nil
}

func (c *Client) sleep(ctx context.Context, d time.Duration) error {
	if c.Sleep != nil {
		return c.Sleep(ctx, d)
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func jsonParam(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// plan é como o objetivo vira campanha e conjunto.
type plan struct {
	objective    string
	optimization string
	destination  string
	whatsapp     bool
	link         string // destino do clique (site ou página) quando não é WhatsApp
}

func planFor(in PublishInput) (plan, []string, error) {
	var warnings []string
	wantsWhatsApp := in.WhatsAppNumber != "" || in.PageWhatsApp
	for _, cp := range in.Copies {
		if cp.CTA == "WHATSAPP_MESSAGE" {
			wantsWhatsApp = true
		}
	}
	wa := plan{optimization: "CONVERSATIONS", destination: "WHATSAPP", whatsapp: true}
	link := strings.TrimSpace(in.LinkURL)
	if link == "" {
		link = "https://www.facebook.com/" + in.PageID
	}
	traffic := plan{objective: "OUTCOME_TRAFFIC", optimization: "LINK_CLICKS", destination: "WEBSITE", link: link}
	switch in.Objective {
	case "OUTCOME_ENGAGEMENT":
		// Conversas no WhatsApp: a Página precisa ter WhatsApp Business vinculado.
		wa.objective = in.Objective
		return wa, nil, nil
	case "OUTCOME_LEADS":
		// Leads por conversa no WhatsApp. Formulário de cadastro (LEAD_GENERATION) fica fora: sem WhatsApp vira tráfego.
		if wantsWhatsApp {
			wa.objective = in.Objective
			return wa, nil, nil
		}
		warnings = append(warnings, "objetivo de leads sem WhatsApp: publicado como tráfego para a página")
		return traffic, warnings, nil
	case "OUTCOME_TRAFFIC":
		return traffic, nil, nil
	}
	return plan{}, nil, fmt.Errorf("objetivo %q não suportado", in.Objective)
}

// Publish cria campanha, conjunto, criativos e anúncios PAUSADOS na conta do cliente.
func (c *Client) Publish(ctx context.Context, token string, in PublishInput) (PublishResult, error) {
	var res PublishResult
	acct := strings.TrimPrefix(strings.TrimSpace(in.AdAccountID), "act_")
	if err := validateInput(acct, in); err != nil {
		return res, err
	}
	act := "/act_" + acct
	p, warns, err := planFor(in)
	if err != nil {
		return res, &PublishError{Step: "campaign", Message: err.Error(), UserMessage: genericUserMessage}
	}
	res.Warnings = append(res.Warnings, warns...)

	targeting, warns, err := c.targeting(ctx, token, in)
	if err != nil {
		return res, err
	}
	res.Warnings = append(res.Warnings, warns...)

	media, err := c.uploadAll(ctx, token, act, in.Creatives)
	if err != nil {
		return res, err
	}
	for _, m := range media {
		if m.igMediaID != "" {
			res.Warnings = append(res.Warnings, "anúncio criado a partir do post do Instagram: vai com a legenda, as curtidas e os comentários do post")
			break
		}
	}

	// Tentativa anterior desta publicação que deixou campanha na conta: apaga antes de criar outra.
	if err := c.removeLeftover(ctx, token, act, in.Name); err != nil {
		return res, err
	}

	// Daqui em diante, falha apaga a campanha.
	fail := func(step string, err error) (PublishResult, error) {
		perr := publishError(step, err)
		if res.CampaignID != "" {
			if rerr := c.rollback(token, res.CampaignID); rerr != nil {
				var pe *PublishError
				if errors.As(perr, &pe) {
					cp := *pe
					cp.Orphan = res.CampaignID
					cp.Message += fmt.Sprintf("; rollback falhou, campanha %s ficou na conta: %v", res.CampaignID, rerr)
					perr = &cp
				}
			}
		}
		return PublishResult{Warnings: res.Warnings}, perr
	}

	var created struct {
		ID string `json:"id"`
	}
	if err := c.call(ctx, http.MethodPost, act+"/campaigns", token, url.Values{
		"name": {in.Name}, "objective": {p.objective}, "status": {"PAUSED"}, "buying_type": {"AUCTION"},
		"special_ad_categories": {"[]"},
		// Orçamento fica no conjunto; a API passou a exigir o campo explícito nesse caso.
		"is_adset_budget_sharing_enabled": {"false"},
	}, &created); err != nil {
		return fail("campaign", err)
	}
	res.CampaignID = created.ID

	promoted := map[string]any{"page_id": in.PageID}
	if num := digits(in.WhatsAppNumber); p.whatsapp && num != "" {
		promoted["whatsapp_phone_number"] = num
	}
	adset := url.Values{
		"name": {in.Name + " - público"}, "campaign_id": {res.CampaignID}, "status": {"PAUSED"},
		"daily_budget": {fmt.Sprint(in.DailyBudgetCents)}, "billing_event": {"IMPRESSIONS"},
		"optimization_goal": {p.optimization}, "bid_strategy": {"LOWEST_COST_WITHOUT_CAP"},
		"destination_type": {p.destination}, "promoted_object": {jsonParam(promoted)}, "targeting": {jsonParam(targeting)},
	}
	if !in.StartTime.IsZero() && in.StartTime.After(time.Now()) {
		adset.Set("start_time", in.StartTime.UTC().Format(time.RFC3339))
	}
	if !in.EndTime.IsZero() {
		adset.Set("end_time", in.EndTime.UTC().Format(time.RFC3339))
	}
	created.ID = ""
	if err := c.call(ctx, http.MethodPost, act+"/adsets", token, adset, &created); err != nil {
		return fail("adset", err)
	}
	res.AdSetID = created.ID

	usedPost := map[int]bool{}
	for _, pr := range pairs(len(media), len(in.Copies)) {
		m, cp := media[pr[0]], in.Copies[pr[1]]
		label := fmt.Sprintf("%s - anúncio %d", in.Name, len(res.AdIDs)+1)
		var params url.Values
		if m.igMediaID == "" {
			params = url.Values{"name": {label}, "object_story_spec": {jsonParam(storySpec(in, p, m, cp))}}
		} else {
			// post do Instagram já tem legenda: um anúncio por post, sem combinar com os textos
			if usedPost[pr[0]] {
				continue
			}
			usedPost[pr[0]] = true
			params = url.Values{"name": {label}, "object_id": {in.PageID}, "instagram_user_id": {in.InstagramActorID},
				"source_instagram_media_id": {m.igMediaID}, "call_to_action": {jsonParam(callToAction(p, cp, false))}}
		}
		created.ID = ""
		if err := c.call(ctx, http.MethodPost, act+"/adcreatives", token, params, &created); err != nil {
			return fail("creative", err)
		}
		creativeID := created.ID
		created.ID = ""
		if err := c.call(ctx, http.MethodPost, act+"/ads", token, url.Values{
			"name": {label}, "adset_id": {res.AdSetID}, "status": {"PAUSED"},
			"creative": {jsonParam(map[string]string{"creative_id": creativeID})},
		}, &created); err != nil {
			return fail("ad", err)
		}
		res.AdIDs = append(res.AdIDs, created.ID)
	}
	return res, nil
}

// digits deixa só os dígitos do telefone ("+55 (49) 99999-0000" -> "5549999990000").
func digits(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, s)
}

func validateInput(acct string, in PublishInput) error {
	var miss []string
	if acct == "" {
		miss = append(miss, "conta de anúncios")
	}
	if strings.TrimSpace(in.PageID) == "" {
		miss = append(miss, "página")
	}
	if in.DailyBudgetCents <= 0 {
		miss = append(miss, "orçamento")
	}
	if len(in.Creatives) == 0 {
		miss = append(miss, "criativos")
	}
	for _, cr := range in.Creatives {
		if cr.InstagramMediaID != "" && strings.TrimSpace(in.InstagramActorID) == "" {
			miss = append(miss, "conta do Instagram (para usar o post)")
			break
		}
	}
	if len(in.Copies) == 0 {
		miss = append(miss, "textos")
	}
	if len(miss) == 0 {
		return nil
	}
	return &PublishError{Step: "campaign", Message: "entrada incompleta: " + strings.Join(miss, ", "), UserMessage: genericUserMessage}
}

// pairs combina criativos e textos: cada rodada dá um texto diferente a cada criativo, até maxAds.
func pairs(nMedia, nCopies int) [][2]int {
	var out [][2]int
	for r := 0; r < nCopies; r++ {
		for i := 0; i < nMedia; i++ {
			if len(out) == maxAds {
				return out
			}
			out = append(out, [2]int{i, (i + r) % nCopies})
		}
	}
	return out
}

// uploaded é a mídia já na conta: hash da imagem, ou vídeo com a miniatura.
type uploaded struct {
	imageHash string
	videoID   string
	thumbURL  string
	igMediaID string // post do Instagram usado como está
}

func (c *Client) uploadAll(ctx context.Context, token, act string, list []PublishCreative) ([]uploaded, error) {
	out := make([]uploaded, 0, len(list))
	for _, cr := range list {
		var u uploaded
		var err error
		switch {
		case cr.InstagramMediaID != "":
			u.igMediaID = cr.InstagramMediaID
		default:
			switch cr.Kind {
			case "image":
				u.imageHash, err = c.uploadImage(ctx, token, act, cr.Data)
			case "video":
				u.videoID, u.thumbURL, err = c.uploadVideo(ctx, token, act, cr)
			default:
				err = fmt.Errorf("tipo de mídia %q", cr.Kind)
			}
		}
		if err != nil {
			return nil, publishError("upload", err)
		}
		out = append(out, u)
	}
	return out, nil
}

func (c *Client) uploadImage(ctx context.Context, token, act string, data []byte) (string, error) {
	var r struct {
		Images map[string]struct {
			Hash string `json:"hash"`
		} `json:"images"`
	}
	if err := c.call(ctx, http.MethodPost, act+"/adimages", token,
		url.Values{"bytes": {base64.StdEncoding.EncodeToString(data)}}, &r); err != nil {
		return "", err
	}
	for _, img := range r.Images {
		if img.Hash != "" {
			return img.Hash, nil
		}
	}
	return "", errors.New("meta: adimages sem hash")
}

func (c *Client) uploadVideo(ctx context.Context, token, act string, cr PublishCreative) (string, string, error) {
	path := act + "/advideos"
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for k, v := range c.auth(token, url.Values{}) {
		_ = mw.WriteField(k, v[0])
	}
	name := "video.mp4"
	if strings.Contains(cr.Mime, "quicktime") {
		name = "video.mov"
	}
	fw, err := mw.CreateFormFile("source", name)
	if err != nil {
		return "", "", err
	}
	if _, err := fw.Write(cr.Data); err != nil {
		return "", "", err
	}
	if err := mw.Close(); err != nil {
		return "", "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.GraphURL+"/"+version+path, &buf)
	if err != nil {
		return "", "", fmt.Errorf("meta: %s: requisição inválida", path)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	var v struct {
		ID string `json:"id"`
	}
	if err := c.do(c.uploadHTTP(), req, path, &v); err != nil {
		return "", "", err
	}
	if v.ID == "" {
		return "", "", errors.New("meta: advideos sem id")
	}
	if err := c.waitVideo(ctx, token, v.ID); err != nil {
		return "", "", err
	}
	thumb, err := c.videoThumb(ctx, token, v.ID)
	return v.ID, thumb, err
}

// errVideoFailed: a Meta não conseguiu processar o vídeo.
var errVideoFailed = errors.New("meta: vídeo não processado")

// waitVideo consulta o status até ficar pronto, com limite de tentativas.
func (c *Client) waitVideo(ctx context.Context, token, id string) error {
	for i := 0; i < videoPolls; i++ {
		var s struct {
			Status struct {
				VideoStatus string `json:"video_status"`
			} `json:"status"`
		}
		if err := c.call(ctx, http.MethodGet, "/"+url.PathEscape(id), token, url.Values{"fields": {"status"}}, &s); err != nil {
			return err
		}
		switch s.Status.VideoStatus {
		case "ready":
			return nil
		case "error", "expired":
			return errVideoFailed
		}
		if err := c.sleep(ctx, videoPollEvery); err != nil {
			return err
		}
	}
	return errors.New("meta: vídeo ainda processando depois do prazo")
}

func (c *Client) videoThumb(ctx context.Context, token, id string) (string, error) {
	var r struct {
		Data []struct {
			URI       string `json:"uri"`
			Preferred bool   `json:"is_preferred"`
		} `json:"data"`
	}
	if err := c.call(ctx, http.MethodGet, "/"+url.PathEscape(id)+"/thumbnails", token, url.Values{"fields": {"uri,is_preferred"}}, &r); err != nil {
		return "", err
	}
	first := ""
	for _, t := range r.Data {
		if t.Preferred && t.URI != "" {
			return t.URI, nil
		}
		if first == "" {
			first = t.URI
		}
	}
	if first == "" {
		return "", errors.New("meta: vídeo sem miniatura")
	}
	return first, nil
}

// storySpec monta o object_story_spec do criativo.
func storySpec(in PublishInput, p plan, m uploaded, cp PublishCopy) map[string]any {
	spec := map[string]any{"page_id": in.PageID}
	if in.InstagramActorID != "" {
		spec["instagram_user_id"] = in.InstagramActorID
	}
	link := p.link
	if p.whatsapp {
		link = whatsAppLink
	}
	cta := callToAction(p, cp, m.videoID != "")
	if m.videoID != "" {
		spec["video_data"] = map[string]any{"video_id": m.videoID, "image_url": m.thumbURL,
			"title": cp.Headline, "message": cp.Text, "call_to_action": cta}
		return spec
	}
	spec["link_data"] = map[string]any{"image_hash": m.imageHash, "message": cp.Text, "name": cp.Headline,
		"link": link, "call_to_action": cta}
	return spec
}

// callToAction é o botão do anúncio. No vídeo de WhatsApp o link vai dentro do botão (video_data não tem campo link).
func callToAction(p plan, cp PublishCopy, video bool) map[string]any {
	if p.whatsapp {
		value := map[string]any{"app_destination": "WHATSAPP"}
		if video {
			value["link"] = whatsAppLink
		}
		return map[string]any{"type": "WHATSAPP_MESSAGE", "value": value}
	}
	t := cp.CTA
	if t != "LEARN_MORE" && t != "CONTACT_US" {
		t = "LEARN_MORE" // WHATSAPP_MESSAGE não vale para clique no site
	}
	return map[string]any{"type": t, "value": map[string]any{"link": p.link}}
}

// rollback apaga a campanha criada (conjunto e anúncios vão junto). Usa contexto próprio:
// o da publicação pode já ter sido cancelado. Erro = a campanha ficou na conta.
func (c *Client) rollback(token, campaignID string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return c.call(ctx, http.MethodDelete, "/"+url.PathEscape(campaignID), token, url.Values{}, nil)
}

// nameMarker é a marca da publicação no fim do nome da campanha ("... · #p12").
var nameMarker = regexp.MustCompile(`#p\d+$`)

// removeLeftover procura, pelo nome exato com a marca da publicação, campanha de tentativa anterior
// (worker caiu, prazo estourou ou rollback falhou) e apaga. Campanha ativa ou que não apaga barra a
// publicação: criar outra duplicaria anúncios na conta.
func (c *Client) removeLeftover(ctx context.Context, token, act, name string) error {
	if !nameMarker.MatchString(name) {
		return nil
	}
	var r struct {
		Data []struct {
			ID     string `json:"id"`
			Name   string `json:"name"`
			Status string `json:"effective_status"`
		} `json:"data"`
	}
	filter := jsonParam([]map[string]string{{"field": "name", "operator": "EQUAL", "value": name}})
	if err := c.call(ctx, http.MethodGet, act+"/campaigns", token, url.Values{"filtering": {filter},
		"fields": {"id,name,effective_status"}, "limit": {"25"}}, &r); err != nil {
		return publishError("campaign", err)
	}
	for _, x := range r.Data {
		if x.ID == "" || x.Name != name {
			continue
		}
		if x.Status == "ACTIVE" {
			return &PublishError{Step: "campaign", Message: fmt.Sprintf("campanha %s de tentativa anterior está ativa", x.ID),
				UserMessage: genericUserMessage}
		}
		if err := c.call(ctx, http.MethodDelete, "/"+url.PathEscape(x.ID), token, url.Values{}, nil); err != nil {
			pe := publishError("campaign", err).(*PublishError)
			pe.Message = fmt.Sprintf("campanha %s de tentativa anterior não apagou: %s", x.ID, pe.Message)
			pe.Orphan = x.ID
			return pe
		}
	}
	return nil
}

// SetStatus liga ou pausa só a campanha. Conjunto e anúncios ficam como estão: o que o cliente (ou a equipe)
// pausou no Gerenciador continua pausado. A primeira ativação é ActivateFirst.
func (c *Client) SetStatus(ctx context.Context, token, campaignID, status string) error {
	if status != "ACTIVE" && status != "PAUSED" {
		return &PublishError{Step: "status", Message: "status inválido: " + status, UserMessage: genericUserMessage}
	}
	if err := c.call(ctx, http.MethodPost, "/"+url.PathEscape(campaignID), token, url.Values{"status": {status}}, nil); err != nil {
		return publishError("status", err)
	}
	return nil
}

var _ Activator = (*Client)(nil)

// ActivateFirst liga, na ordem, o conjunto (com o fim contado a partir de agora), os anúncios criados na
// publicação e a campanha por último: nada entrega antes de tudo estar ligado.
func (c *Client) ActivateFirst(ctx context.Context, token string, a FirstActivation) error {
	set := func(objID string, extra url.Values) error {
		q := url.Values{"status": {"ACTIVE"}}
		for k, v := range extra {
			q[k] = v
		}
		if err := c.call(ctx, http.MethodPost, "/"+url.PathEscape(objID), token, q, nil); err != nil {
			return publishError("status", err)
		}
		return nil
	}
	if strings.TrimSpace(a.CampaignID) == "" {
		return &PublishError{Step: "status", Message: "ativação sem campanha", UserMessage: statusUserMessage}
	}
	if a.AdSetID != "" {
		extra := url.Values{}
		if !a.EndTime.IsZero() {
			extra.Set("end_time", a.EndTime.UTC().Format(time.RFC3339))
		}
		if err := set(a.AdSetID, extra); err != nil {
			return err
		}
	}
	for _, id := range a.AdIDs {
		if err := set(id, nil); err != nil {
			return err
		}
	}
	return set(a.CampaignID, nil)
}

const (
	genericUserMessage = "Não consegui criar a campanha na sua conta da Meta agora. A equipe já foi avisada e vai olhar isso pra você."
	statusUserMessage  = "Não consegui mudar o status da campanha na Meta agora. Tenta de novo daqui a pouquinho; se continuar, a equipe olha pra você."
)

// publishError converte o erro da etapa em PublishError com a mensagem para o cliente.
func publishError(step string, err error) error {
	var pe *PublishError
	if errors.As(err, &pe) {
		return pe
	}
	out := &PublishError{Step: step, Message: err.Error()}
	var ae *apiError
	if errors.As(err, &ae) {
		out.Code, out.Subcode = ae.Code, ae.Subcode
		out.Message = ae.Error()
		if ae.UserMsg != "" {
			out.Message += " (" + ae.UserTitle + ": " + ae.UserMsg + ")"
		}
	}
	out.UserMessage = userMessage(step, err, ae)
	return out
}

func userMessage(step string, err error, ae *apiError) string {
	if errors.Is(err, errVideoFailed) {
		return "A Meta não conseguiu processar um dos vídeos. Manda o vídeo de novo em MP4 (até 4 GB) que eu tento outra vez."
	}
	generic := genericUserMessage
	if step == "status" {
		generic = statusUserMessage
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || ae == nil {
		return generic
	}
	text := strings.ToLower(ae.Message + " " + ae.UserTitle + " " + ae.UserMsg)
	has := func(words ...string) bool {
		for _, w := range words {
			if strings.Contains(text, w) {
				return true
			}
		}
		return false
	}
	switch {
	case ae.Code == 190 || ae.Code == 102:
		return "A conexão com a sua conta da Meta expirou. Conecta de novo pelo link que eu te mandar e eu publico na sequência."
	case ae.Code == 10 || ae.Code == 3 || (ae.Code >= 200 && ae.Code <= 299) || has("ads_management", "permission"):
		return "Faltou permissão pra criar anúncios na sua conta da Meta. Conecta a conta de novo e deixa marcadas todas as permissões (contas de anúncios e Página)."
	case ae.Code == 4 || ae.Code == 17 || ae.Code == 32 || ae.Code == 613 || ae.Code == 80004:
		return "A Meta pediu pra esperar um pouco antes de criar mais coisas na sua conta. Responde aqui daqui a alguns minutos que eu tento de novo."
	case has("whatsapp"):
		return "Sua Página do Facebook não está com o WhatsApp Business vinculado (ou o número não confere). Vincula o WhatsApp na Página (Configurações > Contas vinculadas > WhatsApp) e me avisa aqui."
	case has("payment", "funding", "pagamento"):
		return "Sua conta de anúncios está sem forma de pagamento válida. Cadastra um cartão ou saldo no Gerenciador de Anúncios (Configurações de pagamento) e me avisa aqui que eu publico."
	case has("disabled", "desativad", "restricted", "restrit"):
		return "Sua conta de anúncios está desativada ou com restrição na Meta. Confere a Qualidade da Conta no Gerenciador de Anúncios; quando liberar, eu publico."
	case ae.Code == 368 || has("policy", "polític", "politic"):
		return "A Meta recusou o anúncio pelas políticas de publicidade. A equipe vai revisar o texto e as imagens com você."
	case step == "upload" && has("image", "imagem", "small", "pequen", "dimension", "resolution", "resolução"):
		return "Uma das imagens ficou pequena demais pra Meta. Manda imagens com pelo menos 600x600 pixels (o ideal é 1080x1080)."
	}
	return generic
}
