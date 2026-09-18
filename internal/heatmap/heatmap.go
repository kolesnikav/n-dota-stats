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
// contrast — степень сжатия плотности. Единица оставила бы видимой только
// самую горячую точку, корень (0.5) вытягивает и совсем редкие следы так, что
// они заливают полкарты. 0.65 — середина, при которой видно и маршрут, и где
// человек реально стоял.
const (
	radius   = 22
	contrast = 0.65
)

// Options — что рисовать.
type Options struct {
	Path   []dota.Point
	Deaths []dota.Point
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

	density, max := heat(o)
	if max > 0 {
		overlay(canvas, density, max)
	}
	for _, d := range o.Deaths {
		if !inWindow(d.T, o.From, o.To) {
			continue
		}
		x, y, ok := toPixel(d.X, d.Y)
		if ok {
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
func heat(o Options) ([]float32, float32) {
	d := make([]float32, Size*Size)
	var max float32
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
				if d[row+x] > max {
					max = d[row+x]
				}
			}
		}
	}
	return d, max
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
	{0.14, 40, 90, 200, 45},
	{0.32, 40, 180, 170, 105},
	{0.55, 200, 210, 60, 160},
	{0.78, 235, 140, 40, 205},
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

// overlay накладывает плотность на карту.
//
// Значения сжимаются корнем: без него видно только самую горячую точку, а всё
// остальное сливается в фон — герой почти всё время стоит в двух-трёх местах.
func overlay(dst *image.RGBA, density []float32, max float32) {
	for i, v := range density {
		if v <= 0 {
			continue
		}
		n := math.Pow(float64(v)/float64(max), contrast)
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
