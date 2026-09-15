// Package replay разбирает полный .dem-файл Valve своими силами.
//
// Метаданные матча дают тайминги, прокачку и контроль, но не дают вардов и
// поминутных кривых — за ними приходилось ходить в чужую очередь разбора.
// Здесь эти величины считаются сами.
//
// Что извлекается:
//   - обзорные варды и сентри: кто поставил, когда, где;
//   - снятые чужие варды — из боевого лога;
//   - поминутные кривые золота, опыта, добиваний и отказов;
//   - смерти с координатами.
//
// Чего здесь пока нет: стаки лагерей и участие в файтах. Стаков нет в боевом
// логе этого патча вообще — проверено перебором всех типов записей, события
// NEUTRAL_CAMP_STACK не встречается ни разу. Их придётся считать слежением за
// лагерями нейтралов, это отдельная работа.
package replay

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/dotabuff/manta"
	mdota "github.com/dotabuff/manta/dota"

	"github.com/kolesnikav/n-dota-stats/internal/dota"
)

const tickRate = 30

// Ward — поставленный вард.
type Ward struct {
	Slot     int
	Sentry   bool
	Placed   int // секунда игры
	Removed  int // когда исчез; -1 если дожил до конца
	X, Y     float64
	KilledBy int // слот снявшего, -1 если истёк сам
}

// Death — смерть героя с координатами.
type Death struct {
	Slot int
	Time int
	X, Y float64
}

// PlayerStats — что удалось посчитать по игроку.
type PlayerStats struct {
	Slot      int
	ObsPlaced int
	SenPlaced int
	ObsKilled int // снято чужих обзорных
	SenKilled int
	GoldT     []int
	XPT       []int
	LHT       []int
	DNT       []int
	Wards     []Ward
	Deaths    []Death
}

// Result — итог разбора.
type Result struct {
	Duration int
	Players  map[int]*PlayerStats
}

func (r *Result) player(slot int) *PlayerStats {
	if p, ok := r.Players[slot]; ok {
		return p
	}
	p := &PlayerStats{Slot: slot}
	r.Players[slot] = p
	return p
}

// heroSlot переводит имя юнита героя в слот по данным матча.
type heroSlot map[string]int

func buildHeroSlots(m *dota.Match) (byUnit heroSlot, byKey heroSlot) {
	byUnit, byKey = heroSlot{}, heroSlot{}
	for _, p := range m.Players {
		byUnit[unitName(p.Name())] = p.Slot
		byKey[heroKey(p.Name())] = p.Slot
	}
	return byUnit, byKey
}

// unitName приводит имя героя к виду npc_dota_hero_shadow_shaman — так герои
// называются в боевом логе.
func unitName(name string) string {
	return "npc_dota_hero_" + strings.ReplaceAll(strings.ReplaceAll(
		strings.ToLower(name), "'", ""), " ", "_")
}

// heroKey — имя героя без разделителей. Нужно потому, что классы сущностей
// пишутся то с подчёркиваниями (Winter_Wyvern), то без (ShadowShaman), а в
// таблице матча герой зовётся «Shadow Shaman».
func heroKey(name string) string {
	s := strings.ToLower(name)
	for _, c := range []string{" ", "_", "-", "'"} {
		s = strings.ReplaceAll(s, c, "")
	}
	return s
}

