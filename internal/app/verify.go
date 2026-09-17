package app

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/kolesnikav/n-dota-stats/internal/dota"
	"github.com/kolesnikav/n-dota-stats/internal/replay"
)

// Сверка своего разбора с публичными данными.
//
// Смысл: итоги матча теперь считаются по реплею, а не берутся из чужого
// скорборда. Такое доверие нужно чем-то обосновать, поэтому есть команда,
// которая кладёт рядом наше значение и значение OpenDota по каждому игроку и
// каждой метрике. Расхождение — либо наша ошибка, либо разная методика, и
// разбираться нужно в обоих случаях.

// Field — одна сверяемая величина.
type Field struct {
	Key   string
	Label string
	// Tolerance — допустимое расхождение в долях. 0 означает «обязано совпасть
	// точно»: счётчики вроде убийств или добиваний считаются одинаково и
	// расходиться не имеют права.
	Tolerance float64
	// mins — длительность матча в минутах: нужна тем метрикам, что
	// считаются в единицах за минуту.
	Ours func(t *replay.Totals, mins float64) float64
	// OursFull — если задано, значение берётся отсюда: некоторые величины
	// считаются не по сущностям, а по боевому логу.
	OursFull func(ps *replay.PlayerStats, mins float64) float64
	Theirs   func(*dota.Player) float64
}

// VerifyFields — что сверяем. Порядок задаёт порядок вывода.
var VerifyFields = []Field{
	{Key: "kills", Label: "убийства", Ours: func(t *replay.Totals, _ float64) float64 { return float64(t.Kills) },
		Theirs: func(p *dota.Player) float64 { return float64(p.Kills) }},
	{Key: "deaths", Label: "смерти", Ours: func(t *replay.Totals, _ float64) float64 { return float64(t.Died) },
		Theirs: func(p *dota.Player) float64 { return float64(p.Deaths) }},
	{Key: "assists", Label: "помощь", Ours: func(t *replay.Totals, _ float64) float64 { return float64(t.Assists) },
		Theirs: func(p *dota.Player) float64 { return float64(p.Assists) }},
	{Key: "level", Label: "уровень", Ours: func(t *replay.Totals, _ float64) float64 { return float64(t.Level) },
		Theirs: func(p *dota.Player) float64 { return float64(p.Level) }},
	{Key: "last_hits", Label: "добивания", Ours: func(t *replay.Totals, _ float64) float64 { return float64(t.LastHits) },
		Theirs: func(p *dota.Player) float64 { return float64(p.LastHits) }},
	{Key: "denies", Label: "денаи", Ours: func(t *replay.Totals, _ float64) float64 { return float64(t.Denies) },
		Theirs: func(p *dota.Player) float64 { return float64(p.Denies) }},
	{Key: "net_worth", Label: "нетворс", Tolerance: 0.02, Ours: func(t *replay.Totals, _ float64) float64 { return float64(t.NetWorth) },
		Theirs: func(p *dota.Player) float64 { return float64(p.NetWorth) }},
	{Key: "hero_damage", Label: "урон по героям", Tolerance: 0.02, Ours: func(t *replay.Totals, _ float64) float64 { return float64(t.HeroDamage) },
		Theirs: func(p *dota.Player) float64 { return float64(p.HeroDamage) }},
	{Key: "tower_damage", Label: "урон по строениям", Tolerance: 0.02, Ours: func(t *replay.Totals, _ float64) float64 { return float64(t.TowerDamage) },
		Theirs: func(p *dota.Player) float64 { return float64(p.TowerDamage) }},
	{Key: "healing", Label: "лечение", Tolerance: 0.02, Ours: func(t *replay.Totals, _ float64) float64 { return float64(t.Healing) },
		Theirs: func(p *dota.Player) float64 { return float64(p.HeroHealing) }},
	{Key: "stuns", Label: "контроль", Tolerance: 0.01, Ours: func(t *replay.Totals, _ float64) float64 { return t.Stuns },
		Theirs: func(p *dota.Player) float64 { return p.Stuns }},
	{Key: "camps_stacked", Label: "стаки", Ours: func(t *replay.Totals, _ float64) float64 { return float64(t.CampsStacked) },
		Theirs: func(p *dota.Player) float64 { return float64(p.CampsStacked) }},
	{Key: "rune_pickups", Label: "руны", Ours: func(t *replay.Totals, _ float64) float64 { return float64(t.RunePickups) },
		Theirs: func(p *dota.Player) float64 { return float64(p.RunePickups) }},
	{Key: "obs_placed", Label: "обзорные варды", Ours: func(t *replay.Totals, _ float64) float64 { return float64(t.ObsPlaced) },
		Theirs: func(p *dota.Player) float64 { return float64(p.ObsPlaced) }},
	{Key: "sen_placed", Label: "сентри", Ours: func(t *replay.Totals, _ float64) float64 { return float64(t.SenPlaced) },
		Theirs: func(p *dota.Player) float64 { return float64(p.SenPlaced) }},
	{Key: "teamfight", Label: "участие в файтах", Tolerance: 0.05, Ours: func(t *replay.Totals, _ float64) float64 { return t.TeamfightParticipation },
		Theirs: func(p *dota.Player) float64 { return p.TeamfightParticipation }},
	{Key: "gpm", Label: "золото в минуту", Tolerance: 0.02,
		Ours:   func(t *replay.Totals, mins float64) float64 { return float64(t.Gold) / mins },
		Theirs: func(p *dota.Player) float64 { return float64(p.GPM) }},
	{Key: "xpm", Label: "опыт в минуту", Tolerance: 0.02,
		Ours:   func(t *replay.Totals, mins float64) float64 { return float64(t.XP) / mins },
		Theirs: func(p *dota.Player) float64 { return float64(p.XPM) }},
}

