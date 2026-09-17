// Package replay разбирает полный .dem-файл Valve своими силами.
//
// Метаданные матча дают тайминги, прокачку и контроль, но не дают вардов и
// поминутных кривых — за ними приходилось ходить в чужую очередь разбора.
// Здесь эти величины считаются сами.
//
// Что извлекается:
//   - весь итоговый счёт матча: убийства, смерти, помощь, уровень, добивания,
//     денаи, нетворс, золото и опыт, урон по героям и строениям, контроль,
//     стаки, руны, варды, участие в файтах;
//   - обзорные варды и сентри: кто поставил, когда, где;
//   - снятые чужие варды — из боевого лога;
//   - поминутные кривые золота, опыта, добиваний и отказов;
//   - смерти с координатами.
//
// Итоги лежат не в боевом логе, а в сущностях: CDOTA_DataRadiant и
// CDOTA_DataDire (счёт по команде, пять игроков в каждой) и CDOTA_PlayerResource
// (убийства, смерти, помощь, уровень, участие в файтах — по всем десяти).
// Стаки там тоже есть, в поле m_iCampsStacked: искать их в боевом логе, как
// делалось раньше, было ошибкой — события NEUTRAL_CAMP_STACK в нём нет вовсе.
//
// Чего воспроизвести не удалось: hero_healing в счёте Valve. Поле m_fHealing
// считает что-то другое — у Мипо оно даёт 33 798 при нуле у Valve, у
// Джаггернаута ноль при 5039. Лечение по боевому логу тоже не сходится: туда
// попадает регенерация и вампиризм. Поэтому лечение остаётся из скорборда, а
// своё считается отдельной величиной HealAllies — вылечено союзным героям.
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
//
// Итоги матча берутся из сущностей самого реплея, а не из скорборда: сущности
// есть всегда, а скорборд может прийти неразобранным и таким остаться.
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

	// ItemUses и AbilityUses — сколько раз применён предмет или способность,
	// HeroHits — сколько применений способности задело героя. Всё по боевому
	// логу: в сущностях этого нет, а раньше приходилось брать у OpenDota.
	ItemUses    map[string]int
	AbilityUses map[string]int
	HeroHits    map[string]int

	// NeutralKills — убито нейтральных крипов, BuybackCount — выкупов.
	// Считаются по боевому логу: в сущностях их нет.
	NeutralKills int
	BuybackCount int
	// laneVotes — сколько раз игрок был замечен на каждой линии во время
	// лейнинга. Индексы: 1 нижняя, 2 центр, 3 верхняя.
	laneVotes [4]int

	// HealAllies — вылечено союзным героям, без самолечения. Считается по
	// боевому логу. Со счётом Valve это намеренно разные величины: их
	// hero_healing воспроизвести не удалось (см. docs/replay.md), а для оценки
	// саппорта «сколько вылечил союзникам» и осмысленнее.
	HealAllies int

	// Totals — итоговые значения на конец матча. Заполняются, только если
	// сущности встретились: HasTotals отличает «ноль» от «не считали».
	Totals Totals
}

// Totals — итоговый счёт игрока по данным реплея.
type Totals struct {
	Has bool

	Kills   int
	Died    int // смертей; поле Deaths уже занято списком смертей с координатами
	Assists int
	Level   int

	LastHits int
	Denies   int
	NetWorth int
	Gold     int // всего заработано золота
	XP       int // всего заработано опыта

	HeroDamage  int
	TowerDamage int
	Healing     int

	Stuns        float64
	CampsStacked int
	RunePickups  int

	ObsPlaced      int
	SenPlaced      int
	WardsDestroyed int
	WardsPurchased int

	TPScrolls   int
	SmokesUsed  int
	TowerKills  int
	RoshanKills int

	WisdomShrines int
	LotusesTaken  int

	TeamfightParticipation float64
	RankTier               int
	Name                   string
	AccountID              int64
}

