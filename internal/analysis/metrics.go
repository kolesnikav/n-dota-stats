package analysis

import (
	"fmt"
	"strings"

	"github.com/kolesnikav/n-dota-stats/internal/dota"
)

// CompareKind — с чем сравнивать значение показателя.
type CompareKind int

const (
	CompareHeroPercentile CompareKind = iota // перцентиль по этому же герою
	CompareRoleMedian                        // медиана роли
	CompareOwnHistory                        // среднее по своим прошлым играм на роли
	CompareLaneOpponent                      // соперник по линии
)

// History отдаёт среднее значение показателя по прошлым матчам игрока на роли.
type History interface {
	Average(accountID int64, role dota.Role, key string) (avg float64, games int, ok bool)
}

// Ctx — всё, что нужно показателю для расчёта.
type Ctx struct {
	Match    *dota.Match
	Player   *dota.Player
	Opponent *dota.Player // соперник по линии, может быть nil
	History  History
}

// Value — результат расчёта показателя.
type Value struct {
	Text string  // как показать
	Num  float64 // числовое значение для сравнений
	Has  bool    // есть ли число
}

func none() (Value, bool) { return Value{}, false }
func num(f float64, format string, args ...any) (Value, bool) {
	return Value{Text: fmt.Sprintf(format, args...), Num: f, Has: true}, true
}

// Metric — одна строка сводки.
type Metric struct {
	Key     string
	Label   string
	Roles   []dota.Role
	Short   bool        // попадает в короткую сводку
	Needs   dota.Detail // какой уровень данных требуется
	Bench   string      // ключ benchmarks для перцентиля
	Compare []CompareKind
	Calc    func(*Ctx) (Value, bool)
}

func (m Metric) forRole(r dota.Role) bool {
	for _, x := range m.Roles {
		if x == r {
			return true
		}
	}
	return false
}

var all = []dota.Role{dota.RoleCarry, dota.RoleMid, dota.RoleOfflane, dota.RoleRoamer, dota.RoleHard}
var cores = []dota.Role{dota.RoleCarry, dota.RoleMid, dota.RoleOfflane}
var supports = []dota.Role{dota.RoleRoamer, dota.RoleHard}

