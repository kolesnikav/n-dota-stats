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
	recent := time.Now().Add(-10 * time.Minute)

	fast := &App{Source: fastSource{}}
	// Через Steam Web API опрашиваем раз в минуту всех и всегда: делить на
	// играющих и отдыхающих незачем, запаса ключа хватает с избытком.
	if got := fast.pollEvery(long); got != fastPoll {
		t.Errorf("быстрый источник, давно не играл: интервал %s, ждали %s", got, fastPoll)
	}
	if got := fast.pollEvery(recent); got != fastPoll {
		t.Errorf("быстрый источник, играет: интервал %s, ждали %s", got, fastPoll)
	}

	// Запасной ход не делает источник медленным: основной остался быстрым.
	withBackup := &App{Source: Fallback{Primary: fastSource{}, Backup: odota.NewSource(odota.New(""))}}
	if got := withBackup.pollEvery(long); got != fastPoll {
		t.Errorf("с запасным ходом: интервал %s, ждали %s", got, fastPoll)
	}

	// А саму OpenDota щадим: у неё лимит 60 запросов в минуту на всех.
	slow := &App{Source: odota.NewSource(odota.New(""))}
	if got := slow.pollEvery(long); got != slowIdlePoll {
		t.Errorf("медленный источник, давно не играл: интервал %s, ждали %s", got, slowIdlePoll)
	}
	if got := slow.pollEvery(recent); got != slowActivePoll {
		t.Errorf("медленный источник, играет: интервал %s, ждали %s", got, slowActivePoll)
	}

	// Будильник должен звонить чаще самого частого опроса, иначе минута на
	// деле превращается в полторы.
	if tickInterval >= fastPoll {
		t.Errorf("будильник раз в %s при опросе раз в %s", tickInterval, fastPoll)
	}
}