// steamOffset переводит 64-битный Steam ID в номер аккаунта Dota.
const steamOffset = 76561197960265728

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
	// Сведения об игроках лежат в реплее в своём порядке — по подключению, а
	// не по игровому слоту, и между ними попадаются наблюдатели. Надёжная
	// привязка одна: номер аккаунта.
	slotByAccount := map[int64]int{}
	for _, p := range m.Players {
		if p.AccountID > 0 {
			slotByAccount[p.AccountID] = p.Slot
		}
	}
	res := &Result{Duration: m.Duration, Players: map[int]*PlayerStats{}}

	// Начало игры определяем по первому спавну баунти-руны: они появляются
	// ровно в 0:00. Поле m_flGameStartTime для этого не годится — оно
	// отмечает другой момент и расходится примерно на двадцать секунд.
	startTick := uint32(0)
	heroByIndex := map[int32]int{}  // индекс сущности героя -> слот
	heroPos := map[int][2]float64{} // слот -> последняя известная позиция
	wardsByIndex := map[int32]*Ward{}
	// Счётчик последней снятой минуты ведётся отдельно на каждую команду:
	// сущности Radiant и Dire обновляются независимо, и общий счётчик отдавал
	// минуту той, что успела первой, а вторую оставлял с нулями.
	lastMinute := map[string]int{"CDOTA_DataRadiant": -1, "CDOTA_DataDire": -1}
	// Когда в последний раз снимали итоги с каждой сущности.
	lastTotals := map[string]int{}

	// Использования вардовых предметов из боевого лога. По ним определяется
	// владелец обзорного варда: у самой сущности ссылки на хозяина нет.
	var wardUses []wardUse
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
			slot, ok := slotFromPlayerID(e)
			if !ok {
				// Запасной путь — по имени. Он ненадёжен: у части героев
				// внутреннее имя другое (Io зовётся Wisp, Treant Protector —
				// Treant), и до перехода на номер игрока они молча выпадали.
				slot, ok = slotsByKey[heroKey(strings.TrimPrefix(cn, "CDOTA_Unit_Hero_"))]
			}
			if !ok {
				return nil
			}
			heroByIndex[e.GetIndex()] = slot
			// Боевой лог называет героев по-своему. Раз уж слот известен,
			// запоминаем и это имя — тогда таблица имён строится из самого
			// реплея, а не из догадок о том, как Valve зовёт героя.
			if npc := npcName(cn); npc != "" {
				slots[npc] = slot
			}
			if x, y := cellPos(e); x > 0 {
				heroPos[slot] = [2]float64{x, y}
				// Линию определяем голосованием по позициям во время
				// лейнинга. Первые полминуты пропускаем: все ещё стоят на
				// фонтане, а фонтаны лежат на той же диагонали, что центр.
				if t := gameTime(); t >= 30 && t <= laneWindow {
					if lane := laneAt(x, y); lane > 0 {
						res.player(slot).laneVotes[lane]++
					}
				}
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
			base := 0
			if cn == "CDOTA_DataDire" {
				base = 128
			}
			// Итоги снимаем не чаще раза в две секунды игрового времени:
			// сущность обновляется по многу раз в секунду, а перечитывать два
			// десятка полей на каждое обновление незачем — важно лишь
			// последнее значение.
			if int(p.Tick)-lastTotals[cn] >= 2*tickRate {
				lastTotals[cn] = int(p.Tick)
				for i := 0; i < 5; i++ {
					readTeamTotals(e, fmt.Sprintf("m_vecDataTeam.%04d.", i), &res.player(base+i).Totals)
				}
			}

			t := gameTime()
			if t < 0 {
				return nil
			}
			minute := t / 60
			if minute <= lastMinute[cn] {
				return nil
			}
			lastMinute[cn] = minute
			for i := 0; i < 5; i++ {
				ps := res.player(base + i)
				pre := fmt.Sprintf("m_vecDataTeam.%04d.", i)
				ps.GoldT = appendAt(ps.GoldT, minute, intProp(e, pre+"m_iTotalEarnedGold"))
				ps.XPT = appendAt(ps.XPT, minute, intProp(e, pre+"m_iTotalEarnedXP"))
				ps.LHT = appendAt(ps.LHT, minute, intProp(e, pre+"m_iLastHitCount"))
				ps.DNT = appendAt(ps.DNT, minute, intProp(e, pre+"m_iDenyCount"))
			}

		case cn == "CDOTA_PlayerResource":
			// Убийства, смерти, помощь, уровень и участие в файтах лежат
			// отдельно от остального счёта — в общей на обе команды сущности,
			// где игроки нумеруются подряд от нуля до девяти.
			if int(p.Tick)-lastTotals[cn] < 2*tickRate {
				return nil
			}
			lastTotals[cn] = int(p.Tick)
			for i := 0; i < 10; i++ {
				slot := i
				if i >= 5 {
					slot = 128 + i - 5
				}
				readTeamData(e, i, &res.player(slot).Totals)
			}
			for i := 0; i < 24; i++ {
				pre := fmt.Sprintf("m_vecPlayerData.%04d.", i)
				steam, ok := e.GetUint64(pre + "m_iPlayerSteamID")
				if !ok {
					break
				}
				slot, ok := slotByAccount[int64(steam)-steamOffset]
				if !ok {
					continue // наблюдатель или пустая ячейка
				}
				readPlayerData(e, pre, &res.player(slot).Totals)
			}
		}
		return nil
	})

	// Снятые варды, смерти и установки вардов берём из боевого лога:
	// у сущностей этих сведений нет.
	p.Callbacks.OnCMsgDOTACombatLogEntry(func(entry *mdota.CMsgDOTACombatLogEntry) error {
		if entry.GetType() == mdota.DOTA_COMBATLOG_TYPES_DOTA_COMBATLOG_ITEM {
			inflictor, _ := p.LookupStringByIndex("CombatLogNames", int32(entry.GetInflictorName()))
			att, _ := p.LookupStringByIndex("CombatLogNames", int32(entry.GetAttackerName()))
			slot, ok := slots[att]
			if !ok {
				return nil
			}
			ps := res.player(slot)
			if ps.ItemUses == nil {
				ps.ItemUses = map[string]int{}
			}
			// Ключ без приставки item_: так его называет OpenDota, и так же
			// он записан в реестре показателей.
			ps.ItemUses[strings.TrimPrefix(inflictor, "item_")]++
			switch inflictor {
			case "item_ward_observer", "item_ward_sentry", "item_ward_dispenser":
				wardUses = append(wardUses, wardUse{Time: tick(), Slot: slot})
			}
			return nil
		}
		if entry.GetType() == mdota.DOTA_COMBATLOG_TYPES_DOTA_COMBATLOG_ABILITY {
			att, _ := p.LookupStringByIndex("CombatLogNames", int32(entry.GetAttackerName()))
			slot, ok := slots[att]
			if !ok {
				return nil
			}
			inflictor, _ := p.LookupStringByIndex("CombatLogNames", int32(entry.GetInflictorName()))
			ps := res.player(slot)
			if ps.AbilityUses == nil {
				ps.AbilityUses = map[string]int{}
			}
			ps.AbilityUses[inflictor]++
			return nil
		}
		if entry.GetType() == mdota.DOTA_COMBATLOG_TYPES_DOTA_COMBATLOG_DAMAGE {
			// Попадание по герою: считаем только урон героя по герою и только
			// от способности — обычные атаки нам не нужны.
			// Иллюзии носят имя своего героя, поэтому одной проверки имени
			// мало: удар по иллюзии — не попадание по герою, а урон от
			// иллюзии — не заслуга игрока. С обоими условиями сходимость с
			// OpenDota выше всего (124 из 128), хотя и не полная.
			if entry.GetIsTargetIllusion() || entry.GetIsAttackerIllusion() {
				return nil
			}
			tgt, _ := p.LookupStringByIndex("CombatLogNames", int32(entry.GetTargetName()))
			if _, isHero := slots[tgt]; !isHero {
				return nil
			}
			att, _ := p.LookupStringByIndex("CombatLogNames", int32(entry.GetAttackerName()))
			slot, ok := slots[att]
			if !ok {
				return nil
			}
			// Ключи приводим к виду, принятом у OpenDota: предметы без
			// приставки item_, урон без источника (обычная атака) — "null".
			// Иначе одни и те же величины лежали бы под разными именами.
			inflictor := "null"
			if entry.InflictorName != nil {
				if name, ok := p.LookupStringByIndex("CombatLogNames", int32(entry.GetInflictorName())); ok && name != "" {
					inflictor = strings.TrimPrefix(name, "item_")
				}
			}
			ps := res.player(slot)
			if ps.HeroHits == nil {
				ps.HeroHits = map[string]int{}
			}
			ps.HeroHits[inflictor]++
			return nil
		}
		if entry.GetType() == mdota.DOTA_COMBATLOG_TYPES_DOTA_COMBATLOG_BUYBACK {
			// В записи о выкупе игрок лежит в поле value — это его номер,
			// от нуля до девяти, а не слот.
			id := int(entry.GetValue())
			if id >= 0 && id < 10 {
				slot := id
				if id >= 5 {
					slot = 128 + id - 5
				}
				res.player(slot).BuybackCount++
			}
			return nil
		}
		if entry.GetType() == mdota.DOTA_COMBATLOG_TYPES_DOTA_COMBATLOG_HEAL {
			// Лечение считаем по боевому логу: поле m_fHealing в сущности —
			// другой счётчик, он завышает у Мипо и обнуляется у Джаггернаута.
			att, _ := p.LookupStringByIndex("CombatLogNames", int32(entry.GetAttackerName()))
			tgt, _ := p.LookupStringByIndex("CombatLogNames", int32(entry.GetTargetName()))
			slot, ok := slots[att]
			if !ok {
				return nil
			}
			ps := res.player(slot)
			if _, isHero := slots[tgt]; isHero && tgt != att {
				ps.HealAllies += int(entry.GetValue())
			}
			return nil
		}
		if entry.GetType() != mdota.DOTA_COMBATLOG_TYPES_DOTA_COMBATLOG_DEATH {
			return nil
		}
		target, _ := p.LookupStringByIndex("CombatLogNames", int32(entry.GetTargetName()))
		attacker, _ := p.LookupStringByIndex("CombatLogNames", int32(entry.GetAttackerName()))
		if strings.HasPrefix(target, "npc_dota_neutral_") {
			if slot, ok := slots[attacker]; ok {
				res.player(slot).NeutralKills++
			}
			return nil
		}
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
		// Итоги матча берём из реплея и перекрываем ими скорборд. Это и есть
		// смысл своего разбора: скорборд может прийти неразобранным и таким
		// остаться навсегда, а реплей полон всегда. Сверка с публичными
		// данными — команда --verify.
		if t := ps.Totals; t.Has {
			mins := m.DurationMinutes()
			p.Kills, p.Deaths, p.Assists = t.Kills, t.Died, t.Assists
			p.Level = t.Level
			p.LastHits, p.Denies = t.LastHits, t.Denies
			p.NetWorth = t.NetWorth
			if mins > 0 {
				p.GPM = int(float64(t.Gold)/mins + 0.5)
				p.XPM = int(float64(t.XP)/mins + 0.5)
			}
			p.HeroDamage, p.TowerDamage = t.HeroDamage, t.TowerDamage
			// Лечение намеренно не перекрываем: счёт Valve воспроизвести не
			// удалось ни по сущности, ни по боевому логу (docs/replay.md).
			p.Stuns = t.Stuns
			p.CampsStacked, p.HasStacks = t.CampsStacked, true
			p.RunePickups = t.RunePickups
			p.NeutralKills = ps.NeutralKills
			p.Buybacks = ps.BuybackCount
			p.WisdomShrines = t.WisdomShrines
			p.LotusesTaken = t.LotusesTaken
			if len(ps.ItemUses) > 0 {
				p.ItemUses = ps.ItemUses
			}
			if len(ps.AbilityUses) > 0 {
				p.AbilityUses = ps.AbilityUses
			}
			if len(ps.HeroHits) > 0 {
				p.HeroHits = ps.HeroHits
			}
			if lane, role := ps.Lane(p.IsRadiant); lane > 0 {
				p.Lane, p.LaneRole = lane, role
			}
			p.TeamfightParticipation = t.TeamfightParticipation
			// Счётчики вардов из сущности точнее нашего слежения: оно нужно
			// ради координат и времени жизни, а не ради количества.
			p.ObsPlaced, p.SenPlaced = t.ObsPlaced, t.SenPlaced
		}
		applied++
	}
	if applied > 0 {
		m.Detail = dota.DetailReplay
	}
	return applied
}

