// Package valve — клиент Steam Web API: список матчей игрока и полный скорборд
// напрямую от Valve, без посредников.
//
// Нужен бесплатный ключ: https://steamcommunity.com/dev/apikey.
// Профиль игрока должен разрешать публичный показ данных о матчах, иначе
// GetMatchHistory вернёт пустой список.
package valve

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/kolesnikav/n-dota-stats/internal/dota"
)

const base = "https://api.steampowered.com/IDOTA2Match_570"

// Client — клиент Steam Web API.
//
// Скорборд берётся не через GetMatchDetails: этот метод у Valve сломан и
// отдаёт HTTP 500 с пустым телом на любом матче, хоть на свежем, хоть на
// матче 2014 года (проверено 17.09.2026, ключ при этом рабочий — без ключа
// приходит 403). Рабочий путь один: GetMatchHistoryBySequenceNum, он отдаёт
// тот же полный скорборд, но по сквозному номеру матча.
type Client struct {
	Key  string
	HTTP *http.Client

	// SeqLookup и SeqRemember связывают id матча с его сквозным номером.
	// Номер приходит вместе со списком матчей игрока; чтобы он пережил
	// перезапуск, его хранит база.
	SeqLookup   func(matchID int64) (int64, bool)
	SeqRemember func(matchID, seq int64)

	mu  sync.Mutex
	seq map[int64]int64
}

func New(key string) *Client {
	return &Client{
		Key:  key,
		HTTP: &http.Client{Timeout: 45 * time.Second},
		seq:  map[int64]int64{},
	}
}

// ErrNoSeq — сквозной номер матча неизвестен, а без него скорборд не получить.
var ErrNoSeq = errors.New("неизвестен сквозной номер матча")

func (c *Client) rememberSeq(matchID, seq int64) {
	if matchID == 0 || seq == 0 {
		return
	}
	c.mu.Lock()
	known := c.seq[matchID] == seq
	c.seq[matchID] = seq
	c.mu.Unlock()
	// В базу пишем только новое. При опросе раз в минуту список матчей игрока
	// почти весь повторяется, и без этой проверки два десятка лишних записей
	// уходили бы в базу каждую минуту на каждого.
	if known || c.SeqRemember == nil {
		return
	}
	c.SeqRemember(matchID, seq)
}

func (c *Client) lookupSeq(matchID int64) (int64, bool) {
	c.mu.Lock()
	seq, ok := c.seq[matchID]
	c.mu.Unlock()
	if ok {
		return seq, true
	}
	if c.SeqLookup != nil {
		return c.SeqLookup(matchID)
	}
	return 0, false
}

func (c *Client) get(path string, params url.Values, out any) ([]byte, error) {
	if c.Key == "" {
		return nil, fmt.Errorf("не задан STEAM_API_KEY")
	}
	params.Set("key", c.Key)
	resp, err := c.HTTP.Get(base + path + "?" + params.Encode())
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("steam %s: код %d", path, resp.StatusCode)
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			return nil, err
		}
	}
	return raw, nil
}

// RecentMatchIDs возвращает последние матчи игрока.
func (c *Client) RecentMatchIDs(accountID int64) ([]int64, error) {
	var doc struct {
		Result struct {
			Status     int    `json:"status"`
			StatusText string `json:"statusDetail"`
			Matches    []struct {
				MatchID     int64 `json:"match_id"`
				MatchSeqNum int64 `json:"match_seq_num"`
			} `json:"matches"`
		} `json:"result"`
	}
	params := url.Values{}
	params.Set("account_id", strconv.FormatInt(accountID, 10))
	params.Set("matches_requested", "20")
	if _, err := c.get("/GetMatchHistory/v1/", params, &doc); err != nil {
		return nil, err
	}
	if doc.Result.Status != 1 && doc.Result.Status != 0 {
		return nil, fmt.Errorf("steam: %s", doc.Result.StatusText)
	}
	out := make([]int64, 0, len(doc.Result.Matches))
	for _, m := range doc.Result.Matches {
		out = append(out, m.MatchID)
		c.rememberSeq(m.MatchID, m.MatchSeqNum)
	}
	return out, nil
}

// Name — имя источника для логов.
func (c *Client) Name() string { return "Steam Web API" }

// matchResult — матч в ответе Valve. Одинаков у GetMatchDetails и
// GetMatchHistoryBySequenceNum, поэтому описан один раз.
type matchResult struct {
	MatchID     int64  `json:"match_id"`
	MatchSeqNum int64  `json:"match_seq_num"`
	StartTime   int64  `json:"start_time"`
	Duration    int    `json:"duration"`
	RadiantWin  bool   `json:"radiant_win"`
	LobbyType   int    `json:"lobby_type"`
	GameMode    int    `json:"game_mode"`
	Cluster     int    `json:"cluster"`
	Error       string `json:"error"`
	Players     []struct {
		AccountID   int64 `json:"account_id"`
		PlayerSlot  int   `json:"player_slot"`
		HeroID      int   `json:"hero_id"`
		Kills       int   `json:"kills"`
		Deaths      int   `json:"deaths"`
		Assists     int   `json:"assists"`
		LastHits    int   `json:"last_hits"`
		Denies      int   `json:"denies"`
		GPM         int   `json:"gold_per_min"`
		XPM         int   `json:"xp_per_min"`
		Level       int   `json:"level"`
		NetWorth    int   `json:"net_worth"`
		HeroDamage  int   `json:"hero_damage"`
		TowerDamage int   `json:"tower_damage"`
		HeroHealing int   `json:"hero_healing"`
		LeaverState int   `json:"leaver_status"`
		Item0       int   `json:"item_0"`
		Item1       int   `json:"item_1"`
		Item2       int   `json:"item_2"`
		Item3       int   `json:"item_3"`
		Item4       int   `json:"item_4"`
		Item5       int   `json:"item_5"`
	} `json:"players"`
}

