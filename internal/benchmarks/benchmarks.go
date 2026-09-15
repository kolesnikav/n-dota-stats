// Package benchmarks — перцентили игрока относительно того же героя.
//
// OpenDota отдаёт кривую «значение на перцентиле» (p10…p99). Нам нужно обратное
// преобразование: по значению игрока найти его перцентиль. Делаем линейной
// интерполяцией внутри кривой и зажимаем края.
package benchmarks

import (
	"time"

	"github.com/kolesnikav/n-dota-stats/internal/dota"
	"github.com/kolesnikav/n-dota-stats/internal/odota"
)

// Curves — источник кривых по герою и метрике.
type Curves interface {
	Curve(heroID int, metric string) ([][2]float64, error)
}

// Metrics — метрики, по которым OpenDota публикует кривые.
var Metrics = []string{
	"gold_per_min", "xp_per_min", "kills_per_min", "deaths_per_min",
	"assists_per_min", "last_hits_per_min", "denies_per_min",
	"hero_damage_per_min", "hero_healing_per_min", "tower_damage",
}

// Degenerate — метрики, у которых перцентиль почти ничего не значит: у
// большинства героев значение нулевое, и ноль получает высокий перцентиль
// просто потому, что таких же нулей много. В модель MVP они не входят.
var Degenerate = map[string]bool{"hero_healing_per_min": true}

// Percentile возвращает долю игроков с меньшим значением, 0..1.
func Percentile(curve [][2]float64, value float64) (float64, bool) {
	if len(curve) < 2 {
		return 0, false
	}
	if value <= curve[0][1] {
		// Ниже первой точки кривой (p10) OpenDota перцентиль не публикует.
		// Достраиваем отрезок от нуля: все метрики неотрицательны, и ноль —
		// это нижняя граница шкалы.
		if curve[0][1] <= 0 {
			return 0, true
		}
		return curve[0][0] * value / curve[0][1], true
	}
	last := curve[len(curve)-1]
	if value >= last[1] {
		return last[0], true
	}
	for i := 1; i < len(curve); i++ {
		lo, hi := curve[i-1], curve[i]
		if value <= hi[1] {
			span := hi[1] - lo[1]
			if span <= 0 {
				return hi[0], true
			}
			k := (value - lo[1]) / span
			return lo[0] + k*(hi[0]-lo[0]), true
		}
	}
	return last[0], true
}

// Raw считает сырые значения метрик игрока в тех же единицах, что и кривые.
func Raw(m *dota.Match, p *dota.Player) map[string]float64 {
	minutes := m.DurationMinutes()
	return map[string]float64{
		"gold_per_min":         float64(p.GPM),
		"xp_per_min":           float64(p.XPM),
		"kills_per_min":        float64(p.Kills) / minutes,
		"deaths_per_min":       float64(p.Deaths) / minutes,
		"assists_per_min":      float64(p.Assists) / minutes,
		"last_hits_per_min":    float64(p.LastHits) / minutes,
		"denies_per_min":       float64(p.Denies) / minutes,
		"hero_damage_per_min":  float64(p.HeroDamage) / minutes,
		"hero_healing_per_min": float64(p.HeroHealing) / minutes,
		"tower_damage":         float64(p.TowerDamage),
	}
}

// Apply заполняет перцентили всем игрокам матча из снимка кривых.
// Уже заполненные значения (например, пришедшие готовыми из OpenDota) не трогаем.
func Apply(c Curves, m *dota.Match) {
	if c == nil {
		return
	}
	for _, p := range m.Players {
		if len(p.Benchmarks) > 0 {
			continue
		}
		raw := Raw(m, p)
		out := make(map[string]float64, len(Metrics))
		for _, metric := range Metrics {
			curve, err := c.Curve(p.HeroID, metric)
			if err != nil || len(curve) < 2 {
				continue
			}
			if pct, ok := Percentile(curve, raw[metric]); ok {
				out[metric] = pct
			}
		}
		if len(out) > 0 {
			p.Benchmarks = out
		}
	}
}

// Saver сохраняет снимок кривых.
type Saver interface {
	SaveBenchmarks(heroID int, curves map[string][]struct {
		Percentile float64
		Value      float64
	}) error
	BenchmarksFetchedAt() int64
	BenchmarksHeroCount() int
}

// Refresh обновляет снимок по всем героям.
//
// pause — дополнительная пауза между героями поверх троттлинга клиента. Это
// фоновая задача, и она не должна занимать всю квоту: пользователь, который
// прямо сейчас подключается или просит разбор матча, важнее снимка.
func Refresh(cli *odota.Client, db Saver, heroIDs []int, pause time.Duration, log func(string, ...any)) {
	for i, id := range heroIDs {
		if i > 0 && pause > 0 {
			time.Sleep(pause)
		}
		curves, err := cli.Benchmarks(id)
		if err != nil {
			log("бенчмарки героя %d: %v", id, err)
			continue
		}
		conv := make(map[string][]struct {
			Percentile float64
			Value      float64
		}, len(curves))
		for metric, points := range curves {
			for _, pt := range points {
				conv[metric] = append(conv[metric], struct {
					Percentile float64
					Value      float64
				}{pt.Percentile, pt.Value})
			}
		}
		if err := db.SaveBenchmarks(id, conv); err != nil {
			log("сохранение бенчмарков героя %d: %v", id, err)
		}
	}
}

// Stale сообщает, что снимок пора обновить: он старше суток или неполный
// (прошлый проход мог оборваться на середине из-за ограничения частоты).
func Stale(db Saver) bool {
	at := db.BenchmarksFetchedAt()
	if at == 0 || time.Since(time.Unix(at, 0)) > 24*time.Hour {
		return true
	}
	return db.BenchmarksHeroCount() < 100
}
