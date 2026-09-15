package odota

import (
	"encoding/json"
	"fmt"

	"github.com/kolesnikav/n-dota-stats/internal/dota"
)

// Source — источник матчей поверх OpenDota. Используется, когда не настроен
// ключ Steam Web API, и в тестах.
type Source struct{ C *Client }

func NewSource(c *Client) *Source { return &Source{C: c} }

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
	if m.Detail < dota.DetailReplay {
		go s.C.RequestParse(matchID)
	}
	return m, raw, nil
}