// readTeamTotals снимает счёт игрока с командной сущности.
// Ноль в поле — нормальное значение, поэтому Has взводится по факту того, что
// сущность вообще встретилась, а не по ненулевому счёту.
func readTeamTotals(e *manta.Entity, pre string, t *Totals) {
	t.Has = true
	t.LastHits = intProp(e, pre+"m_iLastHitCount")
	t.Denies = intProp(e, pre+"m_iDenyCount")
	t.NetWorth = intProp(e, pre+"m_iNetWorth")
	t.Gold = intProp(e, pre+"m_iTotalEarnedGold")
	t.XP = intProp(e, pre+"m_iTotalEarnedXP")
	t.CampsStacked = intProp(e, pre+"m_iCampsStacked")
	t.RunePickups = intProp(e, pre+"m_iRunePickups")
	t.ObsPlaced = intProp(e, pre+"m_iObserverWardsPlaced")
	t.SenPlaced = intProp(e, pre+"m_iSentryWardsPlaced")
	t.WardsDestroyed = intProp(e, pre+"m_iWardsDestroyed")
	t.WardsPurchased = intProp(e, pre+"m_iWardsPurchased")
	t.TPScrolls = intProp(e, pre+"m_iTPScrollsPurchased")
	t.SmokesUsed = intProp(e, pre+"m_iSmokesUsed")
	t.TowerKills = intProp(e, pre+"m_iTowerKills")
	t.RoshanKills = intProp(e, pre+"m_iRoshanKills")
	t.WisdomShrines = intProp(e, pre+"m_iWisdomShrinesTaken")
	t.LotusesTaken = intProp(e, pre+"m_iLotusesTaken")
	t.HeroDamage = int(floatProp(e, pre+"m_flHeroDamage"))
	t.TowerDamage = int(floatProp(e, pre+"m_flTowerDamage"))
	t.Healing = int(floatProp(e, pre+"m_fHealing"))
	t.Stuns = float64(floatProp(e, pre+"m_fStuns"))
	if v, ok := e.GetUint64(pre + "m_iPlayerSteamID"); ok && v > steamOffset {
		t.AccountID = int64(v) - steamOffset
	}
}

