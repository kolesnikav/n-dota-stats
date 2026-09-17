package corpus_test

import (
	"math"
	"testing"

	"github.com/kolesnikav/n-dota-stats/internal/benchmarks"
	"github.com/kolesnikav/n-dota-stats/internal/corpus"
	"github.com/kolesnikav/n-dota-stats/internal/dota"
)

// match собирает матч из десяти игроков на одном герое с заданным GPM.
// Для проверки квантилей этого достаточно: важно распределение значений.
func match(id int64, heroID int, gpm []int) *dota.Match {
	m := &dota.Match{
		ID: id, Duration: 2400, LobbyType: 7, GameMode: 22,
		RadiantWin: true, Detail: dota.DetailScoreboard,
	}
	for i := 0; i < 10; i++ {
		slot := i
		if i >= 5 {
			slot = 128 + i - 5
		}
		m.Players = append(m.Players, &dota.Player{
			Slot: slot, HeroID: heroID, IsRadiant: i < 5,
			GPM: gpm[i%len(gpm)], XPM: 500, LastHits: 100,
		})
	}
	return m
}

// Кривая должна воспроизводить распределение, которое в неё положили.
func TestCurveMatchesDistribution(t *testing.T) {
	c := corpus.New()
	// 1000 матчей, GPM равномерно от 0 до 999 — медиана обязана лечь около 500.
	for i := 0; i < 1000; i++ {
		gpm := make([]int, 10)
		for j := range gpm {
			gpm[j] = (i*10 + j) % 1000
		}
		if !c.AddMatch(match(int64(i+1), 1, gpm)) {
			t.Fatalf("матч %d не принят", i)
		}
	}
	curve, err := c.Curve(1, "gold_per_min")
	if err != nil {
		t.Fatalf("кривая не построилась: %v", err)
	}
	want := map[float64]float64{0.1: 100, 0.5: 500, 0.9: 900}
	for _, pt := range curve {
		if exp, ok := want[pt[0]]; ok && math.Abs(pt[1]-exp) > 25 {
			t.Errorf("перцентиль %.2f: ждали около %.0f, получили %.1f", pt[0], exp, pt[1])
		}
	}

	// Обратное преобразование: значение из середины должно дать середину шкалы.
	if pct, ok := benchmarks.Percentile(curve, 500); !ok || math.Abs(pct-0.5) > 0.05 {
		t.Errorf("значение 500 должно быть около медианы, получили %v", pct)
	}
}

// Пока игр мало, своей кривой нет — и это не ошибка, а сигнал взять запасную.
func TestCurveNeedsEnoughGames(t *testing.T) {
	c := corpus.New()
	c.AddMatch(match(1, 5, []int{300}))
	if _, err := c.Curve(5, "gold_per_min"); err == nil {
		t.Error("кривая построилась по десяти играм, хотя порог выше")
	}
}

type fakeCurves struct{ called bool }

func (f *fakeCurves) Curve(int, string) ([][2]float64, error) {
	f.called = true
	return [][2]float64{{0.1, 1}, {0.9, 9}}, nil
}

// Blend берёт запасной источник, пока своих игр не хватает, и перестаёт, когда
// хватило.
func TestBlendSwitchesToOwn(t *testing.T) {
	own := corpus.New()
	fb := &fakeCurves{}
	b := corpus.Blend{Own: own, Fallback: fb}

	if _, err := b.Curve(1, "gold_per_min"); err != nil {
		t.Fatalf("запасной источник не сработал: %v", err)
	}
	if !fb.called {
		t.Fatal("к запасному источнику не обратились")
	}

	for i := 0; i < 100; i++ { // 100 матчей × 10 игроков = 1000 игр на герое
		gpm := make([]int, 10)
		for j := range gpm {
			gpm[j] = 200 + i + j
		}
		own.AddMatch(match(int64(i+1), 1, gpm))
	}
	fb.called = false
	curve, err := b.Curve(1, "gold_per_min")
	if err != nil {
		t.Fatalf("своя кривая не отдалась: %v", err)
	}
	if fb.called {
		t.Error("обратились к запасному источнику, хотя своих игр достаточно")
	}
	if len(curve) != len(corpus.Points) {
		t.Errorf("в кривой %d точек, ждали %d", len(curve), len(corpus.Points))
	}
}

// Матчи, которые испортили бы кривые, в корпус попадать не должны.
func TestEligibility(t *testing.T) {
	ok := match(1, 1, []int{400})
	if !corpus.Eligible(ok) {
		t.Error("обычный рейтинговый матч отвергнут")
	}

	turbo := match(2, 1, []int{400})
	turbo.GameMode = 23
	if corpus.Eligible(turbo) {
		t.Error("турбо попал в корпус")
	}

	short := match(3, 1, []int{400})
	short.Duration = 600
	if corpus.Eligible(short) {
		t.Error("десятиминутная сдача попала в корпус")
	}

	left := match(4, 1, []int{400})
	left.Players[3].Abandoned = true
	if corpus.Eligible(left) {
		t.Error("матч с бросившим игроком попал в корпус")
	}
}

// Кольцо не должно расти сверх ёмкости, а счётчик виденного — расти всегда.
func TestRingEviction(t *testing.T) {
	c := corpus.New()
	games := corpus.Capacity/10 + 50 // заведомо больше ёмкости
	for i := 0; i < games; i++ {
		gpm := make([]int, 10)
		for j := range gpm {
			gpm[j] = i + j
		}
		c.AddMatch(match(int64(i+1), 2, gpm))
	}
	if got, want := c.Games(2), int64(games*10); got != want {
		t.Errorf("виденных игр %d, ждали %d", got, want)
	}
	curve, err := c.Curve(2, "gold_per_min")
	if err != nil {
		t.Fatalf("кривая не построилась: %v", err)
	}
	// Вытеснение оставляет последние значения, то есть самые высокие GPM.
	lowest := curve[0][1]
	if lowest < float64(games-corpus.Capacity/10) {
		t.Errorf("старые значения не вытеснены: p10 = %.0f", lowest)
	}
}

// Значения должны переживать запись и чтение без потери.
func TestEncodeDecodeValues(t *testing.T) {
	in := []float32{0, 1.5, 300.25, 99999}
	out := corpus.DecodeValues(corpus.EncodeValues(in))
	if len(out) != len(in) {
		t.Fatalf("длина %d, ждали %d", len(out), len(in))
	}
	for i := range in {
		if in[i] != out[i] {
			t.Errorf("значение %d: %v вместо %v", i, out[i], in[i])
		}
	}
}