type matchDetails struct {
	Result matchResult `json:"result"`
}

// convert приводит ответ Valve к доменной модели.
func convert(r *matchResult) *dota.Match {
	m := &dota.Match{
		ID:         r.MatchID,
		StartTime:  r.StartTime,
		Duration:   r.Duration,
		RadiantWin: r.RadiantWin,
		LobbyType:  r.LobbyType,
		GameMode:   r.GameMode,
		Cluster:    r.Cluster,
		Detail:     dota.DetailScoreboard,
	}
	for _, rp := range r.Players {
		isRadiant := rp.PlayerSlot < 128
		p := &dota.Player{
			Slot:           rp.PlayerSlot,
			AccountID:      rp.AccountID,
			HeroID:         rp.HeroID,
			IsRadiant:      isRadiant,
			Win:            isRadiant == r.RadiantWin,
			Kills:          rp.Kills,
			Deaths:         rp.Deaths,
			Assists:        rp.Assists,
			LastHits:       rp.LastHits,
			Denies:         rp.Denies,
			GPM:            rp.GPM,
			XPM:            rp.XPM,
			Level:          rp.Level,
			NetWorth:       rp.NetWorth,
			HeroDamage:     rp.HeroDamage,
			TowerDamage:    rp.TowerDamage,
			HeroHealing:    rp.HeroHealing,
			FromScoreboard: true,
			Abandoned:      rp.LeaverState != 0,
			Items: []int{rp.Item0, rp.Item1, rp.Item2, rp.Item3,
				rp.Item4, rp.Item5},
		}
		p.HeroName = dota.HeroName(p.HeroID)
		m.Players = append(m.Players, p)
	}
	return m
}

// Match загружает матч и приводит к доменной модели. Возвращает также сырой
// ответ — он кладётся в базу как есть.
//
// Идём через поток по сквозному номеру: GetMatchDetails у Valve не работает.
func (c *Client) Match(matchID int64) (*dota.Match, []byte, error) {
	seq, ok := c.lookupSeq(matchID)
	if !ok {
		return nil, nil, fmt.Errorf("матч %d: %w", matchID, ErrNoSeq)
	}
	var doc seqDoc
	params := url.Values{}
	params.Set("start_at_match_seq_num", strconv.FormatInt(seq, 10))
	params.Set("matches_requested", "1")
	raw, err := c.get("/GetMatchHistoryBySequenceNum/v1/", params, &doc)
	if err != nil {
		return nil, nil, err
	}
	if doc.Result.Status != 1 || len(doc.Result.Matches) == 0 {
		return nil, nil, fmt.Errorf("матч %d: пустой ответ (статус %d %s)",
			matchID, doc.Result.Status, doc.Result.Error)
	}
	r := &doc.Result.Matches[0]
	if r.MatchID != matchID {
		// Номер указывает на другой матч: значит связь id и номера испорчена,
		// и молча отдавать чужой скорборд нельзя.
		return nil, nil, fmt.Errorf("по номеру %d пришёл матч %d вместо %d", seq, r.MatchID, matchID)
	}
	return convert(r), raw, nil
}

// seqDoc — ответ GetMatchHistoryBySequenceNum: те же матчи, что у
// GetMatchDetails, но пачкой по сто штук за запрос. Это единственный способ
// набрать корпус публичных матчей своими силами: по одному матчу за запрос
// нужный объём не собрать.
type seqDoc struct {
	Result struct {
		Status  int           `json:"status"`
		Error   string        `json:"statusDetail"`
		Matches []matchResult `json:"matches"`
	} `json:"result"`
}

// MatchesBySeq возвращает до count матчей начиная с номера start и номер,
// с которого продолжать. Если матчей больше нет, следующий номер равен start.
func (c *Client) MatchesBySeq(start int64, count int) ([]*dota.Match, int64, error) {
	if count <= 0 || count > 100 {
		count = 100
	}
	var doc seqDoc
	params := url.Values{}
	params.Set("start_at_match_seq_num", strconv.FormatInt(start, 10))
	params.Set("matches_requested", strconv.Itoa(count))
	if _, err := c.get("/GetMatchHistoryBySequenceNum/v1/", params, &doc); err != nil {
		return nil, start, err
	}
	if doc.Result.Status != 1 {
		return nil, start, fmt.Errorf("steam: статус %d %s", doc.Result.Status, doc.Result.Error)
	}
	next := start
	out := make([]*dota.Match, 0, len(doc.Result.Matches))
	for i := range doc.Result.Matches {
		r := &doc.Result.Matches[i]
		if r.MatchSeqNum >= next {
			next = r.MatchSeqNum + 1
		}
		out = append(out, convert(r))
	}
	return out, next, nil
}