// readTeamData снимает то, что лежит в m_vecPlayerTeamData: эта часть
// нумеруется по игровому слоту, от нуля до девяти.
func readTeamData(e *manta.Entity, idx int, t *Totals) {
	pre := fmt.Sprintf("m_vecPlayerTeamData.%04d.", idx)
	t.Kills = intProp(e, pre+"m_iKills")
	t.Died = intProp(e, pre+"m_iDeaths")
	t.Assists = intProp(e, pre+"m_iAssists")
	t.Level = intProp(e, pre+"m_iLevel")
	t.TeamfightParticipation = float64(floatProp(e, pre+"m_flTeamFightParticipation"))
}

// readPlayerData снимает то, что лежит в m_vecPlayerData.
func readPlayerData(e *manta.Entity, pre string, t *Totals) {
	if v := intProp(e, pre+"m_iRankTier"); v > 0 {
		t.RankTier = v
	}
	if v, ok := e.GetString(pre + "m_iszPlayerName"); ok && v != "" {
		t.Name = v
	}
}

// floatProp читает вещественное поле, какого бы точного типа оно ни было.
func floatProp(e *manta.Entity, name string) float32 {
	if v, ok := e.GetFloat32(name); ok {
		return v
	}
	return 0
}

// laneWindow — до какой секунды считаем позиции лейнингом. Десять минут — тот
// же рубеж, по которому считаются добивания и эффективность линии.
const laneWindow = 600

