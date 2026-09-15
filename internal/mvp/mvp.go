// Package mvp — модель «лучшего игрока матча».
//
// Идея: сравнивать игрока не с другими игроками матча напрямую, а с тысячами
// игр на ТОМ ЖЕ герое. Перцентили по герою берутся из снимка benchmarks.
// Именно эта нормировка воспроизвела экран Dota в матче 8999344582
// (Winter Wyvern → Sniper → Naga Siren, включая порядок), тогда как по сырым
// числам первой была бы Naga Siren.
//
// Веса уточняются на разметке пользователя условным логитом: внутри матча
// игроки конкурируют за одно место MVP, вероятность выбрать игрока — softmax
// от взвешенной суммы его перцентилей.
package mvp

import (
	"math"
	"sort"

	"github.com/kolesnikav/n-dota-stats/internal/dota"
)

// Feature — один признак модели.
type Feature struct {
	Key    string // ключ в benchmarks
	Label  string
	Invert bool // меньше значит лучше
}

// Features — признаки модели. hero_healing_per_min сознательно исключён:
// у большинства героев лечение нулевое и перцентиль там вырожденный.
var Features = []Feature{
	{Key: "gold_per_min", Label: "золото/мин"},
	{Key: "xp_per_min", Label: "опыт/мин"},
	{Key: "kills_per_min", Label: "убийства"},
	{Key: "assists_per_min", Label: "помощь"},
	{Key: "last_hits_per_min", Label: "добивания"},
	{Key: "hero_damage_per_min", Label: "урон по героям"},
	{Key: "tower_damage", Label: "урон по строениям"},
	{Key: "deaths_per_min", Label: "смерти", Invert: true},
}

// Dim — размерность вектора признаков.
func Dim() int { return len(Features) }

// EqualWeights — стартовые веса: все признаки равны.
func EqualWeights() []float64 {
	w := make([]float64, len(Features))
	for i := range w {
		w[i] = 1
	}
	return w
}

// Vector строит вектор перцентилей игрока. Отсутствующий показатель — 0.5,
// то есть нейтрально.
func Vector(p *dota.Player) []float64 {
	vec := make([]float64, len(Features))
	for i, f := range Features {
		v, ok := p.Benchmarks[f.Key]
		if !ok {
			v = 0.5
		}
		if f.Invert {
			v = 1 - v
		}
		vec[i] = v
	}
	return vec
}

// Score — взвешенное среднее признаков, 0..1 при неотрицательных весах.
func Score(weights, vec []float64) float64 {
	if len(weights) != len(vec) {
		return 0
	}
	var sum, total float64
	for i := range vec {
		sum += weights[i] * vec[i]
		total += weights[i]
	}
	if total == 0 {
		return 0
	}
	return sum / total
}

// Scored — игрок с посчитанной оценкой.
type Scored struct {
	Player *dota.Player
	Vector []float64
	Score  float64
}

// Rank сортирует игроков матча по убыванию оценки.
func Rank(m *dota.Match, weights []float64) []Scored {
	if len(weights) != len(Features) {
		weights = EqualWeights()
	}
	out := make([]Scored, 0, len(m.Players))
	for _, p := range m.Players {
		vec := Vector(p)
		out = append(out, Scored{Player: p, Vector: vec, Score: Score(weights, vec)})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	return out
}

// Place возвращает место игрока (с единицы) в готовом рейтинге.
func Place(ranked []Scored, p *dota.Player) int {
	for i, s := range ranked {
		if s.Player == p {
			return i + 1
		}
	}
	return 0
}

// Sample — один размеченный матч для обучения.
type Sample struct {
	Vectors [][]float64 // по игроку
	Slots   []int
	MVPSlot int // слот игрока, которого Dota показала лучшим
}

func (s Sample) target() int {
	for i, slot := range s.Slots {
		if slot == s.MVPSlot {
			return i
		}
	}
	return -1
}

func softmax(scores []float64) []float64 {
	top := math.Inf(-1)
	for _, s := range scores {
		if s > top {
			top = s
		}
	}
	out := make([]float64, len(scores))
	var sum float64
	for i, s := range scores {
		out[i] = math.Exp(s - top)
		sum += out[i]
	}
	if sum == 0 {
		return out
	}
	for i := range out {
		out[i] /= sum
	}
	return out
}

// Train подбирает веса градиентным подъёмом по лог-правдоподобию с L2.
// Возвращает веса и среднее лог-правдоподобие.
func Train(samples []Sample, l2 float64, steps int, lr float64) ([]float64, float64) {
	dim := len(Features)
	usable := make([]Sample, 0, len(samples))
	for _, s := range samples {
		if s.target() >= 0 && len(s.Vectors) > 1 {
			usable = append(usable, s)
		}
	}
	w := EqualWeights()
	if len(usable) == 0 {
		return w, 0
	}
	for step := 0; step < steps; step++ {
		grad := make([]float64, dim)
		for _, s := range usable {
			scores := make([]float64, len(s.Vectors))
			for i, v := range s.Vectors {
				var acc float64
				for j := range v {
					acc += w[j] * v[j]
				}
				scores[i] = acc
			}
			probs := softmax(scores)
			t := s.target()
			for j := 0; j < dim; j++ {
				var expected float64
				for i, v := range s.Vectors {
					expected += probs[i] * v[j]
				}
				grad[j] += s.Vectors[t][j] - expected
			}
		}
		for j := 0; j < dim; j++ {
			grad[j] = grad[j]/float64(len(usable)) - l2*w[j]
			w[j] += lr * grad[j]
		}
	}
	var ll float64
	for _, s := range usable {
		scores := make([]float64, len(s.Vectors))
		for i, v := range s.Vectors {
			var acc float64
			for j := range v {
				acc += w[j] * v[j]
			}
			scores[i] = acc
		}
		p := softmax(scores)[s.target()]
		ll += math.Log(math.Max(p, 1e-12))
	}
	return w, ll / float64(len(usable))
}

// Accuracy считает, как часто модель угадывает MVP точно и попадает в тройку.
func Accuracy(samples []Sample, weights []float64) (top1, top3, total int) {
	for _, s := range samples {
		if s.target() < 0 {
			continue
		}
		type row struct {
			slot  int
			score float64
		}
		rows := make([]row, 0, len(s.Vectors))
		for i, v := range s.Vectors {
			var acc float64
			for j := range v {
				if j < len(weights) {
					acc += weights[j] * v[j]
				}
			}
			rows = append(rows, row{s.Slots[i], acc})
		}
		sort.SliceStable(rows, func(i, j int) bool { return rows[i].score > rows[j].score })
		total++
		if rows[0].slot == s.MVPSlot {
			top1++
		}
		for i := 0; i < 3 && i < len(rows); i++ {
			if rows[i].slot == s.MVPSlot {
				top3++
				break
			}
		}
	}
	return
}

// Describe раскладывает веса по долям для показа пользователю.
type WeightShare struct {
	Label string
	Raw   float64
	Share float64 // проценты от суммы модулей
}

func Describe(weights []float64) []WeightShare {
	var total float64
	for _, w := range weights {
		total += math.Abs(w)
	}
	if total == 0 {
		total = 1
	}
	out := make([]WeightShare, 0, len(Features))
	for i, f := range Features {
		if i >= len(weights) {
			break
		}
		label := f.Label
		if f.Invert {
			label += " (меньше=лучше)"
		}
		out = append(out, WeightShare{Label: label, Raw: weights[i], Share: 100 * weights[i] / total})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Raw > out[j].Raw })
	return out
}
