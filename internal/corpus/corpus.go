// Package corpus — собственные перцентили по героям.
//
// OpenDota строит кривые p10…p99 по своему корпусу публичных матчей. Здесь то
// же самое считается на своих данных: по каждой паре «герой + метрика» держим
// выборку последних значений и берём от неё квантили.
//
// Почему кольцевой буфер, а не полная выборка. Перцентили должны отражать то,
// как играют сейчас: после патча значения смещаются, и старые игры только
// портят картину. Кольцо само вытесняет самое давнее, поэтому окно едет за
// патчем без отдельной чистки. Полный резервуар пришлось бы сбрасывать
// вручную, а момент сброса взять неоткуда — номера патчей нам никто не отдаёт.
package corpus

import (
	"encoding/binary"
	"fmt"
	"math"
	"sort"
	"sync"

	"github.com/kolesnikav/n-dota-stats/internal/benchmarks"
	"github.com/kolesnikav/n-dota-stats/internal/dota"
)

// Capacity — сколько последних значений храним по каждой паре «герой+метрика».
// 4000 игр хватает, чтобы p10 и p99 не прыгали: на краю кривой это 40 значений.
const Capacity = 4000

// MinGames — с какого числа игр на герое своей кривой можно верить.
// Ниже этого порога берём снимок OpenDota, пока он есть.
const MinGames = 400

// Points — перцентили, которые публикуем. Те же, что у OpenDota, чтобы кривые
// были взаимозаменяемы и сравнимы.
var Points = []float64{0.1, 0.2, 0.3, 0.4, 0.5, 0.6, 0.7, 0.8, 0.9, 0.95, 0.99}

type key struct {
	hero   int
	metric string
}

// ring — кольцевой буфер значений: заполняется по кругу, вытесняя старые.
type ring struct {
	seen   int64 // всего добавлено за всё время
	values []float32
	next   int // куда писать следующее, когда буфер полон
}

func (r *ring) add(v float64) {
	r.seen++
	if len(r.values) < Capacity {
		r.values = append(r.values, float32(v))
		return
	}
	r.values[r.next] = float32(v)
	r.next = (r.next + 1) % Capacity
}

// Corpus — выборки по всем героям, целиком в памяти.
//
// Влезает: 127 героев × 10 метрик × 4000 значений по 4 байта — около 20 МБ.
// Держать в памяти нужно потому, что сбор идёт сотнями матчей в секунду, и
// чтение-запись в базу на каждое значение была бы на порядки медленнее.
type Corpus struct {
	mu    sync.Mutex
	rings map[key]*ring
	dirty map[key]bool
}

func New() *Corpus {
	return &Corpus{rings: map[key]*ring{}, dirty: map[key]bool{}}
}

// Eligible отсеивает матчи, которые испортили бы кривые.
//
// Турбо и прочие быстрые режимы дают совсем другие золото и опыт в минуту;
// короткие игры — это сдачи, где показатели не успевают сложиться; матч без
// десяти героев разобран не полностью.
func Eligible(m *dota.Match) bool {
	if m == nil || m.Duration < 900 || len(m.Players) != 10 {
		return false
	}
	switch m.LobbyType {
	case 0, 2, 5, 6, 7: // обычный подбор, турнир, командный, кооп-подбор, рейтинг
	default:
		return false
	}
	switch m.GameMode {
	case 1, 2, 3, 4, 5, 16, 22: // all pick, captains, random draft, single draft, all random, CD, ranked AP
	default:
		return false
	}
	for _, p := range m.Players {
		if p.HeroID == 0 || p.Abandoned {
			return false
		}
	}
	return true
}

// AddMatch добавляет всех игроков матча в выборки. Возвращает false, если матч
// в корпус не годится.
func (c *Corpus) AddMatch(m *dota.Match) bool {
	if !Eligible(m) {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, p := range m.Players {
		raw := benchmarks.Raw(m, p)
		for _, metric := range benchmarks.Metrics {
			v, ok := raw[metric]
			if !ok || math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
				continue
			}
			k := key{p.HeroID, metric}
			r := c.rings[k]
			if r == nil {
				r = &ring{}
				c.rings[k] = r
			}
			r.add(v)
			c.dirty[k] = true
		}
	}
	return true
}

// Games — сколько игр набрано по герою. Берём по самой полной метрике: они
// наполняются вместе, но метрика могла появиться в реестре позже остальных.
func (c *Corpus) Games(heroID int) int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	var best int64
	for _, metric := range benchmarks.Metrics {
		if r := c.rings[key{heroID, metric}]; r != nil && r.seen > best {
			best = r.seen
		}
	}
	return best
}

