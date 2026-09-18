package heatmap

import (
	"bytes"
	"image/jpeg"
	"testing"

	"github.com/kolesnikav/n-dota-stats/internal/dota"
)

// Ориентация должна совпадать с миникартой: восток справа, север сверху.
// Ось Y в координатах растёт вверх, а в картинке вниз — здесь легче всего
// ошибиться и получить зеркальную карту.
func TestPixelOrientation(t *testing.T) {
	cases := []struct {
		name        string
		x, y        float64
		wantLeft    bool
		wantTopHalf bool
	}{
		{"угол Radiant, юго-запад", 66, 66, true, false},
		{"угол Dire, северо-восток", 190, 190, false, true},
		{"северо-запад", 66, 190, true, true},
	}
	for _, c := range cases {
		px, py, ok := toPixel(c.x, c.y)
		if !ok {
			t.Fatalf("%s: точка не попала на карту", c.name)
		}
		if left := px < Size/2; left != c.wantLeft {
			t.Errorf("%s: x=%d, ожидали %s половину", c.name, px, side(c.wantLeft))
		}
		if top := py < Size/2; top != c.wantTopHalf {
			t.Errorf("%s: y=%d, ожидали %s половину", c.name, py, vside(c.wantTopHalf))
		}
	}
	if _, _, ok := toPixel(10, 10); ok {
		t.Error("точка за пределами карты принята")
	}
}

func side(left bool) string {
	if left {
		return "левую"
	}
	return "правую"
}

func vside(top bool) string {
	if top {
		return "верхнюю"
	}
	return "нижнюю"
}

// Картинка должна получаться и быть настоящим JPEG нужного размера.
func TestRender(t *testing.T) {
	var path []dota.Point
	for i := 0; i < 60; i++ {
		path = append(path, dota.Point{T: i * 5, X: 80, Y: 160})
	}
	img, err := Render(Options{
		Path:   path,
		Deaths: []dota.Point{{T: 100, X: 80, Y: 160}},
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(img))
	if err != nil {
		t.Fatalf("не разбирается как JPEG: %v", err)
	}
	if cfg.Width != Size || cfg.Height != Size {
		t.Errorf("размер %dx%d, ждали %dx%d", cfg.Width, cfg.Height, Size, Size)
	}
}

// Пустой путь — не повод падать: реплей мог быть не разобран.
func TestRenderEmpty(t *testing.T) {
	img, err := Render(Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(img) == 0 {
		t.Error("на пустом пути картинка не получилась")
	}
}

// Окно времени должно отсекать точки.
func TestWindow(t *testing.T) {
	if !inWindow(100, 0, 600) {
		t.Error("точка внутри окна отвергнута")
	}
	if inWindow(700, 0, 600) {
		t.Error("точка после окна принята")
	}
	if !inWindow(700, 0, 0) {
		t.Error("нулевой конец окна должен означать «до конца матча»")
	}
}