// Parse разбирает реплей. Матч нужен, чтобы сопоставить героев со слотами.
func Parse(r io.Reader, m *dota.Match) (*Result, error) {
	p, err := manta.NewStreamParser(r)
	if err != nil {
		return nil, fmt.Errorf("открыть реплей: %w", err)
	}
	slots, slotsByKey := buildHeroSlots(m)
	res := &Result{Duration: m.Duration, Players: map[int]*PlayerStats{}}

	// Начало игры определяем по первому спавну баунти-руны: они появляются
	// ровно в 0:00. Поле m_flGameStartTime для этого не годится — оно
	// отмечает другой момент и расходится примерно на двадцать секунд.
	startTick := uint32(0)
	heroByIndex := map[int32]int{}  // индекс сущности героя -> слот
	heroPos := map[int][2]float64{} // слот -> последняя известная позиция
	wardsByIndex := map[int32]*Ward{}

	// Использования вардовых предметов из боевого лога. По ним определяется
	// владелец обзорного варда: у самой сущности ссылки на хозяина нет.
	var wardUses []wardUse
	minute := -1

	// Время игры нужно в секундах от гудка, но гудок распознаётся не сразу.
	// Поэтому события записываются в тиках, а в секунды переводятся в конце —
	// иначе всё, что случилось до нуля, пришлось бы выбрасывать.
	gameTime := func() int {
		if startTick == 0 {
			return -1
		}
		return (int(p.Tick) - int(startTick)) / tickRate
	}
	tick := func() int { return int(p.Tick) }

	p.OnEntity(func(e *manta.Entity, op manta.EntityOp) error {
		cn := e.GetClassName()

		switch {
		case cn == "CDOTA_Item_Rune":
			if startTick != 0 || op&manta.EntityOpCreated == 0 {
				return nil
			}
			if rt, ok := e.GetInt32("m_iRuneType"); ok && rt == 5 {
				startTick = p.Tick
			}

		case strings.HasPrefix(cn, "CDOTA_Unit_Hero_"):
			// иллюзии пропускаем
			if rep, ok := e.GetUint64("m_hReplicatingOtherHeroModel"); ok && rep != 16777215 {
				return nil
			}
			slot, ok := slotsByKey[heroKey(strings.TrimPrefix(cn, "CDOTA_Unit_Hero_"))]
			if !ok {
				return nil
			}
			heroByIndex[e.GetIndex()] = slot
			if x, y := cellPos(e); x > 0 {
				heroPos[slot] = [2]float64{x, y}
			}

		case strings.Contains(cn, "NPC_Observer_Ward"):
			// У обзорных вардов ссылка на владельца заполняется не в момент
			// создания, а следующим обновлением. Поэтому владельца ищем на
			// любом обновлении, пока не найдём, и только тогда засчитываем.
			idx := e.GetIndex()
			sentry := strings.Contains(cn, "TrueSight")
			if op&manta.EntityOpCreated != 0 {
				x, y := cellPos(e)
				wardsByIndex[idx] = &Ward{
					Slot: -1, Sentry: sentry, Placed: tick(), Removed: -1, X: x, Y: y, KilledBy: -1,
				}
			}
			w, tracked := wardsByIndex[idx]
			if !tracked {
				return nil
			}
			if w.Slot < 0 {
				team := 0
				if v, ok := e.GetUint64("m_iTeamNum"); ok {
					team = int(v)
				}
				if slot, ok := claimWardUse(wardUses, w.Placed, team); ok {
					w.Slot = slot
					ps := res.player(slot)
					if w.Sentry {
						ps.SenPlaced++
					} else {
						ps.ObsPlaced++
					}
				} else if slot, ok := wardOwner(e, heroByIndex, heroPos, team, w.X, w.Y); ok {
					w.Slot = slot
					ps := res.player(slot)
					if w.Sentry {
						ps.SenPlaced++
					} else {
						ps.ObsPlaced++
					}
				}
			}
			if op&manta.EntityOpDeleted != 0 {
				w.Removed = tick()
				if w.Slot >= 0 {
					res.player(w.Slot).Wards = append(res.player(w.Slot).Wards, *w)
				}
				delete(wardsByIndex, idx)
			}

		case cn == "CDOTA_DataRadiant" || cn == "CDOTA_DataDire":
			t := gameTime()
			if t < 0 {
				return nil
			}
			if m := t / 60; m > minute {
				minute = m
				base := 0
				if cn == "CDOTA_DataDire" {
					base = 128
				}
				for i := 0; i < 5; i++ {
					ps := res.player(base + i)
					pre := fmt.Sprintf("m_vecDataTeam.%04d.", i)
					ps.GoldT = appendAt(ps.GoldT, minute, intProp(e, pre+"m_iTotalEarnedGold"))
					ps.XPT = appendAt(ps.XPT, minute, intProp(e, pre+"m_iTotalEarnedXP"))
					ps.LHT = appendAt(ps.LHT, minute, intProp(e, pre+"m_iLastHitCount"))
					ps.DNT = appendAt(ps.DNT, minute, intProp(e, pre+"m_iDenyCount"))
				}
			}
		}
		return nil
	})

	// Снятые варды, смерти и установки вардов берём из боевого лога:
	// у сущностей этих сведений нет.
	p.Callbacks.OnCMsgDOTACombatLogEntry(func(entry *mdota.CMsgDOTACombatLogEntry) error {
		if entry.GetType() == mdota.DOTA_COMBATLOG_TYPES_DOTA_COMBATLOG_ITEM {
			inflictor, _ := p.LookupStringByIndex("CombatLogNames", int32(entry.GetInflictorName()))
			switch inflictor {
			case "item_ward_observer", "item_ward_sentry", "item_ward_dispenser":
			default:
				return nil
			}
			att, _ := p.LookupStringByIndex("CombatLogNames", int32(entry.GetAttackerName()))
			if slot, ok := slots[att]; ok {
				wardUses = append(wardUses, wardUse{Time: tick(), Slot: slot})
			}
			return nil
		}
		if entry.GetType() != mdota.DOTA_COMBATLOG_TYPES_DOTA_COMBATLOG_DEATH {
			return nil
		}
		target, _ := p.LookupStringByIndex("CombatLogNames", int32(entry.GetTargetName()))
		attacker, _ := p.LookupStringByIndex("CombatLogNames", int32(entry.GetAttackerName()))
		switch target {
		case "npc_dota_observer_wards", "npc_dota_sentry_wards":
			slot, ok := slots[attacker]
			if !ok {
				return nil
			}
			ps := res.player(slot)
			if target == "npc_dota_sentry_wards" {
				ps.SenKilled++
			} else {
				ps.ObsKilled++
			}
		default:
			slot, ok := slots[target]
			if !ok {
				return nil
			}
			ps := res.player(slot)
			ps.Deaths = append(ps.Deaths, Death{
				Slot: slot, Time: tick(),
				X: float64(entry.GetLocationX()), Y: float64(entry.GetLocationY()),
			})
		}
		return nil
	})

	if err := p.Start(); err != nil {
		return nil, fmt.Errorf("разбор реплея: %w", err)
	}
	if startTick == 0 {
		return nil, fmt.Errorf("не нашёл начало игры")
	}
	// варды, дожившие до конца матча
	for _, w := range wardsByIndex {
		if w.Slot >= 0 {
			res.player(w.Slot).Wards = append(res.player(w.Slot).Wards, *w)
		}
	}
	// тики -> секунды от гудка
	toSec := func(t int) int {
		if t < 0 {
			return t
		}
		return (t - int(startTick)) / tickRate
	}
	for _, ps := range res.Players {
		for i := range ps.Wards {
			ps.Wards[i].Placed = toSec(ps.Wards[i].Placed)
			ps.Wards[i].Removed = toSec(ps.Wards[i].Removed)
		}
		for i := range ps.Deaths {
			ps.Deaths[i].Time = toSec(ps.Deaths[i].Time)
		}
	}
	for _, ps := range res.Players {
		sort.Slice(ps.Deaths, func(i, j int) bool { return ps.Deaths[i].Time < ps.Deaths[j].Time })
		sort.Slice(ps.Wards, func(i, j int) bool { return ps.Wards[i].Placed < ps.Wards[j].Placed })
	}
	return res, nil
}