// Медаль намеренно не сверяется. В реплее лежит ранг на момент матча, а
// OpenDota отдаёт текущий ранг из профиля игрока — это разные величины, и их
// расхождение ничего не говорит о качестве разбора. Вдобавок игрок может
// скрыть медаль, и тогда в реплее ноль.

// Mismatch — одно расхождение.
type Mismatch struct {
	Slot   int
	Hero   string
	Field  string
	Ours   float64
	Theirs float64
}

// FieldReport — итог по одной метрике.
type FieldReport struct {
	Label      string
	Checked    int // по скольким игрокам было что сравнивать
	Agreed     int
	Missing    int // у OpenDota значения нет
	Mismatches []Mismatch
}

// VerifyResult — итог сверки матча.
type VerifyResult struct {
	MatchID int64
	Fields  []FieldReport
}

// Verify сравнивает наш разбор реплея с данными OpenDota.
//
// ref — матч в том виде, в каком его отдала OpenDota; res — наш разбор.
func Verify(matchID int64, ref *dota.Match, res *replay.Result) *VerifyResult {
	out := &VerifyResult{MatchID: matchID}
	mins := ref.DurationMinutes()
	for _, f := range VerifyFields {
		rep := FieldReport{Label: f.Label}
		for _, p := range ref.Players {
			ps, ok := res.Players[p.Slot]
			if !ok || !ps.Totals.Has {
				continue
			}
			ours := 0.0
			if f.OursFull != nil {
				ours = f.OursFull(ps, mins)
			} else {
				ours = f.Ours(&ps.Totals, mins)
			}
			theirs := f.Theirs(p)
			// Нулевое значение у OpenDota по неразобранному матчу означает
			// «не считали», а не «ноль». Отличить можно только по тому, что
			// вся метрика пуста у всех десяти игроков, — это учитывается ниже.
			rep.Checked++
			if agree(ours, theirs, f.Tolerance) {
				rep.Agreed++
				continue
			}
			rep.Mismatches = append(rep.Mismatches, Mismatch{
				Slot: p.Slot, Hero: p.Name(), Field: f.Label, Ours: ours, Theirs: theirs,
			})
		}
		// Если у них по всем игрокам ноль, а у нас нет — метрику они просто не
		// считали. Это не расхождение, а отсутствие данных на их стороне.
		if allTheirsZero(ref, res, f) {
			rep.Missing = rep.Checked
			rep.Mismatches = nil
			rep.Agreed = 0
		}
		out.Fields = append(out.Fields, rep)
	}
	return out
}

func agree(a, b, tol float64) bool {
	if a == b {
		return true
	}
	if tol <= 0 {
		return false
	}
	scale := math.Max(math.Abs(a), math.Abs(b))
	if scale == 0 {
		return true
	}
	return math.Abs(a-b)/scale <= tol
}

func allTheirsZero(ref *dota.Match, res *replay.Result, f Field) bool {
	mins := ref.DurationMinutes()
	var theirsSum, oursSum float64
	for _, p := range ref.Players {
		ps, ok := res.Players[p.Slot]
		if !ok || !ps.Totals.Has {
			continue
		}
		theirsSum += math.Abs(f.Theirs(p))
		if f.OursFull != nil {
			oursSum += math.Abs(f.OursFull(ps, mins))
		} else {
			oursSum += math.Abs(f.Ours(&ps.Totals, mins))
		}
	}
	return theirsSum == 0 && oursSum != 0
}

// Text печатает отчёт.
func (v *VerifyResult) Text() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Сверка своего разбора с OpenDota по матчу %d\n\n", v.MatchID)
	for _, f := range v.Fields {
		switch {
		case f.Checked == 0:
			fmt.Fprintf(&b, "  %-20s нечего сравнивать\n", f.Label)
		case f.Missing > 0:
			fmt.Fprintf(&b, "  %-20s у OpenDota нет данных, у нас есть\n", f.Label)
		case len(f.Mismatches) == 0:
			fmt.Fprintf(&b, "  %-20s сходится по всем %d игрокам\n", f.Label, f.Checked)
		default:
			fmt.Fprintf(&b, "  %-20s расходится у %d из %d\n", f.Label, len(f.Mismatches), f.Checked)
			ms := append([]Mismatch(nil), f.Mismatches...)
			sort.Slice(ms, func(i, j int) bool { return ms[i].Slot < ms[j].Slot })
			for _, m := range ms {
				fmt.Fprintf(&b, "      %-18s у нас %10.2f · у них %10.2f\n", m.Hero, m.Ours, m.Theirs)
			}
		}
	}
	return b.String()
}

// Agreed сообщает, всё ли сошлось.
func (v *VerifyResult) Agreed() bool {
	for _, f := range v.Fields {
		if len(f.Mismatches) > 0 {
			return false
		}
	}
	return true
}
