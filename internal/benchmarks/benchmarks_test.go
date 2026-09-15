package benchmarks_test

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/kolesnikav/n-dota-stats/internal/benchmarks"
	"github.com/kolesnikav/n-dota-stats/internal/fixture"
)

// curves читает сохранённый ответ /api/benchmarks?hero_id=60 (Night Stalker).
type curves map[string][][2]float64

func loadCurves(t *testing.T) curves {
	t.Helper()
	raw, err := fixture.Raw("benchmarks_hero60.json")
	if err != nil {
		t.Fatalf("фикстура бенчмарков: %v", err)
	}
	var doc struct {
		Result map[string][]struct {
			Percentile float64 `json:"percentile"`
			Value      float64 `json:"value"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("разбор бенчмарков: %v", err)
	}
	out := curves{}
	for metric, points := range doc.Result {
		for _, p := range points {
			out[metric] = append(out[metric], [2]float64{p.Percentile, p.Value})
		}
	}
	return out
}

func (c curves) Curve(heroID int, metric string) ([][2]float64, error) { return c[metric], nil }

// Наш обратный пересчёт перцентиля должен сходиться с тем, что OpenDota
// посчитала сама и положила в матч.
func TestPercentileMatchesOpenDota(t *testing.T) {
	c := loadCurves(t)
	m, err := fixture.Match8999344582()
	if err != nil {
		t.Fatalf("фикстура: %v", err)
	}
	ns := m.FindByHero("Night Stalker")
	if ns == nil {
		t.Fatal("в матче нет Night Stalker")
	}
	raw := benchmarks.Raw(m, ns)

	checked := 0
	for _, metric := range benchmarks.Metrics {
		want, ok := ns.Benchmarks[metric]
		if !ok || len(c[metric]) < 2 || benchmarks.Degenerate[metric] {
			continue
		}
		got, ok := benchmarks.Percentile(c[metric], raw[metric])
		if !ok {
			t.Errorf("%s: перцентиль не посчитался", metric)
			continue
		}
		if diff := math.Abs(got-want) * 100; diff > 6 {
			t.Errorf("%s: наш перцентиль %.0f, у OpenDota %.0f (расхождение %.0f пунктов)",
				metric, got*100, want*100, diff)
		}
		checked++
	}
	if checked < 5 {
		t.Fatalf("проверено всего %d метрик — фикстура не та", checked)
	}
}

func TestPercentileOutsideCurve(t *testing.T) {
	curve := [][2]float64{{0.1, 100}, {0.5, 200}, {0.9, 300}}
	// ниже первой точки достраиваем отрезок от нуля: 10 из 100 — это десятая
	// часть пути до p10, то есть примерно первый перцентиль
	if p, _ := benchmarks.Percentile(curve, 10); math.Abs(p-0.01) > 1e-9 {
		t.Errorf("ниже кривой ждали 0.01, получили %v", p)
	}
	if p, _ := benchmarks.Percentile(curve, 1000); p != 0.9 {
		t.Errorf("выше кривой ждали 0.9, получили %v", p)
	}
	if p, _ := benchmarks.Percentile(curve, 150); math.Abs(p-0.3) > 1e-9 {
		t.Errorf("середина отрезка должна дать 0.3, получили %v", p)
	}
}

// Apply не должен затирать перцентили, пришедшие из источника готовыми.
func TestApplyKeepsExistingBenchmarks(t *testing.T) {
	c := loadCurves(t)
	m, _ := fixture.Match8999344582()
	ns := m.FindByHero("Night Stalker")
	before := ns.Benchmarks["gold_per_min"]
	benchmarks.Apply(c, m)
	if ns.Benchmarks["gold_per_min"] != before {
		t.Error("готовые перцентили перезаписаны")
	}
}

func TestApplyFillsEmptyBenchmarks(t *testing.T) {
	c := loadCurves(t)
	m, _ := fixture.Match8999344582()
	ns := m.FindByHero("Night Stalker")
	ns.Benchmarks = nil
	benchmarks.Apply(c, m)
	if len(ns.Benchmarks) == 0 {
		t.Fatal("перцентили не заполнились")
	}
	if _, ok := ns.Benchmarks["gold_per_min"]; !ok {
		t.Error("нет перцентиля по золоту")
	}
}
