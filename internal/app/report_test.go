package app

import (
	"strings"
	"testing"
	"time"

	"github.com/kolesnikav/n-dota-stats/internal/analysis"
)

// Время матча читается словами для близких дат и цифрами для дальних.
func TestMatchTime(t *testing.T) {
	now := time.Now()
	cases := []struct {
		when time.Time
		want string
	}{
		{time.Date(now.Year(), now.Month(), now.Day(), 21, 14, 0, 0, time.Local), "сегодня в 21:14"},
		{time.Date(now.Year(), now.Month(), now.Day(), 9, 5, 0, 0, time.Local).AddDate(0, 0, -1), "вчера в 09:05"},
	}
	for _, c := range cases {
		if got := matchTime(c.when.Unix()); got != c.want {
			t.Errorf("%s: получили %q, ждали %q", c.when, got, c.want)
		}
	}

	// Дальняя дата этого года — без года, прошлогодняя — с годом.
	old := now.AddDate(0, 0, -40)
	if got := matchTime(old.Unix()); !strings.Contains(got, old.Format("02.01")) || strings.Contains(got, old.Format("2006")) {
		t.Errorf("дата этого года: %q", got)
	}
	last := now.AddDate(-1, 0, 0)
	if got := matchTime(last.Unix()); !strings.Contains(got, last.Format("02.01.2006")) {
		t.Errorf("прошлогодняя дата: %q", got)
	}

	if got := matchTime(0); got != "" {
		t.Errorf("без времени ждали пустую строку, получили %q", got)
	}
}

// В сводке показывается тройка Dota. Своя оценка — только для того, кто в неё
// не попал: рядом с фактом догадка лишняя.
func TestTop3ShowsDotaAnswer(t *testing.T) {
	base := analysis.Snapshot{
		DotaMVP: []string{"Anti-Mage", "Mirana", "Phantom Assassin"},
		Place:   9, Players: 10, Score: 0.27,
		Top: []analysis.SnapTop{{Name: "Кто-то"}},
	}

	// Игрока в тройке нет — показываем его место по нашей оценке.
	out := strings.Join((&Report{Snap: base}).top3(), "\n")
	if !strings.Contains(out, "ЛУЧШИЕ ПО ВЕРСИИ DOTA") {
		t.Error("нет раздела с ответом игры")
	}
	if strings.Contains(out, "МОЕЙ ОЦЕНКЕ") {
		t.Error("своя тройка показана рядом с ответом игры")
	}
	if !strings.Contains(out, "9-е место") {
		t.Errorf("нет своей оценки места:\n%s", out)
	}

	// Игрок в тройке — своя оценка не нужна, достаточно пометки.
	inside := base
	inside.DotaPlace = 2
	out = strings.Join((&Report{Snap: inside}).top3(), "\n")
	if !strings.Contains(out, "Mirana ← ты") {
		t.Errorf("не помечено место игрока:\n%s", out)
	}
	if strings.Contains(out, "по моей оценке") {
		t.Error("своя оценка показана, хотя игрок и так в тройке")
	}

	// Ответа игры нет — показываем свою тройку, как раньше.
	noAnswer := base
	noAnswer.DotaMVP = nil
	out = strings.Join((&Report{Snap: noAnswer}).top3(), "\n")
	if !strings.Contains(out, "ЛУЧШИЕ ПО МОЕЙ ОЦЕНКЕ") {
		t.Errorf("без ответа игры нет своей тройки:\n%s", out)
	}
}
