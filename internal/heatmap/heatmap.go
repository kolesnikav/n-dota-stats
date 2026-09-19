// Package heatmap рисует тепловую карту поверх карты Dota.
//
// Плотность считается не по клеткам, а размазыванием каждой точки пути по
// окрестности: клеточная сетка на такой мелкой карте выглядит лесенкой, а
// размазывание даёт то, что глаз и ожидает увидеть на тепловой карте.
package heatmap

import (
	"bytes"
	_ "embed"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"math"
	"sort"

	"github.com/kolesnikav/n-dota-stats/internal/dota"
)

// Карта из набора OpenDota: она нарисована в той же системе координат, что и
// позиции в реплее, — от 64 до 192 по обеим осям, — поэтому точки ложатся на
// неё без подгонки.
//
//go:embed map.jpg
var mapJPEG []byte

// Границы карты в координатах реплея.
const (
	lo   = 64.0
	span = 128.0
)

// Size — сторона готовой картинки.
const Size = 640

// radius — радиус размазывания одной точки в пикселях. Примерно радиус обзора
// героя: меньше даёт рваные пятна, больше смазывает разницу между линией и
// лесом.
//
// contrast — степень сжатия плотности поверх нормировки по перцентилю.
// Единица оставляет шкалу линейной, меньшие значения вытягивают слабые следы.
// 0.8 подобрано глазами: маршрут виден, но не спорит по яркости с местом, где
// человек реально стоял.
const (
	radius   = 22
	contrast = 0.8
)

// Options — что рисовать.
type Options struct {
	Path   []dota.Point
	Deaths []dota.Point
	Kills  []dota.Point
	From   int // начало отрезка в секундах
	To     int // конец, 0 — до конца матча
	Title  string
}

