package app

import (
	"testing"
	"time"

	"github.com/kolesnikav/n-dota-stats/internal/dota"
	"github.com/kolesnikav/n-dota-stats/internal/odota"
)

type fastSource struct{}

func (fastSource) Name() string                             { return "быстрый" }
func (fastSource) RecentMatchIDs(int64) ([]int64, error)    { return nil, nil }
func (fastSource) Match(int64) (*dota.Match, []byte, error) { return nil, nil, nil }

// Частота опроса не должна зависеть от того, как источник себя называет.
//
// Раньше проверялось буквальное совпадение имени со «Steam Web API». Когда у
// источника появился запасной ход и имя стало «Steam Web API (запасной —
// OpenDota)», опрос молча замедлился вдвое.
func TestPollEveryIgnoresSourceName(t *testing.T) {
	long := time.Now().Add(-24 * time.Hour)

	fast := &App{Source: fastSource{}}
	if got := fast.pollEvery(long); got != idlePoll {
		t.Errorf("быстрый источник: интервал %s, ждали %s", got, idlePoll)
	}

	// Запасной ход не делает источник медленным: основной остался быстрым.
	withBackup := &App{Source: Fallback{Primary: fastSource{}, Backup: odota.NewSource(odota.New(""))}}
	if got := withBackup.pollEvery(long); got != idlePoll {
		t.Errorf("с запасным ходом: интервал %s, ждали %s", got, idlePoll)
	}

	// А вот если основной — сама OpenDota, опрашиваем вдвое реже.
	slow := &App{Source: odota.NewSource(odota.New(""))}
	if got := slow.pollEvery(long); got != idlePoll*slowSourceFactor {
		t.Errorf("медленный источник: интервал %s, ждали %s", got, idlePoll*slowSourceFactor)
	}

	// Пока человек играет, опрашиваем часто.
	if got := fast.pollEvery(time.Now().Add(-10 * time.Minute)); got != activePoll {
		t.Errorf("играющий: интервал %s, ждали %s", got, activePoll)
	}
}
