package analysis

import "github.com/kolesnikav/n-dota-stats/internal/dota"

// Снимок сводки: что посчитано по матчу, отдельно от того, что считается при
// показе.
//
// Разделение нужно не ради экономии. Значения показателей и сравнение с
// соперником по линии — факты матча, они не меняются никогда. Медиана роли и
// своё среднее меняются: медианы обновляются раз в неделю, среднее растёт с
// каждой игрой. Храня первое и пересчитывая второе, мы получаем историю,
// которая не врёт про прошлое и при этом сравнивает с сегодняшним днём.

// laneEnd — до какой секунды считаем происходящее стадией линий. Тот же
// рубеж, что у добиваний к 10:00 и эффективности линии.
const laneEnd = 600

// SnapLine — один показатель в снимке.
type SnapLine struct {
	Key   string   `json:"k"`
	Group string   `json:"g"`
	Label string   `json:"l"`
	Value string   `json:"v"`
	Num   float64  `json:"n"`
	Has   bool     `json:"h"`
	Fixed []string `json:"f,omitempty"` // пометки, привязанные к матчу
}

// SnapTop — строка рейтинга «лучшие по моей формуле».
type SnapTop struct {
	Name  string  `json:"n"`
	Side  string  `json:"s"`
	Score float64 `json:"v"`
	Me    bool    `json:"me,omitempty"`
}

// Snapshot — сводка матча в том виде, в каком она кладётся в базу.
//
// Здесь всё, что нужно, чтобы показать матч, не поднимая его заново: шапка,
// показатели и рейтинг. Не хватает только сравнений с сегодняшним днём — они
// и считаются при показе.
type Snapshot struct {
	MatchID   int64     `json:"match"`
	AccountID int64     `json:"account"`
	Role      dota.Role `json:"role"`
	HeroID    int       `json:"hero"`

	Hero       string `json:"hero_name"`
	StartTime  int64  `json:"start,omitempty"`
	Duration   int    `json:"duration"`
	Win        bool   `json:"win"`
	Kills      int    `json:"k"`
	Deaths     int    `json:"d"`
	Assists    int    `json:"a"`
	RankTier   int    `json:"rank,omitempty"`
	RoleManual bool   `json:"role_manual,omitempty"`
	Partial    bool   `json:"partial,omitempty"` // реплей ещё не разобран

	Lines []SnapLine `json:"lines"`

	// Heat — где игрок был на линии и где за весь матч. Лежит в снимке, а не
	// считается при показе, потому что путь героя есть только в разборе
	// реплея, и держать его ради этого негде.
	HeatLane  Heat `json:"heat_lane,omitempty"`
	HeatMatch Heat `json:"heat_match,omitempty"`

	Top     []SnapTop `json:"top,omitempty"`
	Place   int       `json:"place,omitempty"`
	Score   float64   `json:"score,omitempty"`
	Players int       `json:"players,omitempty"`
}

// Snap считает всё, что зависит от матча.
func Snap(m *dota.Match, p *dota.Player) Snapshot {
	c := &Ctx{Match: m, Player: p, Opponent: LaneOpponent(m, p)}
	snap := Snapshot{
		MatchID: m.ID, AccountID: p.AccountID, Role: p.Role, HeroID: p.HeroID,
		Hero: p.Name(), StartTime: m.StartTime, Duration: m.Duration, Win: p.Win,
		Kills: p.Kills, Deaths: p.Deaths, Assists: p.Assists,
		RankTier:   p.RankTier,
		RoleManual: p.RoleSource == dota.SourceManual,
		Partial:    m.Detail < dota.DetailMeta,
	}
	if len(p.Path) > 0 {
		snap.HeatLane = HeatMap(p, 0, laneEnd)
		snap.HeatMatch = HeatMap(p, 0, 0)
	}
	for _, metric := range MetricsFor(p.Role, p.HeroID, false) {
		if metric.Needs > m.Detail {
			continue
		}
		v, ok := metric.Calc(c)
		if !ok || v.Text == "" {
			continue
		}
		snap.Lines = append(snap.Lines, SnapLine{
			Key:   metric.Key,
			Group: metric.GroupFor(p),
			Label: metric.Label,
			Value: v.Text,
			Num:   v.Num,
			Has:   v.Has,
			Fixed: metric.fixedNotes(c, v),
		})
	}
	return snap
}

// Render достраивает снимок до готовых строк: добавляет сравнения, которые
// зависят от сегодняшнего дня, и считает вердикт.
func (s Snapshot) Render(hist History, short bool) []Line {
	// Для сравнений нужен игрок, но только его роль, герой и аккаунт: сам матч
	// здесь уже не нужен, всё матчезависимое лежит в снимке.
	p := &dota.Player{AccountID: s.AccountID, Role: s.Role, HeroID: s.HeroID}
	c := &Ctx{Player: p, History: hist}

	var out []Line
	var top []rankedLine
	for _, sl := range s.Lines {
		metric, ok := metricByKey(sl.Key)
		if !ok {
			continue
		}
		if short && !metric.Short {
			continue
		}
		v := Value{Text: sl.Value, Num: sl.Num, Has: sl.Has}
		line := Line{
			Group:   sl.Group,
			Label:   sl.Label,
			Value:   sl.Value,
			Notes:   mergeNotes(metric.movingNotes(c, v), sl.Fixed),
			Verdict: metric.verdict(c, v),
		}
		if idx, ok := priorityIndex(s.Role, metric.Key); ok && !metric.HeroSpecific() {
			line.Group = "Главное"
			top = append(top, rankedLine{idx, line})
			continue
		}
		out = append(out, line)
	}
	return append(sortRanked(top), out...)
}

// Numbers — числовые значения показателей для истории и проверок.
func (s Snapshot) Numbers() map[string]float64 {
	out := make(map[string]float64, len(s.Lines))
	for _, sl := range s.Lines {
		if sl.Has {
			out[sl.Key] = sl.Num
		}
	}
	return out
}

func metricByKey(key string) (Metric, bool) {
	for _, m := range Registry {
		if m.Key == key {
			return m, true
		}
	}
	return Metric{}, false
}
