package analysis

import (
	"fmt"
	"math"
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

// History отдаёт среднее значение показателя по прошлым матчам игрока: либо на
// той же роли, либо на том же герое — смотря что осмысленнее для показателя.
type History interface {
	Average(accountID int64, role dota.Role, key string) (avg float64, games int, ok bool)
	AverageOnHero(accountID int64, heroID int, key string) (avg float64, games int, ok bool)
}

// minHistoryGames — с какого числа матчей показывать своё среднее. Три — это
// мало, поэтому рядом всегда пишется объём выборки: пусть цифра будет видна,
// но и её надёжность тоже.
const minHistoryGames = 3

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
	Heroes  []int                // если задано — показывается только на этих героях
	ByHero  bool                 // сравнивать со своей историей по герою, а не по роли
	Short   bool                 // попадает в короткую сводку
	Needs   dota.Detail          // какой уровень данных требуется
	Group   string               // раздел сводки
	Lower   bool                 // меньше значит лучше
	Unit    string               // единица измерения для пометок сравнения, например "%"
	Format  func(float64) string // как печатать число в пометках, если не просто число
	Bench   string               // ключ benchmarks для перцентиля
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

// forHero отвечает, показывать ли метрику на этом герое. Без списка героев
// метрика общая и подходит всем.
func (m Metric) forHero(heroID int) bool {
	if len(m.Heroes) == 0 {
		return true
	}
	for _, id := range m.Heroes {
		if id == heroID {
			return true
		}
	}
	return false
}

// HeroSpecific сообщает, что показатель заведён под конкретных героев.
func (m Metric) HeroSpecific() bool { return len(m.Heroes) > 0 }

// historyByHero отвечает, с чем сравнивать своё прошлое. Лечение, контроль и
// точность умений задаются героем, а не позицией: сравнивать лечение Мираны со
// своим средним по всем саппортам — значит сравнивать её с Дазлом.
func (m Metric) historyByHero() bool { return m.ByHero || m.HeroSpecific() }

// compares отвечает, объявлена ли такая база сравнения. Маркер не должен
// опираться на то, чего в строке не видно: иначе непонятно, откуда вердикт.
func (m Metric) compares(kind CompareKind) bool {
	for _, k := range m.Compare {
		if k == kind {
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
		Key: "gpm", Group: "Фарм", Label: "GPM", Roles: all, Short: true,
		Bench: "gold_per_min", Compare: []CompareKind{CompareHeroPercentile, CompareRoleMedian, CompareOwnHistory},
		Calc: func(c *Ctx) (Value, bool) { return num(float64(c.Player.GPM), "%d", c.Player.GPM) },
	},
	{
		Key: "xpm", Group: "Фарм", Label: "XPM", Roles: all,
		Bench: "xp_per_min", Compare: []CompareKind{CompareHeroPercentile, CompareRoleMedian},
		Calc: func(c *Ctx) (Value, bool) { return num(float64(c.Player.XPM), "%d", c.Player.XPM) },
	},
	{
		Key: "lh10", Group: "Линия", Label: "Добивания к 10:00", Roles: cores, Short: true,
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
		Key: "lh20", Group: "Линия", Label: "Добивания к 20:00", Roles: []dota.Role{dota.RoleCarry},
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
		Key: "xp10", Group: "Линия", Label: "Опыт к 10:00", Roles: []dota.Role{dota.RoleMid}, Short: true,
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
		Key: "lane_eff", Group: "Линия", Label: "Линия к 10:00", Roles: all, Short: true, Unit: "%",
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
		Key: "nw_gap", Group: "Линия", Label: "Нетворс против вражеского керри", Roles: []dota.Role{dota.RoleCarry}, Short: true,
		Compare: []CompareKind{CompareOwnHistory},
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
		Key: "dmg_share", Group: "Бой", Label: "Доля урона команды", Roles: cores, Unit: "%",
		Compare: []CompareKind{CompareOwnHistory},
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
		Key: "hero_damage", Group: "Бой", Label: "Урон по героям", Roles: all,
		Bench: "hero_damage_per_min", Compare: []CompareKind{CompareHeroPercentile},
		Calc: func(c *Ctx) (Value, bool) {
			return num(float64(c.Player.HeroDamage), "%s", thousands(c.Player.HeroDamage))
		},
	},
	{
		Key: "tower_damage", Group: "Карта", Label: "Урон по строениям", Roles: []dota.Role{dota.RoleCarry, dota.RoleOfflane},
		Bench: "tower_damage", Compare: []CompareKind{CompareHeroPercentile, CompareRoleMedian},
		Calc: func(c *Ctx) (Value, bool) {
			return num(float64(c.Player.TowerDamage), "%s", thousands(c.Player.TowerDamage))
		},
	},
	{
		Key: "kill_part", Group: "Бой", Label: "Участие в убийствах команды", Roles: []dota.Role{dota.RoleMid, dota.RoleRoamer}, Unit: "%",
		Compare: []CompareKind{CompareOwnHistory},
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
		Key: "wards", Group: "Карта", Label: "Варды", Roles: supports, Short: true,
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
		Key: "stacks", Group: "Карта", Label: "Стаки", Roles: []dota.Role{dota.RoleOfflane, dota.RoleRoamer, dota.RoleHard}, Short: true,
		Needs: dota.DetailMeta, Compare: []CompareKind{CompareRoleMedian, CompareOwnHistory},
		Calc: func(c *Ctx) (Value, bool) {
			return num(float64(c.Player.CampsStacked), "%d", c.Player.CampsStacked)
		},
	},
	{
		Key: "stuns", Group: "Бой", Label: "Секунды контроля", Roles: []dota.Role{dota.RoleOfflane, dota.RoleRoamer, dota.RoleHard}, Unit: " с", ByHero: true,
		Needs: dota.DetailMeta, Compare: []CompareKind{CompareOwnHistory},
		Calc: func(c *Ctx) (Value, bool) {
			if c.Player.Stuns <= 0 {
				return none()
			}
			return num(c.Player.Stuns, "%.0f с", c.Player.Stuns)
		},
	},
	{
		Key: "teamfight", Group: "Бой", Label: "Участие в файтах", Roles: all, Short: true, Unit: "%",
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
		Key: "runes", Group: "Карта", Label: "Собрано рун", Roles: []dota.Role{dota.RoleMid, dota.RoleRoamer},
		Needs: dota.DetailMeta, Compare: []CompareKind{CompareOwnHistory},
		Calc: func(c *Ctx) (Value, bool) {
			return num(float64(c.Player.RunePickups), "%d", c.Player.RunePickups)
		},
	},
	{
		Key: "neutrals", Group: "Фарм", Label: "Нейтралы", Roles: []dota.Role{dota.RoleCarry, dota.RoleOfflane},
		Needs: dota.DetailMeta, Compare: []CompareKind{CompareOwnHistory},
		Calc: func(c *Ctx) (Value, bool) {
			if c.Player.NeutralKills == 0 {
				return none()
			}
			return num(float64(c.Player.NeutralKills), "%d", c.Player.NeutralKills)
		},
	},
	{
		Key: "healing", Group: "Бой", Label: "Лечение", Roles: supports, ByHero: true,
		Compare: []CompareKind{CompareOwnHistory},
		Calc: func(c *Ctx) (Value, bool) {
			if c.Player.HeroHealing == 0 {
				return none()
			}
			return num(float64(c.Player.HeroHealing), "%s", thousands(c.Player.HeroHealing))
		},
	},
	{
		Key: "buybacks", Group: "Бой", Label: "Байбэки", Roles: all,
		Needs: dota.DetailMeta, Compare: []CompareKind{CompareOwnHistory},
		Calc: func(c *Ctx) (Value, bool) {
			if c.Player.Buybacks == 0 {
				return none()
			}
			return num(float64(c.Player.Buybacks), "%d", c.Player.Buybacks)
		},
	},
	{
		Key: "tp", Group: "Карта", Label: "Использовано TP", Roles: supports,
		Needs: dota.DetailMeta, Compare: []CompareKind{CompareOwnHistory},
		Calc: func(c *Ctx) (Value, bool) {
			n := c.Player.ItemUses["tpscroll"]
			if n == 0 {
				return none()
			}
			return num(float64(n), "%d", n)
		},
	},
	{
		Key: "boots", Group: "Карта", Label: "Ботинки куплены", Roles: all, Lower: true,
		Needs: dota.DetailMeta, Compare: []CompareKind{CompareOwnHistory},
		Format: func(f float64) string { return clock(int(f)) },
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
		Key: "deaths", Group: "Бой", Label: "Смерти", Roles: all, Lower: true,
		Bench: "deaths_per_min", Compare: []CompareKind{CompareHeroPercentile, CompareOwnHistory},
		Calc: func(c *Ctx) (Value, bool) {
			return num(float64(c.Player.Deaths), "%d", c.Player.Deaths)
		},
	},
}

// Groups — разделы сводки в порядке показа.
var Groups = []string{"Линия", "Фарм", "Бой", "Карта", "Герой"}

// skillshot строит показатель точности умения с наведением: сколько попаданий
// по героям из скольких применений. Данные есть только в разобранном реплее.
func skillshot(key, label string, heroID int, ability string) Metric {
	return Metric{
		Key: key, Group: "Герой", Label: label, Roles: all, Heroes: []int{heroID},
		Needs: dota.DetailReplay, Unit: "%",
		Compare: []CompareKind{CompareOwnHistory},
		Calc: func(c *Ctx) (Value, bool) {
			casts := c.Player.AbilityUses[ability]
			if casts == 0 {
				return none()
			}
			hits := c.Player.HeroHits[ability]
			acc := float64(hits) / float64(casts) * 100
			return Value{
				Text: fmt.Sprintf("%d из %d (%.0f%%)", hits, casts, acc),
				Num:  acc, Has: true,
			}, true
		},
	}
}

// MetricsFor возвращает показатели роли для конкретного героя.
func MetricsFor(role dota.Role, heroID int, short bool) []Metric {
	out := make([]Metric, 0, len(Registry))
	for _, m := range Registry {
		if !m.forRole(role) || !m.forHero(heroID) {
			continue
		}
		if short && !m.Short {
			continue
		}
		out = append(out, m)
	}
	return out
}

// heroMetrics — показатели под конкретных героев. Добавление нового героя
// это одна строка: метрика сама решит, кому показываться.
var heroMetrics = []Metric{
	skillshot("mirana_arrow", "Стрелы", 9, "mirana_arrow"),
	skillshot("pudge_hook", "Крюки", 14, "pudge_meat_hook"),
}

func init() { Registry = append(Registry, heroMetrics...) }

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
//
// Порядок намеренный: медиана роли понятнее перцентиля, своя история важнее
// чужого перцентиля. Больше двух пометок строка не держит — становится нечитаемой.
func (m Metric) notes(c *Ctx, v Value) []string {
	order := []CompareKind{CompareRoleMedian, CompareOwnHistory, CompareLaneOpponent, CompareHeroPercentile}
	want := map[CompareKind]bool{}
	for _, k := range m.Compare {
		want[k] = true
	}
	var out []string
	for _, kind := range order {
		if !want[kind] {
			continue
		}
		if len(out) >= 3 {
			break
		}
		switch kind {
		case CompareHeroPercentile:
			if m.Bench == "" {
				continue
			}
			if pct, ok := c.Player.Benchmarks[m.Bench]; ok {
				// Перцентиль везде читается одинаково: «лучше стольких-то
				// процентов игроков на этом герое». Для смертей это значит
				// перевернуть шкалу, иначе высокая цифра выглядела бы похвалой.
				if m.Lower {
					pct = 1 - pct
				}
				out = append(out, fmt.Sprintf("лучше %d%% на герое", int(pct*100+0.5)))
			}
		case CompareRoleMedian:
			if med, ok := RoleMedian(c.Player.Role, m.Key); ok && v.Has {
				out = append(out, "медиана "+m.fmtNum(med))
			}
		case CompareOwnHistory:
			if c.History == nil || c.Player.AccountID == 0 || !v.Has {
				continue
			}
			var (
				avg   float64
				games int
				ok    bool
			)
			if m.historyByHero() {
				avg, games, ok = c.History.AverageOnHero(c.Player.AccountID, c.Player.HeroID, m.Key)
			} else {
				avg, games, ok = c.History.Average(c.Player.AccountID, c.Player.Role, m.Key)
			}
			if ok && games >= minHistoryGames {
				note := "твоё среднее " + m.fmtNum(avg)
				if games < 10 {
					note += fmt.Sprintf(" (%s)", plural(games, "игра", "игры", "игр"))
				}
				out = append(out, note)
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

// trim печатает число так, как его читает человек: у крупных величин доли
// не нужны, у мелких — единственное, что отличает одно значение от другого.
// plural склоняет число игр по-русски.
func plural(n int, one, few, many string) string {
	mod100, mod10 := n%100, n%10
	switch {
	case mod100 >= 11 && mod100 <= 14:
		return fmt.Sprintf("%d %s", n, many)
	case mod10 == 1:
		return fmt.Sprintf("%d %s", n, one)
	case mod10 >= 2 && mod10 <= 4:
		return fmt.Sprintf("%d %s", n, few)
	default:
		return fmt.Sprintf("%d %s", n, many)
	}
}

// fmtNum печатает число в пометке так же, как выглядит само значение.
func (m Metric) fmtNum(f float64) string {
	if m.Format != nil {
		return m.Format(f)
	}
	return trim(f) + m.Unit
}

func trim(f float64) string {
	if f >= 10 || f <= -10 || f == float64(int(f)) {
		return fmt.Sprintf("%.0f", f)
	}
	return fmt.Sprintf("%.1f", f)
}

// Вердикт по показателю — чтобы глаз цеплялся за проблемные строки.
const (
	VerdictNone = 0 // сравнивать не с чем
	VerdictGood = 1 // заметно лучше базы
	VerdictEven = 2 // примерно на уровне
	VerdictBad  = 3 // заметно хуже базы
)

// Line — готовая строка сводки.
type Line struct {
	Group   string
	Label   string
	Value   string
	Notes   []string
	Verdict int
}

func (l Line) String() string {
	s := l.Label + ": " + l.Value
	if len(l.Notes) > 0 {
		s += " · " + strings.Join(l.Notes, " · ")
	}
	return s
}

// verdict сравнивает значение с медианой роли, а если её нет — с перцентилем
// по герою. Порог в 15% выбран так, чтобы обычный разброс не подсвечивался.
func (m Metric) verdict(c *Ctx, v Value) int {
	if !v.Has {
		return VerdictNone
	}
	if med, ok := RoleMedian(c.Player.Role, m.Key); ok && med > 0 {
		ratio := v.Num / med
		if m.Lower {
			ratio = med / math.Max(v.Num, 0.01)
		}
		switch {
		case ratio >= 1.15:
			return VerdictGood
		case ratio <= 0.85:
			return VerdictBad
		default:
			return VerdictEven
		}
	}
	if c.History != nil && c.Player.AccountID != 0 && m.compares(CompareOwnHistory) {
		var (
			avg   float64
			games int
			ok    bool
		)
		if m.historyByHero() {
			avg, games, ok = c.History.AverageOnHero(c.Player.AccountID, c.Player.HeroID, m.Key)
		} else {
			avg, games, ok = c.History.Average(c.Player.AccountID, c.Player.Role, m.Key)
		}
		if ok && games >= minHistoryGames && avg > 0 {
			ratio := v.Num / avg
			if m.Lower {
				ratio = avg / math.Max(v.Num, 0.01)
			}
			switch {
			case ratio >= 1.15:
				return VerdictGood
			case ratio <= 0.85:
				return VerdictBad
			default:
				return VerdictEven
			}
		}
	}
	if m.Bench != "" {
		if pct, ok := c.Player.Benchmarks[m.Bench]; ok {
			if m.Lower {
				pct = 1 - pct
			}
			switch {
			case pct >= 0.65:
				return VerdictGood
			case pct <= 0.35:
				return VerdictBad
			default:
				return VerdictEven
			}
		}
	}
	return VerdictNone
}

// Build считает показатели роли игрока и возвращает готовые строки.
func Build(m *dota.Match, p *dota.Player, hist History, short bool) []Line {
	c := &Ctx{Match: m, Player: p, Opponent: LaneOpponent(m, p), History: hist}
	var out []Line
	for _, metric := range MetricsFor(p.Role, p.HeroID, short) {
		if metric.Needs > m.Detail {
			continue
		}
		v, ok := metric.Calc(c)
		if !ok || v.Text == "" {
			continue
		}
		out = append(out, Line{
			Group:   metric.Group,
			Label:   metric.Label,
			Value:   v.Text,
			Notes:   metric.notes(c, v),
			Verdict: metric.verdict(c, v),
		})
	}
	return out
}
