// Package odota — клиент OpenDota. Основное назначение: снимок перцентилей по
// героям. Дополнительно умеет отдавать матчи целиком — это запасной источник,
// когда не настроен ключ Steam Web API, и он же используется в тестах.
package odota

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/kolesnikav/n-dota-stats/internal/dota"
)

const base = "https://api.opendota.com/api"

// Client — клиент OpenDota с троттлингом и повтором при 429.
//
// Без ключа лимит — 60 запросов в минуту. Держать ровно эту частоту нельзя:
// счётчик на их стороне скользящий, и любой лишний запрос (например, от
// пользователя, который прямо сейчас подключается) приводит к 429 у всех.
// Поэтому интервал с запасом, а на 429 — отход и повтор.
type Client struct {
	APIKey string
	HTTP   *http.Client

	// MinInterval — минимальный промежуток между запросами.
	MinInterval time.Duration

	mu   sync.Mutex
	last time.Time
}

func New(apiKey string) *Client {
	gap := 1600 * time.Millisecond // ~37 запросов в минуту, с запасом к лимиту
	if apiKey != "" {
		gap = 350 * time.Millisecond
	}
	return &Client{
		APIKey:      apiKey,
		HTTP:        &http.Client{Timeout: 60 * time.Second},
		MinInterval: gap,
	}
}

func (c *Client) throttle() {
	c.mu.Lock()
	defer c.mu.Unlock()
	gap := c.MinInterval
	if gap <= 0 {
		gap = 1600 * time.Millisecond
	}
	if wait := gap - time.Since(c.last); wait > 0 {
		time.Sleep(wait)
	}
	c.last = time.Now()
}

// TooManyRequests сообщает, что сервис ограничил частоту запросов.
type TooManyRequests struct{ Path string }

func (e *TooManyRequests) Error() string {
	return "opendota " + e.Path + ": слишком часто (429)"
}

func (c *Client) get(path string, out any) error {
	u := base + path
	if c.APIKey != "" {
		sep := "?"
		if len(path) > 0 && containsRune(path, '?') {
			sep = "&"
		}
		u += sep + "api_key=" + url.QueryEscape(c.APIKey)
	}
	const attempts = 3
	for attempt := 1; ; attempt++ {
		c.throttle()
		req, err := http.NewRequest(http.MethodGet, u, nil)
		if err != nil {
			return err
		}
		req.Header.Set("User-Agent", "n-dota-stats/1.0")
		resp, err := c.HTTP.Do(req)
		if err != nil {
			return err
		}
		if resp.StatusCode == http.StatusTooManyRequests {
			wait := retryAfter(resp.Header.Get("Retry-After"), attempt)
			_ = resp.Body.Close()
			if attempt >= attempts {
				return &TooManyRequests{Path: path}
			}
			time.Sleep(wait)
			continue
		}
		if resp.StatusCode != http.StatusOK {
			_ = resp.Body.Close()
			return fmt.Errorf("opendota %s: код %d", path, resp.StatusCode)
		}
		body, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			return err
		}
		return json.Unmarshal(body, out)
	}
}

// retryAfter выбирает паузу перед повтором: слушаем заголовок сервиса, иначе
// растём по степеням.
func retryAfter(header string, attempt int) time.Duration {
	if header != "" {
		if sec, err := strconv.Atoi(header); err == nil && sec > 0 && sec < 120 {
			return time.Duration(sec) * time.Second
		}
	}
	return time.Duration(attempt*attempt) * 3 * time.Second
}

func containsRune(s string, r rune) bool {
	for _, c := range s {
		if c == r {
			return true
		}
	}
	return false
}

// RecentMatch — короткая запись из истории матчей.
type RecentMatch struct {
	MatchID   int64 `json:"match_id"`
	HeroID    int   `json:"hero_id"`
	StartTime int64 `json:"start_time"`
	Duration  int   `json:"duration"`
}

// RecentMatches возвращает последние матчи игрока.
func (c *Client) RecentMatches(accountID int64) ([]RecentMatch, error) {
	var out []RecentMatch
	err := c.get(fmt.Sprintf("/players/%d/recentMatches", accountID), &out)
	return out, err
}

