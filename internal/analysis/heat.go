package analysis

import "github.com/kolesnikav/n-dota-stats/internal/dota"

// Тепловая карта: где игрок провёл время.
//
// Карта делится на сетку, по каждой клетке считается, сколько раз герой там
// оказывался, и счёт приводится к пяти уровням от пустого до сплошного. Точки
// в пути идут через равные промежутки, поэтому счёт клетки — это прямо время,
// проведённое в ней, без всяких поправок.
//
// Сетка нечётная: у карты Dota есть смысловой центр — река идёт по диагонали
// через середину, — и центральная клетка не должна попадать на границу.

// HeatSize — сторона сетки.
const HeatSize = 11

// Границы карты в тех же единицах, что и координаты в реплее.
const (
	heatLo   = 64.0
	heatSpan = 128.0
)

// Heat — тепловая карта одного отрезка матча.
type Heat struct {
	// From и To — границы отрезка в секундах.
	From int `json:"from"`
	To   int `json:"to"`
	// Cells — уровни заполнения от 0 до 4, строка за строкой, снизу вверх:
	// Cells[0] это нижний край карты, как на миникарте.
	Cells []int8 `json:"cells"`
	// Deaths — клетки, где игрок умирал.
	Deaths []int16 `json:"deaths,omitempty"`
	// Points — сколько точек пути легло в карту. Ноль означает, что пути нет,
	// а не что игрок стоял на месте.
	Points int `json:"points"`
}

// cell возвращает номер клетки по координатам и признак попадания на карту.
func cell(x, y float64) (int, bool) {
	cx := int((x - heatLo) / heatSpan * HeatSize)
	cy := int((y - heatLo) / heatSpan * HeatSize)
	if cx < 0 || cx >= HeatSize || cy < 0 || cy >= HeatSize {
		return 0, false
	}
	return cy*HeatSize + cx, true
}

// HeatMap строит карту по отрезку матча. to == 0 означает «до конца».
func HeatMap(p *dota.Player, from, to int) Heat {
	h := Heat{From: from, To: to, Cells: make([]int8, HeatSize*HeatSize)}
	counts := make([]int, HeatSize*HeatSize)
	max := 0
	for _, pt := range p.Path {
		if pt.T < from || (to > 0 && pt.T > to) {
			continue
		}
		i, ok := cell(pt.X, pt.Y)
		if !ok {
			continue
		}
		counts[i]++
		h.Points++
		if counts[i] > max {
			max = counts[i]
		}
	}
	if max == 0 {
		return h
	}
	// Пять уровней: пусто и четыре ступени заполнения. Деление по максимуму, а
	// не по общему времени: интересно, где человек бывал чаще всего, а не
	// какая доля матча пришлась на клетку.
	for i, n := range counts {
		if n == 0 {
			continue
		}
		lvl := (n*4 + max - 1) / max
		if lvl > 4 {
			lvl = 4
		}
		h.Cells[i] = int8(lvl)
	}
	for _, d := range p.DeathsAt {
		if d.T < from || (to > 0 && d.T > to) {
			continue
		}
		if i, ok := cell(d.X, d.Y); ok {
			h.Deaths = append(h.Deaths, int16(i))
		}
	}
	return h
}