// Render рисует карту и возвращает PNG.
func Render(o Options) ([]byte, error) {
	base, err := jpeg.Decode(bytes.NewReader(mapJPEG))
	if err != nil {
		return nil, fmt.Errorf("карта Dota: %w", err)
	}
	// Картинка карты уже нужного размера, поэтому масштабировать нечего —
	// заодно обходимся стандартной библиотекой.
	if b := base.Bounds(); b.Dx() != Size || b.Dy() != Size {
		return nil, fmt.Errorf("карта Dota размером %dx%d, ожидалось %dx%d", b.Dx(), b.Dy(), Size, Size)
	}
	canvas := image.NewRGBA(image.Rect(0, 0, Size, Size))
	draw.Draw(canvas, canvas.Bounds(), base, base.Bounds().Min, draw.Src)

	density := heat(o)
	if scale := normalizer(density); scale > 0 {
		overlay(canvas, density, scale)
	}
	// Сначала убийства, потом смерти: если и то и другое случилось в одной
	// точке, крест должен лежать сверху — своя смерть важнее.
	for _, k := range o.Kills {
		if !inWindow(k.T, o.From, o.To) {
			continue
		}
		if x, y, ok := toPixel(k.X, k.Y); ok {
			skull(canvas, x, y)
		}
	}
	for _, d := range o.Deaths {
		if !inWindow(d.T, o.From, o.To) {
			continue
		}
		if x, y, ok := toPixel(d.X, d.Y); ok {
			cross(canvas, x, y)
		}
	}

	// JPEG, а не PNG: под картинкой лежит фотография карты, и без потерь она
	// весит впятеро больше при той же различимости пятен.
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, canvas, &jpeg.Options{Quality: 88}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func inWindow(t, from, to int) bool {
	return t >= from && (to <= 0 || t <= to)
}

// toPixel переводит координаты реплея в пиксели картинки.
//
// Ось Y на карте направлена вверх, а в картинке вниз, поэтому она
// переворачивается — иначе карта окажется зеркальной по вертикали.
func toPixel(x, y float64) (int, int, bool) {
	fx := (x - lo) / span
	fy := (y - lo) / span
	if fx < 0 || fx > 1 || fy < 0 || fy > 1 {
		return 0, 0, false
	}
	return int(fx * (Size - 1)), int((1 - fy) * (Size - 1)), true
}

// heat считает плотность присутствия в каждом пикселе.
//
// Каждая точка пути добавляет колокол вокруг себя: вклад падает от единицы в
// центре до нуля на границе радиуса. Так соседние точки складываются в одно
// пятно, а одиночная не превращается в квадрат.
func heat(o Options) []float32 {
	d := make([]float32, Size*Size)
	// Веса колокола считаем один раз: одно и то же ядро кладётся в тысячи мест.
	kernel := make([]float32, (2*radius+1)*(2*radius+1))
	for dy := -radius; dy <= radius; dy++ {
		for dx := -radius; dx <= radius; dx++ {
			r := math.Hypot(float64(dx), float64(dy))
			w := 0.0
			if r < radius {
				w = math.Exp(-2.5 * (r / radius) * (r / radius))
			}
			kernel[(dy+radius)*(2*radius+1)+dx+radius] = float32(w)
		}
	}
	for _, p := range o.Path {
		if !inWindow(p.T, o.From, o.To) {
			continue
		}
		cx, cy, ok := toPixel(p.X, p.Y)
		if !ok {
			continue
		}
		for dy := -radius; dy <= radius; dy++ {
			y := cy + dy
			if y < 0 || y >= Size {
				continue
			}
			row := y * Size
			krow := (dy + radius) * (2*radius + 1)
			for dx := -radius; dx <= radius; dx++ {
				x := cx + dx
				if x < 0 || x >= Size {
					continue
				}
				w := kernel[krow+dx+radius]
				if w == 0 {
					continue
				}
				d[row+x] += w
			}
		}
	}
	return d
}

// normalizer выбирает, какую плотность считать «полной».
//
// По максимуму нормировать нельзя: в самой горячей клетке набирается около
// десятой доли времени, а половина укладывается в дюжину клеток из сотни. При
// делении на пик всё, кроме одного-двух пятен, уходило в почти прозрачное, и
// карта выглядела так, будто человек стоял в одной точке.
//
// Берём девяносто седьмой перцентиль занятых пикселей: всё, что выше,
// упирается в красный. Девяностый пробовали — карту заливало целиком, и
// разница между «стоял» и «прошёл мимо» пропадала.
func normalizer(d []float32) float32 {
	nonzero := make([]float32, 0, len(d)/8)
	for _, v := range d {
		if v > 0 {
			nonzero = append(nonzero, v)
		}
	}
	if len(nonzero) == 0 {
		return 0
	}
	sort.Slice(nonzero, func(i, j int) bool { return nonzero[i] < nonzero[j] })
	return nonzero[len(nonzero)*97/100]
}

// gradient — цвета тепловой карты от редкого к частому.
//
// Шкала идёт от холодного к горячему, как её привыкли читать. Прозрачность
// растёт вместе с цветом: на редких местах карта должна просвечивать, иначе
// непонятно, где вообще находишься.
var gradient = []struct {
	at    float64
	r     uint8
	g     uint8
	b     uint8
	alpha uint8
}{
	{0.00, 0, 0, 0, 0},
	{0.06, 50, 100, 210, 90},
	{0.28, 40, 185, 175, 140},
	{0.52, 205, 215, 60, 175},
	{0.76, 235, 140, 40, 210},
	{1.00, 225, 35, 35, 235},
}

func colorAt(v float64) color.RGBA {
	for i := 1; i < len(gradient); i++ {
		hi := gradient[i]
		if v > hi.at && i != len(gradient)-1 {
			continue
		}
		lo := gradient[i-1]
		k := 0.0
		if hi.at > lo.at {
			k = (v - lo.at) / (hi.at - lo.at)
		}
		if k < 0 {
			k = 0
		}
		if k > 1 {
			k = 1
		}
		mix := func(a, b uint8) uint8 { return uint8(float64(a) + (float64(b)-float64(a))*k) }
		return color.RGBA{mix(lo.r, hi.r), mix(lo.g, hi.g), mix(lo.b, hi.b), mix(lo.alpha, hi.alpha)}
	}
	return color.RGBA{}
}

// overlay накладывает плотность на карту. Всё, что выше выбранного порога,
// упирается в верх шкалы.
func overlay(dst *image.RGBA, density []float32, scale float32) {
	for i, v := range density {
		if v <= 0 {
			continue
		}
		n := math.Pow(float64(v)/float64(scale), contrast)
		if n > 1 {
			n = 1
		}
		c := colorAt(n)
		if c.A == 0 {
			continue
		}
		x, y := i%Size, i/Size
		blend(dst, x, y, c)
	}
}

func blend(dst *image.RGBA, x, y int, c color.RGBA) {
	o := dst.PixOffset(x, y)
	a := float64(c.A) / 255
	dst.Pix[o+0] = uint8(float64(dst.Pix[o+0])*(1-a) + float64(c.R)*a)
	dst.Pix[o+1] = uint8(float64(dst.Pix[o+1])*(1-a) + float64(c.G)*a)
	dst.Pix[o+2] = uint8(float64(dst.Pix[o+2])*(1-a) + float64(c.B)*a)
	dst.Pix[o+3] = 255
}

// cross рисует место смерти. Белая обводка нужна, чтобы крест был виден и на
// красном пятне, и на тёмном лесу.
func cross(dst *image.RGBA, cx, cy int) {
	const arm = 15
	put := func(x, y int, c color.RGBA) {
		if x >= 0 && x < Size && y >= 0 && y < Size {
			blend(dst, x, y, c)
		}
	}
	white := color.RGBA{255, 255, 255, 230}
	black := color.RGBA{20, 20, 20, 200}
	for d := -arm; d <= arm; d++ {
		for _, w := range []int{-2, -1, 1, 2} {
			put(cx+d, cy+d+w, black)
			put(cx+d, cy-d+w, black)
		}
		for _, w := range []int{0, 1} {
			put(cx+d, cy+d+w, white)
			put(cx+d, cy-d+w, white)
		}
	}
}

// skull помечает убитого врага. Кружок, а не крест: крестов на карте и так
// хватает, а разной формы фигуры различаются даже боковым зрением.
func skull(dst *image.RGBA, cx, cy int) {
	const r = 7
	dark := color.RGBA{20, 20, 20, 210}
	light := color.RGBA{250, 250, 250, 235}
	for dy := -r - 2; dy <= r+2; dy++ {
		for dx := -r - 2; dx <= r+2; dx++ {
			x, y := cx+dx, cy+dy
			if x < 0 || x >= Size || y < 0 || y >= Size {
				continue
			}
			d := math.Hypot(float64(dx), float64(dy))
			switch {
			case d <= r-2:
				blend(dst, x, y, light)
			case d <= r+1:
				blend(dst, x, y, dark)
			}
		}
	}
}