// Heroes возвращает справочник героев.
func (c *Client) Heroes() (map[int]string, error) {
	var rows []struct {
		ID   int    `json:"id"`
		Name string `json:"localized_name"`
	}
	if err := c.get("/heroes", &rows); err != nil {
		return nil, err
	}
	out := make(map[int]string, len(rows))
	for _, r := range rows {
		out[r.ID] = r.Name
	}
	return out, nil
}

// BenchmarkPoint — точка кривой перцентилей.
type BenchmarkPoint struct {
	Percentile float64 `json:"percentile"`
	Value      float64 `json:"value"`
}

// Benchmarks возвращает кривые перцентилей по герою: метрика -> точки.
func (c *Client) Benchmarks(heroID int) (map[string][]BenchmarkPoint, error) {
	var out struct {
		Result map[string][]BenchmarkPoint `json:"result"`
	}
	if err := c.get(fmt.Sprintf("/benchmarks?hero_id=%d", heroID), &out); err != nil {
		return nil, err
	}
	return out.Result, nil
}

// RequestParse ставит матч в очередь разбора на стороне OpenDota. Используется
// только в запасном режиме, когда своего разбора нет.
func (c *Client) RequestParse(matchID int64) {
	c.throttle()
	req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/request/%d", base, matchID), nil)
	if err != nil {
		return
	}
	req.Header.Set("User-Agent", "n-dota-stats/1.0")
	resp, err := c.HTTP.Do(req)
	if err == nil {
		_ = resp.Body.Close()
	}
}

// Match загружает матч и приводит его к доменной модели.
func (c *Client) Match(matchID int64) (*dota.Match, error) {
	var raw json.RawMessage
	if err := c.get(fmt.Sprintf("/matches/%d", matchID), &raw); err != nil {
		return nil, err
	}
	return Decode(raw)
}

type rawMatch struct {
	MatchID    int64      `json:"match_id"`
	StartTime  int64      `json:"start_time"`
	Duration   int        `json:"duration"`
	RadiantWin bool       `json:"radiant_win"`
	LobbyType  int        `json:"lobby_type"`
	GameMode   int        `json:"game_mode"`
	Cluster    int        `json:"cluster"`
	Version    *int       `json:"version"`
	Players    []rawPlyer `json:"players"`
}

type rawPlyer struct {
	PlayerSlot  int    `json:"player_slot"`
	AccountID   *int64 `json:"account_id"`
	HeroID      int    `json:"hero_id"`
	IsRadiant   bool   `json:"isRadiant"`
	Win         int    `json:"win"`
	Kills       int    `json:"kills"`
	Deaths      int    `json:"deaths"`
	Assists     int    `json:"assists"`
	LastHits    int    `json:"last_hits"`
	Denies      int    `json:"denies"`
	GPM         int    `json:"gold_per_min"`
	XPM         int    `json:"xp_per_min"`
	Level       int    `json:"level"`
	NetWorth    int    `json:"net_worth"`
	HeroDamage  int    `json:"hero_damage"`
	TowerDamage int    `json:"tower_damage"`
	HeroHealing int    `json:"hero_healing"`
	RankTier    *int   `json:"rank_tier"`
	Item0       int    `json:"item_0"`
	Item1       int    `json:"item_1"`
	Item2       int    `json:"item_2"`
	Item3       int    `json:"item_3"`
	Item4       int    `json:"item_4"`
	Item5       int    `json:"item_5"`

	Lane              *int     `json:"lane"`
	LaneRole          *int     `json:"lane_role"`
	IsRoaming         *bool    `json:"is_roaming"`
	ObsPlaced         *int     `json:"obs_placed"`
	SenPlaced         *int     `json:"sen_placed"`
	ObserverKills     *int     `json:"observer_kills"`
	SentryKills       *int     `json:"sentry_kills"`
	CampsStacked      *int     `json:"camps_stacked"`
	NeutralKills      *int     `json:"neutral_kills"`
	BuybackCount      *int     `json:"buyback_count"`
	RunePickups       *int     `json:"rune_pickups"`
	Stuns             *float64 `json:"stuns"`
	Teamfight         *float64 `json:"teamfight_participation"`
	LaneEfficiencyPct *int     `json:"lane_efficiency_pct"`

	GoldT           []int          `json:"gold_t"`
	XPT             []int          `json:"xp_t"`
	LHT             []int          `json:"lh_t"`
	FirstPurchase   map[string]int `json:"first_purchase_time"`
	ItemUses        map[string]int `json:"item_uses"`
	AbilityUpgrades []int          `json:"ability_upgrades_arr"`

	Benchmarks map[string]struct {
		Raw *float64 `json:"raw"`
		Pct *float64 `json:"pct"`
	} `json:"benchmarks"`
}