// Registry — все показатели. Добавление нового — одна запись здесь.
var Registry = []Metric{
	{
		Key: "gpm", Label: "GPM", Roles: all, Short: true,
		Bench: "gold_per_min", Compare: []CompareKind{CompareHeroPercentile, CompareRoleMedian, CompareOwnHistory},
		Calc: func(c *Ctx) (Value, bool) { return num(float64(c.Player.GPM), "%d", c.Player.GPM) },
	},
	{
		Key: "xpm", Label: "XPM", Roles: all,
		Bench: "xp_per_min", Compare: []CompareKind{CompareHeroPercentile, CompareRoleMedian},
		Calc: func(c *Ctx) (Value, bool) { return num(float64(c.Player.XPM), "%d", c.Player.XPM) },
	},
	{
		Key: "lh10", Label: "Добивания к 10:00", Roles: cores, Short: true,
		Compare: []CompareKind{CompareRoleMedian, CompareOwnHistory, CompareLaneOpponent},
		Calc: func(c *Ctx) (Value, bool) {
			v, ok := dota.At(c.Player.LHT, 10)
			if !ok {
				return none()
			}
			return num(float64(v), "%d", v)
		},
	},
	{
		Key: "lh20", Label: "Добивания к 20:00", Roles: []dota.Role{dota.RoleCarry},
		Compare: []CompareKind{CompareOwnHistory},
		Calc: func(c *Ctx) (Value, bool) {
			v, ok := dota.At(c.Player.LHT, 20)
			if !ok {
				return none()
			}
			return num(float64(v), "%d", v)
		},
	},
	{
		Key: "xp10", Label: "Опыт к 10:00", Roles: []dota.Role{dota.RoleMid}, Short: true,
		Compare: []CompareKind{CompareRoleMedian, CompareLaneOpponent},
		Calc: func(c *Ctx) (Value, bool) {
			v, ok := dota.At(c.Player.XPT, 10)
			if !ok {
				return none()
			}
			return num(float64(v), "%d", v)
		},
	},
	{
		Key: "lane_eff", Label: "Линия к 10:00", Roles: all, Short: true,
		Compare: []CompareKind{CompareRoleMedian, CompareOwnHistory},
		Calc: func(c *Ctx) (Value, bool) {
			pct := c.Player.LaneEfficiencyPct
			if pct == 0 {
				// золото на 10:00, делённое на стоимость полной линии крипов
				g, ok := dota.At(c.Player.GoldT, 10)
				if !ok {
					return none()
				}
				pct = int(float64(g) / 4948.0 * 100)
			}
			return num(float64(pct), "%d%%", pct)
		},
	},
	{
		Key: "nw_gap", Label: "Нетворс против вражеского керри", Roles: []dota.Role{dota.RoleCarry}, Short: true,
		Calc: func(c *Ctx) (Value, bool) {
			var enemy *dota.Player
			for _, o := range c.Match.Opponents(c.Player) {
				if o.Role == dota.RoleCarry {
					enemy = o
				}
			}
			if enemy == nil {
				return none()
			}
			gap := c.Player.NetWorth - enemy.NetWorth
			sign := "+"
			if gap < 0 {
				sign = "−"
				gap = -gap
			}
			return num(float64(c.Player.NetWorth-enemy.NetWorth), "%s%s (%s)", sign, thousands(gap), enemy.Name())
		},
	},
	{
		Key: "dmg_share", Label: "Доля урона команды", Roles: cores,
		Calc: func(c *Ctx) (Value, bool) {
			var total int
			for _, p := range c.Match.Team(c.Player.IsRadiant) {
				total += p.HeroDamage
			}
			if total == 0 {
				return none()
			}
			share := float64(c.Player.HeroDamage) / float64(total) * 100
			return num(share, "%.0f%%", share)
		},
	},
	{
		Key: "hero_damage", Label: "Урон по героям", Roles: all,
		Bench: "hero_damage_per_min", Compare: []CompareKind{CompareHeroPercentile},
		Calc: func(c *Ctx) (Value, bool) {
			return num(float64(c.Player.HeroDamage), "%s", thousands(c.Player.HeroDamage))
		},
	},
	{
		Key: "tower_damage", Label: "Урон по строениям", Roles: []dota.Role{dota.RoleCarry, dota.RoleOfflane},
		Bench: "tower_damage", Compare: []CompareKind{CompareHeroPercentile, CompareRoleMedian},
		Calc: func(c *Ctx) (Value, bool) {
			return num(float64(c.Player.TowerDamage), "%s", thousands(c.Player.TowerDamage))
		},
	},
	{
		Key: "kill_part", Label: "Участие в убийствах команды", Roles: []dota.Role{dota.RoleMid, dota.RoleRoamer},
		Calc: func(c *Ctx) (Value, bool) {
			var teamKills int
			for _, p := range c.Match.Team(c.Player.IsRadiant) {
				teamKills += p.Kills
			}
			if teamKills == 0 {
				return none()
			}
			share := float64(c.Player.Kills+c.Player.Assists) / float64(teamKills) * 100
			return num(share, "%.0f%%", share)
		},
	},
	{
		Key: "wards", Label: "Варды", Roles: supports, Short: true,
		Needs: dota.DetailMeta, Compare: []CompareKind{CompareRoleMedian, CompareOwnHistory},
		Calc: func(c *Ctx) (Value, bool) {
			if c.Player.ObsPlaced == 0 && c.Player.SenPlaced == 0 {
				return none()
			}
			s := fmt.Sprintf("%d обзорных, %d сентри", c.Player.ObsPlaced, c.Player.SenPlaced)
			if c.Player.ObsKilled > 0 {
				s += fmt.Sprintf(", снято %d чужих", c.Player.ObsKilled)
			}
			return Value{Text: s, Num: float64(c.Player.ObsPlaced), Has: true}, true
		},
	},
	{
		Key: "stacks", Label: "Стаки", Roles: []dota.Role{dota.RoleOfflane, dota.RoleRoamer, dota.RoleHard}, Short: true,
		Needs: dota.DetailMeta, Compare: []CompareKind{CompareRoleMedian, CompareOwnHistory},
		Calc: func(c *Ctx) (Value, bool) {
			return num(float64(c.Player.CampsStacked), "%d", c.Player.CampsStacked)
		},
	},
	{
		Key: "stuns", Label: "Секунды контроля", Roles: []dota.Role{dota.RoleOfflane, dota.RoleRoamer, dota.RoleHard},
		Needs: dota.DetailMeta, Compare: []CompareKind{CompareOwnHistory},
		Calc: func(c *Ctx) (Value, bool) {
			if c.Player.Stuns <= 0 {
				return none()
			}
			return num(c.Player.Stuns, "%.0f", c.Player.Stuns)
		},
	},
	{
		Key: "teamfight", Label: "Участие в файтах", Roles: all, Short: true,
		Needs: dota.DetailReplay, Compare: []CompareKind{CompareOwnHistory},
		Calc: func(c *Ctx) (Value, bool) {
			if c.Player.TeamfightParticipation <= 0 {
				return none()
			}
			v := c.Player.TeamfightParticipation * 100
			return num(v, "%.0f%%", v)
		},
	},
	{
		Key: "runes", Label: "Собрано рун", Roles: []dota.Role{dota.RoleMid, dota.RoleRoamer},
		Needs: dota.DetailMeta, Compare: []CompareKind{CompareOwnHistory},
		Calc: func(c *Ctx) (Value, bool) {
			return num(float64(c.Player.RunePickups), "%d", c.Player.RunePickups)
		},
	},
	{
		Key: "neutrals", Label: "Нейтралы", Roles: []dota.Role{dota.RoleCarry, dota.RoleOfflane},
		Needs: dota.DetailMeta,
		Calc: func(c *Ctx) (Value, bool) {
			if c.Player.NeutralKills == 0 {
				return none()
			}
			return num(float64(c.Player.NeutralKills), "%d", c.Player.NeutralKills)
		},
	},
	{
		Key: "healing", Label: "Лечение", Roles: supports,
		Calc: func(c *Ctx) (Value, bool) {
			if c.Player.HeroHealing == 0 {
				return none()
			}
			return num(float64(c.Player.HeroHealing), "%s", thousands(c.Player.HeroHealing))
		},
	},
	{
		Key: "buybacks", Label: "Байбэки", Roles: all,
		Needs: dota.DetailMeta,
		Calc: func(c *Ctx) (Value, bool) {
			if c.Player.Buybacks == 0 {
				return none()
			}
			return num(float64(c.Player.Buybacks), "%d", c.Player.Buybacks)
		},
	},
	{
		Key: "tp", Label: "Использовано TP", Roles: supports,
		Needs: dota.DetailMeta,
		Calc: func(c *Ctx) (Value, bool) {
			n := c.Player.ItemUses["tpscroll"]
			if n == 0 {
				return none()
			}
			return num(float64(n), "%d", n)
		},
	},
	{
		Key: "boots", Label: "Ботинки куплены", Roles: all,
		Needs: dota.DetailMeta, Compare: []CompareKind{CompareOwnHistory},
		Calc: func(c *Ctx) (Value, bool) {
			best := -1
			for _, item := range []string{"boots", "power_treads", "arcane_boots", "phase_boots", "tranquil_boots"} {
				if t, ok := c.Player.FirstPurchase[item]; ok && t > 0 && (best < 0 || t < best) {
					best = t
				}
			}
			if best < 0 {
				return none()
			}
			return num(float64(best), "%s", clock(best))
		},
	},
	{
		Key: "deaths", Label: "Смерти", Roles: all,
		Bench: "deaths_per_min", Compare: []CompareKind{CompareHeroPercentile, CompareOwnHistory},
		Calc: func(c *Ctx) (Value, bool) {
			return num(float64(c.Player.Deaths), "%d", c.Player.Deaths)
		},
	},
}

