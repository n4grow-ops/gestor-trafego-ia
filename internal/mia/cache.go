package mia

import (
	"context"
	"errors"
	"sync"
	"time"
)

// ErrNoConfig: o banco ainda não tem configuração salva (vale o padrão).
var ErrNoConfig = errors.New("mia: sem configuração salva")

// Cache guarda a configuração lida do banco por TTL. Sem configuração, vale Defaults(); se o banco
// falhar, continua a última boa (ou o padrão). Um *Cache nil devolve sempre o padrão.
type Cache struct {
	Load func(ctx context.Context) (Config, error)
	TTL  time.Duration
	Now  func() time.Time
	// OnError recebe falhas de leitura (para log); opcional.
	OnError func(error)

	mu  sync.Mutex
	cfg *Config
	at  time.Time
}

func (c *Cache) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

// Get devolve a configuração em vigor.
func (c *Cache) Get(ctx context.Context) Config {
	if c == nil || c.Load == nil {
		return Defaults()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	ttl := c.TTL
	if ttl <= 0 {
		ttl = 30 * time.Second
	}
	if c.cfg != nil && c.now().Sub(c.at) < ttl {
		return *c.cfg
	}
	cfg, err := c.Load(ctx)
	switch {
	case errors.Is(err, ErrNoConfig):
		cfg = Defaults()
	case err != nil:
		if c.OnError != nil {
			c.OnError(err)
		}
		c.at = c.now() // não martela o banco fora do ar
		if c.cfg != nil {
			return *c.cfg
		}
		d := Defaults()
		c.cfg = &d
		return d
	default:
		cfg = cfg.Normalize()
	}
	c.cfg, c.at = &cfg, c.now()
	return cfg
}

// Invalidate força a próxima leitura (usado pelo painel depois de salvar, no mesmo processo).
func (c *Cache) Invalidate() {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.cfg = nil
	c.mu.Unlock()
}