// laneMid — полуширина центральной полосы в долях карты. Подобрана сверкой с
// OpenDota: у́же — центровые начинают попадать в боковые линии, шире — боковые
// затягивает в центр.
const laneMid = 0.14

// laneAt относит точку карты к линии: 1 нижняя, 2 центр, 3 верхняя, 0 — не
// определено.
//
// Координаты в реплее лежат примерно в диапазоне 64…192, начало отсчёта в углу
// Radiant. Центральная линия идёт по главной диагонали, нижняя проходит ниже
// неё, верхняя выше, поэтому достаточно знать, по какую сторону диагонали
// точка и насколько далеко.
func laneAt(x, y float64) int {
	const lo, span = 64.0, 128.0
	nx, ny := (x-lo)/span, (y-lo)/span
	if nx < 0 || nx > 1 || ny < 0 || ny > 1 {
		return 0
	}
	switch d := nx - ny; {
	case d > laneMid:
		return 1
	case d < -laneMid:
		return 3
	default:
		return 2
	}
}

// Lane возвращает линию игрока по голосованию позиций и её же в виде роли
// линии: 1 лёгкая, 2 центр, 3 сложная. Роль зависит от стороны — нижняя линия
// лёгкая для Radiant и сложная для Dire.
func (p *PlayerStats) Lane(radiant bool) (lane, role int) {
	best, votes := 0, 0
	for l := 1; l <= 3; l++ {
		if p.laneVotes[l] > votes {
			best, votes = l, p.laneVotes[l]
		}
	}
	if best == 0 {
		return 0, 0
	}
	role = best
	if !radiant {
		switch best {
		case 1:
			role = 3
		case 3:
			role = 1
		}
	}
	return best, role
}