// wardUse — момент, когда игрок применил вардовый предмет.
type wardUse struct {
	Time int
	Slot int
	Used bool
}

// claimWardUse находит применение предмета, которому соответствует появившийся
// вард: ближайшее по времени, той же команды и ещё не занятое другим вардом.
// Диспенсер не сообщает, какой вард поставлен, поэтому тип не сверяем.
func claimWardUse(uses []wardUse, placed, team int) (int, bool) {
	best, bestDiff := -1, 4*tickRate // четыре секунды
	for i := range uses {
		u := &uses[i]
		if u.Used {
			continue
		}
		radiant := u.Slot < 128
		if (team == 2) != radiant {
			continue
		}
		diff := placed - u.Time
		if diff < 0 {
			diff = -diff
		}
		if diff <= bestDiff {
			bestDiff, best = diff, i
		}
	}
	if best < 0 {
		return 0, false
	}
	uses[best].Used = true
	return uses[best].Slot, true
}

// wardOwner определяет, кто поставил вард.
//
// У сентри ссылка на хозяина заполнена сразу, а у обзорных вардов её нет
// вовсе — ни в момент создания, ни позже. Поэтому запасной путь: вард ставится
// вплотную к герою, значит владелец — ближайший союзник в пределах пары клеток.
func wardOwner(e *manta.Entity, heroes map[int32]int, pos map[int][2]float64,
	team int, x, y float64) (int, bool) {

	if owner, ok := e.GetUint64("m_hOwnerEntity"); ok {
		if slot, found := heroes[int32(owner&0x3FFF)]; found {
			return slot, true
		}
	}
	best, bestDist := -1, 6.0 // шесть клеток — заведомо больше дальности установки
	for slot, p := range pos {
		radiant := slot < 128
		if (team == 2) != radiant {
			continue
		}
		dx, dy := p[0]-x, p[1]-y
		if d := dx*dx + dy*dy; d < bestDist*bestDist {
			bestDist = mathSqrt(d)
			best = slot
		}
	}
	return best, best >= 0
}

