package odota

import (
	"encoding/json"
	"fmt"

	"github.com/kolesnikav/n-dota-stats/internal/dota"
)

// Source — источник матчей поверх OpenDota. Используется, когда не настроен
// ключ Steam Web API, и в тестах.
type Source struct {
	C *Client

	// NoParseRequests выключает заявки на разбор реплея. Нужен при загрузке
	// истории: сто заявок разом — это злоупотребление чужой очередью, а для
	// матчей старше двух недель реплея всё равно уже нет.
	NoParseRequests bool
}

func NewSource(c *Client) *Source { return &Source{C: c} }

// MatchIDsSince возвращает матчи игрока за последние days дней.
func (s *Source) MatchIDsSince(accountID int64, days int) ([]int64, error) {
	var rows []struct {
		MatchID int64 `json:"match_id"`
	}
	path := fmt.Sprintf("/players/%d/matches?date=%d", accountID, days)
	if err := s.C.get(path, &rows); err != nil {
		return nil, err
	}
	out := make([]int64, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.MatchID)
	}
	return out, nil
}

func (s *Source) Name() string { return "OpenDota" }

func (s *Source) RecentMatchIDs(accountID int64) ([]int64, error) {
	rows, err := s.C.RecentMatches(accountID)
	if err != nil {
		return nil, err
	}
	out := make([]int64, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.MatchID)
	}
	return out, nil
}

func (s *Source) Match(matchID int64) (*dota.Match, []byte, error) {
	var raw json.RawMessage
	if err := s.C.get(fmt.Sprintf("/matches/%d", matchID), &raw); err != nil {
		return nil, nil, err
	}
	m, err := Decode(raw)
	if err != nil {
		return nil, nil, err
	}
	// просим OpenDota разобрать реплей, чтобы в следующий раз данных было больше
	if m.Detail < dota.DetailReplay && !s.NoParseRequests {
		go s.C.RequestParse(matchID)
	}
	return m, raw, nil
}

// SlowPolling — у OpenDota лимит 60 запросов в минуту на всех пользователей,
// поэтому опрашивать её нужно реже, чем Steam Web API.
func (s *Source) SlowPolling() bool { return true }
