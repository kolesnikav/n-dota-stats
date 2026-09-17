// Package dota описывает доменную модель матча, не зависящую от источника
// данных. Источники (Steam Web API, OpenDota) приводят свои ответы к этим типам.
package dota

import (
	_ "embed"
	"encoding/json"
	"strconv"
	"sync"
)

//go:embed heroes.json
var heroesJSON []byte

var (
	heroesOnce sync.Once
	heroNames  map[int]string
	heroesMu   sync.RWMutex
)

// HeroName возвращает имя героя. Список вшит в бинарник и может быть обновлён
// через SetHeroNames, когда доступен справочник OpenDota.
func HeroName(id int) string {
	heroesOnce.Do(func() {
		raw := map[string]string{}
		_ = json.Unmarshal(heroesJSON, &raw)
		heroNames = make(map[int]string, len(raw))
		for k, v := range raw {
			if n, err := strconv.Atoi(k); err == nil {
				heroNames[n] = v
			}
		}
	})
	heroesMu.RLock()
	defer heroesMu.RUnlock()
	if name, ok := heroNames[id]; ok {
		return name
	}
	return "герой " + strconv.Itoa(id)
}

// SetHeroNames обновляет справочник героев (например, после нового патча).
func SetHeroNames(m map[int]string) {
	if len(m) == 0 {
		return
	}
	HeroName(0) // гарантируем инициализацию
	heroesMu.Lock()
	defer heroesMu.Unlock()
	for k, v := range m {
		heroNames[k] = v
	}
}

// Role — позиция игрока от 1 (керри) до 5 (хард саппорт).
type Role int

const (
	RoleUnknown Role = 0
	RoleCarry   Role = 1
	RoleMid     Role = 2
	RoleOfflane Role = 3
	RoleRoamer  Role = 4
	RoleHard    Role = 5
)

var roleNames = map[Role]string{
	RoleCarry:   "керри",
	RoleMid:     "мид",
	RoleOfflane: "оффлейн",
	RoleRoamer:  "роум",
	RoleHard:    "хард саппорт",
}

func (r Role) String() string {
	if n, ok := roleNames[r]; ok {
		return n
	}
	return "роль не определена"
}

var roleShort = map[Role]string{
	RoleCarry: "керри", RoleMid: "мид", RoleOfflane: "офф",
	RoleRoamer: "роум", RoleHard: "хард",
}

// Short — короткое имя роли для подписей, где важна длина.
func (r Role) Short() string {
	if n, ok := roleShort[r]; ok {
		return n
	}
	return "?"
}

// Valid сообщает, что роль распознана.
func (r Role) Valid() bool { return r >= RoleCarry && r <= RoleHard }

// Source говорит, откуда взялась роль: посчитана или указана игроком.
type Source string

const (
	SourceAuto   Source = "auto"
	SourceManual Source = "manual"
)

// Detail — уровень доступных данных по матчу.
type Detail int

const (
	DetailScoreboard Detail = iota // только итоговая таблица
	DetailMeta                     // + метаданные матча (тайминги, контроль)
	DetailReplay                   // + полный реплей (варды, позиции)
)

// Match — матч целиком.
type Match struct {
	ID         int64
	StartTime  int64
	Duration   int
	RadiantWin bool
	LobbyType  int
	GameMode   int
	Cluster    int
	Detail     Detail
	Players    []*Player
}

