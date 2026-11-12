package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"syscall"
	"time"
)

// Timeout de cada entrega.
const Timeout = 5 * time.Second

// Kinds são os tipos de destino.
var Kinds = []string{"pushcut", "macrodroid", "webhook"}

// Hosts esperados de cada app (o webhook genérico aceita qualquer host público).
var kindHosts = map[string]string{"pushcut": "api.pushcut.io", "macrodroid": "trigger.macrodroid.com"}

var (
	ErrBlocked = errors.New("endereço bloqueado: rede interna ou privada")
	ErrBadURL  = errors.New("url inválida")
)

// Sender entrega as notificações. O zero vale: rede pública só, https só, 5 s de prazo.
type Sender struct {
	// AllowLocal aceita localhost, IP privado e qualquer host (só nos testes, com servidor local).
	AllowLocal bool
	// Transport troca o transporte (testes com servidor TLS local). nil = transporte com bloqueio de SSRF.
	Transport http.RoundTripper
	// Resolver confere o DNS na hora de salvar; nil = net.DefaultResolver.
	Resolver interface {
		LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
	}
}

// ValidateURL confere a URL de um destino: https, sem usuário e senha, host do app certo e, depois
// do DNS, só IP público. A mensagem de erro nunca repete a URL (ela é segredo).
func (s *Sender) ValidateURL(ctx context.Context, kind, raw string) error {
	if len(raw) > 2000 {
		return fmt.Errorf("%w: longa demais", ErrBadURL)
	}
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return fmt.Errorf("%w: confira o endereço", ErrBadURL)
	}
	if u.Scheme != "https" {
		return fmt.Errorf("%w: só https", ErrBadURL)
	}
	if u.User != nil {
		return fmt.Errorf("%w: sem usuário e senha no endereço", ErrBadURL)
	}
	if u.Fragment != "" {
		return fmt.Errorf("%w: sem # no endereço", ErrBadURL)
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if s.AllowLocal {
		return nil
	}
	if want, ok := kindHosts[kind]; ok && host != want {
		return fmt.Errorf("%w: o endereço do %s começa com https://%s/", ErrBadURL, kindLabel(kind), want)
	}
	if p := u.Port(); p != "" && p != "443" {
		return fmt.Errorf("%w: só a porta 443", ErrBadURL)
	}
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".internal") {
		return ErrBlocked
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		if blockedIP(ip) {
			return ErrBlocked
		}
		return nil
	}
	var res interface {
		LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
	} = net.DefaultResolver
	if s.Resolver != nil {
		res = s.Resolver
	}
	lctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	ips, err := res.LookupNetIP(lctx, "ip", host)
	if err != nil || len(ips) == 0 {
		return fmt.Errorf("%w: o endereço não foi encontrado (DNS)", ErrBadURL)
	}
	for _, ip := range ips {
		if blockedIP(ip) {
			return ErrBlocked
		}
	}
	return nil
}

func kindLabel(kind string) string {
	switch kind {
	case "pushcut":
		return "Pushcut"
	case "macrodroid":
		return "MacroDroid"
	}
	return "webhook"
}

var blockedPrefixes = func() []netip.Prefix {
	var out []netip.Prefix
	for _, p := range []string{
		"0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8", "169.254.0.0/16", "172.16.0.0/12",
		"192.0.0.0/24", "192.0.2.0/24", "192.88.99.0/24", "192.168.0.0/16", "198.18.0.0/15", "198.51.100.0/24",
		"203.0.113.0/24", "224.0.0.0/4", "240.0.0.0/4", "255.255.255.255/32",
		"::/128", "::1/128", "64:ff9b::/96", "64:ff9b:1::/48", "100::/64", "2001::/32", "2001:db8::/32", "2002::/16",
		"fc00::/7", "fe80::/10", "ff00::/8",
	} {
		out = append(out, netip.MustParsePrefix(p))
	}
	return out
}()

// blockedIP: loopback, privado, link-local, CGNAT, multicast, reservados e os túneis que embutem IPv4.
func blockedIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsValid() || ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return true
	}
	for _, p := range blockedPrefixes {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}

