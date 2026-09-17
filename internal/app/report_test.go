package app

import (
	"strings"
	"testing"
	"time"
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