// Player — один игрок в матче.
type Player struct {
	Slot      int
	AccountID int64
	HeroID    int
	IsRadiant bool
	Win       bool

	Kills, Deaths, Assists int
	LastHits, Denies       int
	GPM, XPM               int
	Level                  int
	NetWorth               int
	HeroDamage             int
	TowerDamage            int
	HeroHealing            int
	Items                  []int
	RankTier               int

	// Требуют метаданных или разбора реплея.
	Lane         int
	LaneRole     int
	IsRoaming    bool
	ObsPlaced    int
	SenPlaced    int
	ObsKilled    int
	SenKilled    int
	CampsStacked int
	// HasStacks различает «стаков не было» и «стаки никто не считал»:
	// их отдаёт только разбор OpenDota, свой парсер их не видит.
	HasStacks              bool
	NeutralKills           int
	Buybacks               int
	RunePickups            int
	Stuns                  float64
	TeamfightParticipation float64
	LaneEfficiencyPct      int

	GoldT           []int // по минутам, нарастающим итогом
	XPT             []int
	LHT             []int
	LevelUpTimes    []int          // секунды получения уровней 2..25
	FirstPurchase   map[string]int // предмет -> секунда первой покупки
	ItemUses        map[string]int
	AbilityUpgrades []int

	// AbilityUses — сколько раз способность применена, HeroHits — сколько раз
	// попала по герою. Вместе дают точность умений с наведением.
	AbilityUses map[string]int
	HeroHits    map[string]int

	// Перцентили относительно того же героя, 0..1. Заполняются из снимка
	// benchmarks либо приходят готовыми из источника.
	Benchmarks map[string]float64

	// FirstItem — когда предмет впервые появился в инвентаре, секунды.
	// Заполняется из метаданных матча.
	FirstItem map[int]int

	// Заполняется анализом.
	Role       Role
	RoleSource Source
	HeroName   string
}

// Name — имя героя игрока.
func (p *Player) Name() string {
	if p.HeroName != "" {
		return p.HeroName
	}
	return HeroName(p.HeroID)
}

// SideName — сторона игрока для вывода.
func (p *Player) SideName() string {
	if p.IsRadiant {
		return "Radiant"
	}
	return "Dire"
}

// At возвращает значение поминутной кривой на минуте minute, безопасно к длине.
func At(series []int, minute int) (int, bool) {
	if minute < 0 || minute >= len(series) {
		return 0, false
	}
	return series[minute], true
}

// Find возвращает игрока по account_id.
func (m *Match) Find(accountID int64) *Player {
	for _, p := range m.Players {
		if p.AccountID == accountID {
			return p
		}
	}
	return nil
}

// Team возвращает игроков одной стороны.
func (m *Match) Team(radiant bool) []*Player {
	out := make([]*Player, 0, 5)
	for _, p := range m.Players {
		if p.IsRadiant == radiant {
			out = append(out, p)
		}
	}
	return out
}

// Opponents возвращает игроков противоположной стороны.
func (m *Match) Opponents(p *Player) []*Player { return m.Team(!p.IsRadiant) }

// DurationMinutes — длительность в минутах, минимум 1.
func (m *Match) DurationMinutes() float64 {
	if m.Duration <= 0 {
		return 1
	}
	return float64(m.Duration) / 60.0
}

// RankTierName переводит числовой ранг Valve в читаемое название медали.
func RankTierName(tier int) string {
	if tier <= 0 {
		return ""
	}
	medals := map[int]string{
		1: "Herald", 2: "Guardian", 3: "Crusader", 4: "Archon",
		5: "Legend", 6: "Ancient", 7: "Divine", 8: "Immortal",
	}
	medal, ok := medals[tier/10]
	if !ok {
		return ""
	}
	if tier/10 == 8 {
		return medal
	}
	return medal + " " + strconv.Itoa(tier%10)
}

//go:embed items.json
var itemsJSON []byte

var (
	itemsOnce sync.Once
	itemNames map[int]string
)

// ItemName возвращает внутреннее имя предмета по его id (например, 29 —
// "boots"). Пустая строка, если предмет неизвестен.
func ItemName(id int) string {
	itemsOnce.Do(func() {
		raw := map[string]string{}
		_ = json.Unmarshal(itemsJSON, &raw)
		itemNames = make(map[int]string, len(raw))
		for k, v := range raw {
			if n, err := strconv.Atoi(k); err == nil {
				itemNames[n] = v
			}
		}
	})
	return itemNames[id]
}