// dialControl roda depois do DNS, com o IP que vai ser conectado de fato: bloqueia rede interna
// mesmo quando o nome resolve diferente na hora de salvar e na hora de enviar (DNS rebinding).
func dialControl(_, address string, _ syscall.RawConn) error {
	ap, err := netip.ParseAddrPort(address)
	if err != nil {
		return ErrBlocked
	}
	if blockedIP(ap.Addr()) {
		return ErrBlocked
	}
	return nil
}

func (s *Sender) client() *http.Client {
	tr := s.Transport
	if tr == nil {
		d := &net.Dialer{Timeout: Timeout, KeepAlive: 30 * time.Second}
		if !s.AllowLocal {
			d.Control = dialControl
		}
		tr = &http.Transport{
			Proxy: nil, DialContext: d.DialContext, ForceAttemptHTTP2: true,
			TLSHandshakeTimeout: Timeout, ResponseHeaderTimeout: Timeout, MaxIdleConns: 10, IdleConnTimeout: 60 * time.Second,
		}
	}
	return &http.Client{
		Transport: tr, Timeout: Timeout,
		// Redirecionamento não é seguido: o destino novo não passou pela checagem.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// Request é o que vai para um destino.
type Request struct {
	Kind  string
	URL   string
	Event string
	Title string
	Text  string
	Data  map[string]string
	At    time.Time
}

// Result é a resposta de uma entrega.
type Result struct {
	StatusCode int
	Retry      bool   // erro de rede, 429 ou 5xx: vale tentar de novo
	Error      string // sem a URL
}

func (r Result) OK() bool { return r.Error == "" }

// Send entrega uma vez, com prazo de 5 s. Nunca devolve a URL no erro.
func (s *Sender) Send(ctx context.Context, rq Request) Result {
	httpReq, err := s.build(ctx, rq)
	if err != nil {
		return Result{Error: err.Error()}
	}
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	resp, err := s.client().Do(httpReq.WithContext(ctx))
	if err != nil {
		return Result{Retry: !errors.Is(err, ErrBlocked), Error: cleanErr(err)}
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 8<<10))
	res := Result{StatusCode: resp.StatusCode}
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
	case resp.StatusCode >= 300 && resp.StatusCode < 400:
		res.Error = fmt.Sprintf("HTTP %d: redirecionamento não é seguido", resp.StatusCode)
	default:
		res.Error = fmt.Sprintf("HTTP %d", resp.StatusCode)
		res.Retry = resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500
	}
	return res
}

func (s *Sender) build(ctx context.Context, rq Request) (*http.Request, error) {
	u, err := url.Parse(rq.URL)
	if err != nil || u.Host == "" {
		return nil, ErrBadURL
	}
	if u.Scheme != "https" && !s.AllowLocal {
		return nil, fmt.Errorf("%w: só https", ErrBadURL)
	}
	var body any
	switch rq.Kind {
	case "pushcut":
		body = map[string]string{"title": rq.Title, "text": rq.Text}
	case "macrodroid":
		q := u.Query()
		q.Set("title", rq.Title)
		q.Set("text", rq.Text)
		u.RawQuery = q.Encode()
		body = map[string]string{"title": rq.Title, "text": rq.Text}
	case "webhook":
		data := rq.Data
		if data == nil {
			data = map[string]string{}
		}
		body = map[string]any{"evento": rq.Event, "dados": data, "titulo": rq.Title, "texto": rq.Text, "enviado_em": rq.At.UTC().Format(time.RFC3339)}
	default:
		return nil, fmt.Errorf("tipo de destino desconhecido")
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(raw))
	if err != nil {
		return nil, ErrBadURL
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "meuanuncio-notificacoes/1")
	return req, nil
}

// cleanErr tira a URL do erro do cliente HTTP (o *url.Error repete o endereço inteiro).
func cleanErr(err error) string {
	if errors.Is(err, ErrBlocked) {
		return ErrBlocked.Error()
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "tempo esgotado (5 s)"
	}
	var ue *url.Error
	if errors.As(err, &ue) {
		if ue.Timeout() {
			return "tempo esgotado (5 s)"
		}
		err = ue.Err
	}
	var ne *net.OpError
	if errors.As(err, &ne) && ne.Err != nil {
		return "falha de conexão: " + ne.Err.Error()
	}
	var dn *net.DNSError
	if errors.As(err, &dn) {
		return "endereço não encontrado (DNS)"
	}
	msg := err.Error()
	if strings.Contains(msg, "://") {
		return "falha de conexão"
	}
	return Clip(msg, 200)
}
