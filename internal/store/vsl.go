package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// VSLBuckets é o número de faixas do vídeo (de 5% em 5%) guardadas em watched_mask.
const VSLBuckets = 20

// VSLEvent é o que a página inicial manda enquanto a pessoa vê a VSL. Os números vêm do navegador:
// servem só para a estatística, nada é liberado por eles.
type VSLEvent struct {
	Session  string // uuid
	Visitor  string // uuid
	VideoID  string // definido pelo servidor, não pela página
	Device   string // "", mobile, tablet, desktop (do User-Agent)
	Source   string
	Played   bool
	CTA      bool
	Duration int
	Max      int
	Mask     int
	Watched  int // segundos assistidos de fato
}

// RecordVSL grava ou atualiza a sessão. Os contadores só crescem: um evento atrasado ou repetido não
// apaga o que já foi visto. Sessão de outro visitante (uuid adivinhado) não é mexida.
func (s *Store) RecordVSL(ctx context.Context, ev VSLEvent, now time.Time) error {
	var played, cta *time.Time
	if ev.Played {
		played = &now
	}
	if ev.CTA {
		cta = &now
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO vsl_views (session_id, visitor_id, video_id, device, source, played_at, duration_s, max_s, watched_mask, cta_at, started_at, last_seen_at, watched_s)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $11, $12)
		ON CONFLICT (session_id) DO UPDATE SET
			played_at = coalesce(vsl_views.played_at, EXCLUDED.played_at),
			duration_s = greatest(vsl_views.duration_s, EXCLUDED.duration_s),
			max_s = greatest(vsl_views.max_s, EXCLUDED.max_s),
			watched_mask = vsl_views.watched_mask | EXCLUDED.watched_mask,
			watched_s = greatest(vsl_views.watched_s, EXCLUDED.watched_s),
			cta_at = coalesce(vsl_views.cta_at, EXCLUDED.cta_at),
			last_seen_at = EXCLUDED.last_seen_at
		WHERE vsl_views.visitor_id = EXCLUDED.visitor_id AND vsl_views.video_id = EXCLUDED.video_id`,
		ev.Session, ev.Visitor, ev.VideoID, ev.Device, ev.Source, played, ev.Duration, ev.Max, ev.Mask, cta, now, ev.Watched)
	return err
}

// VSLDay são as aberturas e plays de um dia (fuso de Brasília).
type VSLDay struct {
	Day   string `json:"day"`
	Views int    `json:"views"`
	Plays int    `json:"plays"`
}

// VSLSession é uma linha da lista de quem viu.
type VSLSession struct {
	StartedAt  time.Time `json:"started_at"`
	LastSeenAt time.Time `json:"last_seen_at"`
	Device     string    `json:"device"`
	Source     string    `json:"source"`
	Played     bool      `json:"played"`
	MaxPct     int       `json:"max_pct"`     // ponto mais longe, em %
	WatchedPct int       `json:"watched_pct"` // quanto do vídeo de fato passou na tela, em %
	WatchedS   int       `json:"watched_s"`
	MaxS       int       `json:"max_s"`
	CTA        bool      `json:"cta"`
	Returning  bool      `json:"returning"` // o mesmo navegador já tinha aberto antes
}

// VSLStats é o que a aba VSL do admin mostra.
type VSLStats struct {
	VideoID      string         `json:"video_id"`
	Days         int            `json:"days"`
	Views        int            `json:"views"`
	Visitors     int            `json:"visitors"`
	Plays        int            `json:"plays"`
	PlayVisitors int            `json:"play_visitors"`
	CTA          int            `json:"cta"`
	CTAPlayed    int            `json:"cta_played"`
	DurationS    int            `json:"duration_s"`
	AvgWatchedS  int            `json:"avg_watched_s"`
	Retention    []int          `json:"retention"`  // por faixa de 5%: quantos plays assistiram aquela faixa
	Milestones   map[string]int `json:"milestones"` // 25, 50, 75, 100: quantos chegaram lá
	Series       []VSLDay       `json:"series"`
	Devices      map[string]int `json:"devices"` // plays por aparelho
	Recent       []VSLSession   `json:"recent"`
}

// VSLStats junta os números dos últimos `days` dias do vídeo.
func (s *Store) VSLStats(ctx context.Context, videoID string, days int, now time.Time) (VSLStats, error) {
	st := VSLStats{VideoID: videoID, Days: days, Retention: make([]int, VSLBuckets), Milestones: map[string]int{}, Devices: map[string]int{}}
	since := now.AddDate(0, 0, -days)
	err := s.pool.QueryRow(ctx, `SELECT count(*), count(DISTINCT visitor_id), count(played_at), count(DISTINCT visitor_id) FILTER (WHERE played_at IS NOT NULL),
			count(cta_at), count(cta_at) FILTER (WHERE played_at IS NOT NULL), coalesce(max(duration_s), 0),
			coalesce(round(avg(watched_s) FILTER (WHERE played_at IS NOT NULL)), 0)::int
		FROM vsl_views WHERE video_id = $1 AND started_at >= $2`, videoID, since).
		Scan(&st.Views, &st.Visitors, &st.Plays, &st.PlayVisitors, &st.CTA, &st.CTAPlayed, &st.DurationS, &st.AvgWatchedS)
	if err != nil {
		return st, err
	}

	rows, err := s.pool.Query(ctx, `SELECT watched_mask, device FROM vsl_views
		WHERE video_id = $1 AND started_at >= $2 AND played_at IS NOT NULL`, videoID, since)
	if err != nil {
		return st, err
	}
	for rows.Next() {
		var mask int
		var device string
		if err := rows.Scan(&mask, &device); err != nil {
			rows.Close()
			return st, err
		}
		for i := range VSLBuckets {
			if mask&(1<<i) != 0 {
				st.Retention[i]++
			}
		}
		if device == "" {
			device = "outro"
		}
		st.Devices[device]++
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return st, err
	}
	// Chegou a X% = assistiu a faixa que começa em X% (a última, 95%, vale como "até o fim").
	st.Milestones["25"], st.Milestones["50"], st.Milestones["75"], st.Milestones["100"] = st.Retention[5], st.Retention[10], st.Retention[15], st.Retention[19]

	rows, err = s.pool.Query(ctx, `SELECT to_char(d, 'YYYY-MM-DD'),
			count(v.id), count(v.played_at)
		FROM generate_series(date_trunc('day', $2::timestamptz AT TIME ZONE 'America/Sao_Paulo') - make_interval(days => $3 - 1),
			date_trunc('day', $2::timestamptz AT TIME ZONE 'America/Sao_Paulo'), interval '1 day') d
		LEFT JOIN vsl_views v ON v.video_id = $1 AND date_trunc('day', v.started_at AT TIME ZONE 'America/Sao_Paulo') = d
		GROUP BY d ORDER BY d`, videoID, now, days)
	if err != nil {
		return st, err
	}
	if st.Series, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (VSLDay, error) {
		var d VSLDay
		return d, r.Scan(&d.Day, &d.Views, &d.Plays)
	}); err != nil {
		return st, err
	}

	rows, err = s.pool.Query(ctx, `SELECT v.started_at, v.last_seen_at, v.device, v.source, v.played_at IS NOT NULL, v.duration_s, v.max_s, v.watched_s, v.cta_at IS NOT NULL,
			EXISTS (SELECT 1 FROM vsl_views o WHERE o.visitor_id = v.visitor_id AND o.started_at < v.started_at)
		FROM vsl_views v WHERE v.video_id = $1 AND v.started_at >= $2 AND v.played_at IS NOT NULL
		ORDER BY v.started_at DESC LIMIT 50`, videoID, since)
	if err != nil {
		return st, err
	}
	st.Recent, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (VSLSession, error) {
		var v VSLSession
		var dur int
		if err := r.Scan(&v.StartedAt, &v.LastSeenAt, &v.Device, &v.Source, &v.Played, &dur, &v.MaxS, &v.WatchedS, &v.CTA, &v.Returning); err != nil {
			return v, err
		}
		if dur > 0 {
			v.MaxPct = min(100, v.MaxS*100/dur)
			v.WatchedPct = min(100, v.WatchedS*100/dur)
		}
		return v, nil
	})
	return st, err
}
