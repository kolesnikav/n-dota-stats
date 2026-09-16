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