// Decode превращает сырой ответ OpenDota в доменную модель.
func Decode(raw []byte) (*dota.Match, error) {
	var rm rawMatch
	if err := json.Unmarshal(raw, &rm); err != nil {
		return nil, err
	}
	if rm.MatchID == 0 || len(rm.Players) == 0 {
		return nil, fmt.Errorf("пустой матч")
	}
	m := &dota.Match{
		ID:         rm.MatchID,
		StartTime:  rm.StartTime,
		Duration:   rm.Duration,
		RadiantWin: rm.RadiantWin,
		LobbyType:  rm.LobbyType,
		GameMode:   rm.GameMode,
		Cluster:    rm.Cluster,
		Detail:     dota.DetailScoreboard,
	}
	if rm.Version != nil {
		m.Detail = dota.DetailReplay
	}
	for _, rp := range rm.Players {
		p := &dota.Player{
			Slot:        rp.PlayerSlot,
			HeroID:      rp.HeroID,
			IsRadiant:   rp.IsRadiant,
			Win:         rp.Win == 1,
			Kills:       rp.Kills,
			Deaths:      rp.Deaths,
			Assists:     rp.Assists,
			LastHits:    rp.LastHits,
			Denies:      rp.Denies,
			GPM:         rp.GPM,
			XPM:         rp.XPM,
			Level:       rp.Level,
			NetWorth:    rp.NetWorth,
			HeroDamage:  rp.HeroDamage,
			TowerDamage: rp.TowerDamage,
			HeroHealing: rp.HeroHealing,
			Items: []int{rp.Item0, rp.Item1, rp.Item2, rp.Item3,
				rp.Item4, rp.Item5},
			GoldT:           rp.GoldT,
			XPT:             rp.XPT,
			LHT:             rp.LHT,
			FirstPurchase:   rp.FirstPurchase,
			ItemUses:        rp.ItemUses,
			AbilityUpgrades: rp.AbilityUpgrades,
		}
		if rp.AccountID != nil {
			p.AccountID = *rp.AccountID
		}
		if rp.RankTier != nil {
			p.RankTier = *rp.RankTier
		}
		setInt(&p.Lane, rp.Lane)
		setInt(&p.LaneRole, rp.LaneRole)
		setInt(&p.ObsPlaced, rp.ObsPlaced)
		setInt(&p.SenPlaced, rp.SenPlaced)
		setInt(&p.ObsKilled, rp.ObserverKills)
		setInt(&p.SenKilled, rp.SentryKills)
		setInt(&p.CampsStacked, rp.CampsStacked)
		setInt(&p.NeutralKills, rp.NeutralKills)
		setInt(&p.Buybacks, rp.BuybackCount)
		setInt(&p.RunePickups, rp.RunePickups)
		setInt(&p.LaneEfficiencyPct, rp.LaneEfficiencyPct)
		if rp.IsRoaming != nil {
			p.IsRoaming = *rp.IsRoaming
		}
		if rp.Stuns != nil {
			p.Stuns = *rp.Stuns
		}
		if rp.Teamfight != nil {
			p.TeamfightParticipation = *rp.Teamfight
		}
		if len(rp.Benchmarks) > 0 {
			p.Benchmarks = make(map[string]float64, len(rp.Benchmarks))
			for k, v := range rp.Benchmarks {
				if v.Pct != nil {
					p.Benchmarks[k] = *v.Pct
				}
			}
		}
		p.HeroName = dota.HeroName(p.HeroID)
		m.Players = append(m.Players, p)
	}
	return m, nil
}

func setInt(dst *int, src *int) {
	if src != nil {
		*dst = *src
	}
}