// MetricsFor возвращает показатели роли: сначала короткие, потом остальные.
func MetricsFor(role dota.Role, short bool) []Metric {
	out := make([]Metric, 0, len(Registry))
	for _, m := range Registry {
		if !m.forRole(role) {
			continue
		}
		if short && !m.Short {
			continue
		}
		out = append(out, m)
	}
	return out
}

func thousands(v int) string {
	if v < 1000 {
		return fmt.Sprintf("%d", v)
	}
	return fmt.Sprintf("%.1fk", float64(v)/1000)
}

func clock(sec int) string {
	if sec < 0 {
		sec = 0
	}
	return fmt.Sprintf("%d:%02d", sec/60, sec%60)
}

// LaneOpponent ищет соперника на той же линии: сначала по совпадению линии,
// среди нескольких берём самого богатого.
func LaneOpponent(m *dota.Match, p *dota.Player) *dota.Player {
	var best *dota.Player
	for _, o := range m.Opponents(p) {
		if p.Lane == 0 || o.Lane != p.Lane {
			continue
		}
		if best == nil || o.NetWorth > best.NetWorth {
			best = o
		}
	}
	return best
}

// notes строит пометки сравнения после значения показателя.
func (m Metric) notes(c *Ctx, v Value) []string {
	var out []string
	for _, kind := range m.Compare {
		switch kind {
		case CompareHeroPercentile:
			if m.Bench == "" {
				continue
			}
			if pct, ok := c.Player.Benchmarks[m.Bench]; ok {
				out = append(out, fmt.Sprintf("%d-й перцентиль", int(pct*100+0.5)))
			}
		case CompareRoleMedian:
			if med, ok := RoleMedian(c.Player.Role, m.Key); ok && v.Has {
				out = append(out, fmt.Sprintf("медиана роли %s", trim(med)))
			}
		case CompareOwnHistory:
			if c.History == nil || c.Player.AccountID == 0 || !v.Has {
				continue
			}
			if avg, games, ok := c.History.Average(c.Player.AccountID, c.Player.Role, m.Key); ok && games >= 5 {
				out = append(out, fmt.Sprintf("твоё среднее %s", trim(avg)))
			}
		case CompareLaneOpponent:
			if c.Opponent == nil || !v.Has {
				continue
			}
			oc := &Ctx{Match: c.Match, Player: c.Opponent, History: c.History}
			if ov, ok := m.Calc(oc); ok && ov.Has {
				out = append(out, fmt.Sprintf("у %s — %s", c.Opponent.Name(), ov.Text))
			}
		}
	}
	return out
}

func trim(f float64) string {
	if f == float64(int(f)) {
		return fmt.Sprintf("%d", int(f))
	}
	return fmt.Sprintf("%.1f", f)
}

// Line — готовая строка сводки.
type Line struct {
	Label string
	Value string
	Notes []string
}

func (l Line) String() string {
	s := l.Label + ": " + l.Value
	if len(l.Notes) > 0 {
		s += " · " + strings.Join(l.Notes, " · ")
	}
	return s
}

// Build считает показатели роли игрока и возвращает готовые строки.
func Build(m *dota.Match, p *dota.Player, hist History, short bool) []Line {
	c := &Ctx{Match: m, Player: p, Opponent: LaneOpponent(m, p), History: hist}
	var out []Line
	for _, metric := range MetricsFor(p.Role, short) {
		if metric.Needs > m.Detail {
			continue
		}
		v, ok := metric.Calc(c)
		if !ok || v.Text == "" {
			continue
		}
		out = append(out, Line{Label: metric.Label, Value: v.Text, Notes: metric.notes(c, v)})
	}
	return out
}
