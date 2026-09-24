package httpapi

import (
	"sync"
	"time"
)

// Limiter conta requisições por chave em janelas fixas de um minuto. Fica em memória: basta para
// uma réplica da api e protege o Supabase Auth e a BrasilAPI de abuso pelo cadastro.
type Limiter struct {
	mu      sync.Mutex
	window  time.Time
	counts  map[string]int
	nowFunc func() time.Time
}

func NewLimiter() *Limiter {
	return &Limiter{counts: map[string]int{}, nowFunc: time.Now}
}

// Allow registra uma requisição da chave e diz se ela cabe no limite por minuto.
func (l *Limiter) Allow(key string, perMinute int) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.nowFunc().Truncate(time.Minute)
	if !now.Equal(l.window) {
		l.window = now
		clear(l.counts)
	}
	l.counts[key]++
	return l.counts[key] <= perMinute
}