// Ready сообщает, по скольким героям своей кривой уже можно верить.
func (c *Corpus) Ready() (ready, total int) {
	seen := map[int]bool{}
	c.mu.Lock()
	for k := range c.rings {
		seen[k.hero] = true
	}
	c.mu.Unlock()
	for hero := range seen {
		total++
		if c.Games(hero) >= MinGames {
			ready++
		}
	}
	return ready, total
}

// Curve возвращает кривую «перцентиль → значение» по своей выборке.
// Реализует benchmarks.Curves.
func (c *Corpus) Curve(heroID int, metric string) ([][2]float64, error) {
	c.mu.Lock()
	r := c.rings[key{heroID, metric}]
	var vals []float32
	if r != nil {
		vals = append(vals, r.values...)
	}
	c.mu.Unlock()
	if int64(len(vals)) < MinGames {
		return nil, fmt.Errorf("герой %d, %s: набрано %d игр из %d", heroID, metric, len(vals), MinGames)
	}
	sort.Slice(vals, func(i, j int) bool { return vals[i] < vals[j] })
	out := make([][2]float64, 0, len(Points))
	for _, p := range Points {
		out = append(out, [2]float64{p, quantile(vals, p)})
	}
	return out, nil
}

// quantile — линейная интерполяция по отсортированной выборке.
func quantile(sorted []float32, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	pos := p * float64(len(sorted)-1)
	lo := int(math.Floor(pos))
	hi := int(math.Ceil(pos))
	if lo == hi {
		return float64(sorted[lo])
	}
	k := pos - float64(lo)
	return float64(sorted[lo])*(1-k) + float64(sorted[hi])*k
}

// Blend отдаёт свою кривую, когда игр достаточно, и снимок OpenDota, пока нет.
//
// Переход происходит по каждому герою отдельно: на популярных героях свои
// кривые появляются за первый же проход сбора, на редких — позже, и до тех пор
// сводка не должна пустеть.
type Blend struct {
	Own      *Corpus
	Fallback benchmarks.Curves
}

func (b Blend) Curve(heroID int, metric string) ([][2]float64, error) {
	if b.Own != nil {
		if curve, err := b.Own.Curve(heroID, metric); err == nil {
			return curve, nil
		}
	}
	if b.Fallback == nil {
		return nil, fmt.Errorf("нет кривой по герою %d, метрика %s", heroID, metric)
	}
	return b.Fallback.Curve(heroID, metric)
}

// ------------------------------------------------------------------ хранение

// Store — то, что корпусу нужно от базы.
type Store interface {
	LoadCorpus() (map[int]map[string][]float32, map[int]map[string]int64, error)
	SaveCorpusRing(heroID int, metric string, seen int64, values []float32) error
	CorpusHasMatch(matchID int64) bool
	CorpusAddMatch(matchID int64) error
}

// Load поднимает корпус из базы.
func Load(db Store) (*Corpus, error) {
	c := New()
	values, seen, err := db.LoadCorpus()
	if err != nil {
		return nil, err
	}
	for hero, byMetric := range values {
		for metric, vals := range byMetric {
			if len(vals) > Capacity {
				vals = vals[len(vals)-Capacity:]
			}
			c.rings[key{hero, metric}] = &ring{seen: seen[hero][metric], values: vals}
		}
	}
	return c, nil
}

// Flush сохраняет изменившиеся выборки.
func (c *Corpus) Flush(db Store) error {
	c.mu.Lock()
	type item struct {
		k    key
		seen int64
		vals []float32
	}
	var batch []item
	for k := range c.dirty {
		r := c.rings[k]
		if r == nil {
			continue
		}
		batch = append(batch, item{k, r.seen, append([]float32(nil), r.values...)})
	}
	c.dirty = map[key]bool{}
	c.mu.Unlock()

	for _, it := range batch {
		if err := db.SaveCorpusRing(it.k.hero, it.k.metric, it.seen, it.vals); err != nil {
			return err
		}
	}
	return nil
}

// EncodeValues и DecodeValues — формат хранения выборки: подряд идущие float32
// с порядком байт от младшего. Компактнее JSON примерно в шесть раз, а точности
// float32 для перцентилей хватает с запасом.
func EncodeValues(vals []float32) []byte {
	buf := make([]byte, 4*len(vals))
	for i, v := range vals {
		binary.LittleEndian.PutUint32(buf[4*i:], math.Float32bits(v))
	}
	return buf
}

func DecodeValues(buf []byte) []float32 {
	out := make([]float32, len(buf)/4)
	for i := range out {
		out[i] = math.Float32frombits(binary.LittleEndian.Uint32(buf[4*i:]))
	}
	return out
}
