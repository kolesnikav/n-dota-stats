package analysis_test

import (
	"testing"

	"github.com/kolesnikav/n-dota-stats/internal/analysis"
	"github.com/kolesnikav/n-dota-stats/internal/dota"
)

func at(h analysis.Heat, col, row int) int8 { return h.Cells[row*analysis.HeatSize+col] }

// Ориентация карты должна совпадать с миникартой: восток справа, север сверху.
func TestHeatOrientation(t *testing.T) {
	p := &dota.Player{Path: []dota.Point{
		{T: 0, X: 66, Y: 66},   // юго-запад, угол Radiant
		{T: 5, X: 190, Y: 190}, // северо-восток, угол Dire
	}}
	h := analysis.HeatMap(p, 0, 0)
	if at(h, 0, 0) == 0 {
		t.Error("юго-западный угол пуст")
	}
	if at(h, analysis.HeatSize-1, analysis.HeatSize-1) == 0 {
		t.Error("северо-восточный угол пуст")
	}
	if h.Points != 2 {
		t.Errorf("точек %d, ждали 2", h.Points)
	}
}

// Уровень заполнения считается от самой посещаемой клетки.
func TestHeatLevels(t *testing.T) {
	var path []dota.Point
	for i := 0; i < 8; i++ {
		path = append(path, dota.Point{T: i * 5, X: 70, Y: 70}) // любимая клетка
	}
	path = append(path, dota.Point{T: 100, X: 186, Y: 186}) // заглянул один раз
	h := analysis.HeatMap(&dota.Player{Path: path}, 0, 0)
	if got := at(h, 0, 0); got != 4 {
		t.Errorf("самая посещаемая клетка имеет уровень %d, ждали 4", got)
	}
	if got := at(h, analysis.HeatSize-1, analysis.HeatSize-1); got != 1 {
		t.Errorf("случайная клетка имеет уровень %d, ждали 1", got)
	}
}

// Отрезок времени должен отсекать и точки пути, и смерти.
func TestHeatWindow(t *testing.T) {
	p := &dota.Player{
		Path: []dota.Point{
			{T: 100, X: 70, Y: 70},
			{T: 900, X: 186, Y: 186},
		},
		DeathsAt: []dota.Point{
			{T: 120, X: 70, Y: 70},
			{T: 950, X: 186, Y: 186},
		},
	}
	h := analysis.HeatMap(p, 0, 600)
	if h.Points != 1 {
		t.Errorf("в окно попало %d точек, ждали 1", h.Points)
	}
	if len(h.Deaths) != 1 {
		t.Errorf("в окно попало %d смертей, ждали 1", len(h.Deaths))
	}
	if at(h, analysis.HeatSize-1, analysis.HeatSize-1) != 0 {
		t.Error("точка вне окна попала на карту")
	}
}

// Пустой путь — это отсутствие данных, а не стояние на месте.
func TestHeatEmpty(t *testing.T) {
	h := analysis.HeatMap(&dota.Player{}, 0, 0)
	if h.Points != 0 {
		t.Errorf("точек %d, ждали 0", h.Points)
	}
	for _, c := range h.Cells {
		if c != 0 {
			t.Fatal("на пустом пути карта не должна быть заполнена")
		}
	}
}
