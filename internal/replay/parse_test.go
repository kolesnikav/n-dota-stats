package replay_test

import (
	"os"
	"testing"

	"github.com/kolesnikav/n-dota-stats/internal/fixture"
	"github.com/kolesnikav/n-dota-stats/internal/replay"
)

// Разбор полного реплея проверяется только когда путь к .dem задан явно:
// файл весит десятки мегабайт и в репозиторий не кладётся.
//
//	TEST_REPLAY_PATH=/путь/8999344582.dem go test ./internal/replay/
func TestParseAgainstOpenDota(t *testing.T) {
	path := os.Getenv("TEST_REPLAY_PATH")
	if path == "" {
		t.Skip("не задан TEST_REPLAY_PATH")
	}
	m, err := fixture.Match8999344582()
	if err != nil {
		t.Fatalf("фикстура: %v", err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("реплей: %v", err)
	}
	defer f.Close()

	res, err := replay.Parse(f, m)
	if err != nil {
		t.Fatalf("разбор: %v", err)
	}
	if len(res.Players) != 10 {
		t.Fatalf("игроков разобрано: %d", len(res.Players))
	}

	for _, p := range m.Players {
		ps, ok := res.Players[p.Slot]
		if !ok {
			t.Errorf("%s: нет данных", p.Name())
			continue
		}
		if ps.ObsPlaced != p.ObsPlaced {
			t.Errorf("%s: обзорных %d, у OpenDota %d", p.Name(), ps.ObsPlaced, p.ObsPlaced)
		}
		if ps.SenPlaced != p.SenPlaced {
			t.Errorf("%s: сентри %d, у OpenDota %d", p.Name(), ps.SenPlaced, p.SenPlaced)
		}
		if ps.ObsKilled != p.ObsKilled {
			t.Errorf("%s: снято чужих обзорных %d, у OpenDota %d", p.Name(), ps.ObsKilled, p.ObsKilled)
		}
		// Проверка обязана падать, когда наших данных нет: раньше она их молча
		// пропускала и не заметила, что у одной из команд кривые пустые.
		minutes := m.Duration / 60
		if len(ps.LHT) < minutes {
			t.Errorf("%s: точек в кривой добиваний %d, а матч идёт %d минут",
				p.Name(), len(ps.LHT), minutes)
		}
		if len(p.LHT) > 10 {
			if len(ps.LHT) <= 10 {
				t.Errorf("%s: нет добиваний к 10:00", p.Name())
			} else if diff := ps.LHT[10] - p.LHT[10]; diff > 2 || diff < -2 {
				t.Errorf("%s: добиваний к 10:00 %d, у OpenDota %d", p.Name(), ps.LHT[10], p.LHT[10])
			}
		}
		if len(p.GoldT) > 10 {
			if len(ps.GoldT) <= 10 {
				t.Errorf("%s: нет золота к 10:00", p.Name())
			} else if got, want := ps.GoldT[10], p.GoldT[10]; got-want > want/10 || want-got > want/10 {
				t.Errorf("%s: золота к 10:00 %d, у OpenDota %d", p.Name(), got, want)
			}
		}
		if p.Deaths > 0 && len(ps.Deaths) != p.Deaths {
			t.Errorf("%s: смертей %d, в таблице %d", p.Name(), len(ps.Deaths), p.Deaths)
		}
	}
}

// Итоги матча из реплея должны совпадать с публичным скорбордом. Это главная
// проверка независимости: если она проходит, скорборд нам больше не нужен.
//
//	TEST_REPLAY_PATH=/путь/8999344582.dem go test ./internal/replay/
func TestTotalsMatchScoreboard(t *testing.T) {
	path := os.Getenv("TEST_REPLAY_PATH")
	if path == "" {
		t.Skip("не задан TEST_REPLAY_PATH")
	}
	m, err := fixture.Match8999344582()
	if err != nil {
		t.Fatalf("фикстура: %v", err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("реплей: %v", err)
	}
	defer f.Close()

	res, err := replay.Parse(f, m)
	if err != nil {
		t.Fatalf("разбор: %v", err)
	}
	for _, p := range m.Players {
		ps, ok := res.Players[p.Slot]
		if !ok || !ps.Totals.Has {
			t.Fatalf("%s: итогов нет", p.Name())
		}
		tt := ps.Totals
		check := func(name string, ours, theirs int) {
			t.Helper()
			if ours != theirs {
				t.Errorf("%s, %s: у нас %d, в скорборде %d", p.Name(), name, ours, theirs)
			}
		}
		check("убийства", tt.Kills, p.Kills)
		check("смерти", tt.Died, p.Deaths)
		check("помощь", tt.Assists, p.Assists)
		check("добивания", tt.LastHits, p.LastHits)
		check("денаи", tt.Denies, p.Denies)
		check("уровень", tt.Level, p.Level)
		check("урон по героям", tt.HeroDamage, p.HeroDamage)
		check("урон по строениям", tt.TowerDamage, p.TowerDamage)
		check("стаки", tt.CampsStacked, p.CampsStacked)
		check("руны", tt.RunePickups, p.RunePickups)
		check("обзорные варды", tt.ObsPlaced, p.ObsPlaced)
		check("сентри", tt.SenPlaced, p.SenPlaced)
	}
}

// Координаты смерти, путь героев и моменты подбора ресурсов.
//
//	TEST_REPLAY_PATH=/путь/8999344582.dem go test ./internal/replay/
func TestPositionsAndPickups(t *testing.T) {
	path := os.Getenv("TEST_REPLAY_PATH")
	if path == "" {
		t.Skip("не задан TEST_REPLAY_PATH")
	}
	m, err := fixture.Match8999344582()
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	res, err := replay.Parse(f, m)
	if err != nil {
		t.Fatalf("разбор: %v", err)
	}

	for _, p := range m.Players {
		ps, ok := res.Players[p.Slot]
		if !ok {
			continue
		}

		// Смерть без координат — это потерянные данные, а не «умер в нуле».
		for _, d := range ps.Deaths {
			if d.X == 0 && d.Y == 0 {
				t.Errorf("%s: смерть на %d:%02d без координат", p.Name(), d.Time/60, d.Time%60)
				break
			}
			// Зону Valve присылает сам — она и проверяет наши координаты.
			if d.Zone > 0 {
				if got := zoneOf(d.X, d.Y); got != d.Zone {
					t.Errorf("%s, смерть на %d:%02d в (%.0f, %.0f): наша зона %d, у Valve %d",
						p.Name(), d.Time/60, d.Time%60, d.X, d.Y, got, d.Zone)
				}
			}
		}

		// Путь должен быть и покрывать матч: шаг известен, точек примерно
		// столько, сколько шагов в матче.
		if ps.Track.Step != replay.TrackStep {
			t.Errorf("%s: шаг пути %d вместо %d", p.Name(), ps.Track.Step, replay.TrackStep)
		}
		if want := m.Duration / replay.TrackStep / 2; len(ps.Track.X) < want {
			t.Errorf("%s: точек пути %d, ждали хотя бы %d", p.Name(), len(ps.Track.X), want)
		}

		// Подборы должны сходиться со счётчиками — это те же события,
		// посчитанные двумя способами.
		got := map[string]int{}
		for _, pk := range ps.Pickups {
			got[pk.Kind]++
			if pk.Time < 0 || pk.Time > m.Duration+120 {
				t.Errorf("%s: подбор %s на %d секунде — вне матча", p.Name(), pk.Kind, pk.Time)
			}
		}
		for kind, want := range map[string]int{
			"rune":   ps.Totals.RunePickups,
			"shrine": ps.Totals.WisdomShrines,
			"lotus":  ps.Totals.LotusesTaken,
		} {
			if got[kind] != want {
				t.Errorf("%s: подборов %s %d, а счётчик показывает %d", p.Name(), kind, got[kind], want)
			}
		}
	}
}

// zoneOf — зона карты по координатам в той же нумерации, что у Valve:
// 1 нижняя, 2 центр, 3 верхняя.
func zoneOf(x, y float64) int {
	const lo, span, mid = 64.0, 128.0, 0.14
	nx, ny := (x-lo)/span, (y-lo)/span
	switch d := nx - ny; {
	case d > mid:
		return 1
	case d < -mid:
		return 3
	default:
		return 2
	}
}
