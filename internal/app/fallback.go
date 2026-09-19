package app

import (
	"github.com/kolesnikav/n-dota-stats/internal/dota"
)

// Fallback — источник матчей с запасным ходом.
//
// Нужен потому, что у Valve сломан GetMatchDetails, и скорборд приходится
// брать из потока по сквозному номеру. Номер известен только для матчей,
// которые мы видели в списке игрока; для всех прочих — например, когда
// пользователь просит разбор чужого матча по номеру — остаётся OpenDota.
type Fallback struct {
	Primary MatchSource
	Backup  MatchSource
	Log     func(string, ...any)
}

func (f Fallback) Name() string {
	return f.Primary.Name() + " (запасной — " + f.Backup.Name() + ")"
}

func (f Fallback) RecentMatchIDs(accountID int64) ([]int64, error) {
	ids, err := f.Primary.RecentMatchIDs(accountID)
	if err == nil && len(ids) > 0 {
		return ids, nil
	}
	if err != nil && f.Log != nil {
		f.Log("список матчей %d у %s: %v — беру у %s", accountID, f.Primary.Name(), err, f.Backup.Name())
	}
	return f.Backup.RecentMatchIDs(accountID)
}

func (f Fallback) Match(matchID int64) (*dota.Match, []byte, error) {
	m, raw, err := f.Primary.Match(matchID)
	if err == nil {
		return m, raw, nil
	}
	if f.Log != nil {
		f.Log("матч %d у %s: %v — беру у %s", matchID, f.Primary.Name(), err, f.Backup.Name())
	}
	return f.Backup.Match(matchID)
}

// SlowPolling берётся у основного источника: именно его мы опрашиваем, а
// запасной включается только когда он не справился.
func (f Fallback) SlowPolling() bool {
	s, ok := f.Primary.(SlowSource)
	return ok && s.SlowPolling()
}