func mathSqrt(v float64) float64 {
	if v <= 0 {
		return 0
	}
	x := v
	for i := 0; i < 20; i++ {
		x = (x + v/x) / 2
	}
	return x
}

func cellPos(e *manta.Entity) (float64, float64) {
	cx, _ := e.GetUint64("CBodyComponent.m_cellX")
	cy, _ := e.GetUint64("CBodyComponent.m_cellY")
	vx, _ := e.GetFloat32("CBodyComponent.m_vecX")
	vy, _ := e.GetFloat32("CBodyComponent.m_vecY")
	return float64(cx) + float64(vx)/256, float64(cy) + float64(vy)/256
}

func intProp(e *manta.Entity, name string) int {
	if v, ok := e.GetInt32(name); ok {
		return int(v)
	}
	if v, ok := e.GetUint64(name); ok {
		return int(v)
	}
	return 0
}

// appendAt дописывает значение так, чтобы индекс совпадал с минутой.
func appendAt(series []int, minute, value int) []int {
	for len(series) < minute {
		last := 0
		if len(series) > 0 {
			last = series[len(series)-1]
		}
		series = append(series, last)
	}
	if len(series) == minute {
		return append(series, value)
	}
	series[minute] = value
	return series
}

// Apply переносит посчитанное в матч и поднимает уровень детализации.
func (r *Result) Apply(m *dota.Match) int {
	applied := 0
	for _, p := range m.Players {
		ps, ok := r.Players[p.Slot]
		if !ok {
			continue
		}
		p.ObsPlaced = ps.ObsPlaced
		p.SenPlaced = ps.SenPlaced
		p.ObsKilled = ps.ObsKilled
		p.SenKilled = ps.SenKilled
		if len(ps.GoldT) > 0 {
			p.GoldT, p.XPT, p.LHT = ps.GoldT, ps.XPT, ps.LHT
		}
		applied++
	}
	if applied > 0 {
		m.Detail = dota.DetailReplay
	}
	return applied
}