// slotFromPlayerID достаёт слот игрока прямо из сущности героя.
//
// m_iPlayerID хранится удвоенным: у первого игрока 0, у второго 2 и так далее
// до 18. Это надёжнее имени: имя героя внутри игры может не совпадать с тем,
// как он называется в таблице матча.
func slotFromPlayerID(e *manta.Entity) (int, bool) {
	raw, ok := e.GetUint32("m_iPlayerID")
	if !ok || raw > 18 || raw%2 != 0 {
		return 0, false
	}
	idx := int(raw) / 2
	if idx < 5 {
		return idx, true
	}
	return 128 + idx - 5, true
}

// npcName переводит имя класса сущности в имя из боевого лога:
// CDOTA_Unit_Hero_SpiritBreaker -> npc_dota_hero_spirit_breaker.
func npcName(className string) string {
	name := strings.TrimPrefix(className, "CDOTA_Unit_Hero_")
	if name == className {
		return ""
	}
	var b strings.Builder
	for i, r := range name {
		if r >= 'A' && r <= 'Z' {
			if i > 0 && b.Len() > 0 && !strings.HasSuffix(b.String(), "_") {
				b.WriteByte('_')
			}
			b.WriteRune(r - 'A' + 'a')
			continue
		}
		b.WriteRune(r)
	}
	return "npc_dota_hero_" + b.String()
}
