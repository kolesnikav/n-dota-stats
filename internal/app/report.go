package app

import (
	"errors"
	"fmt"
	"html"
	"strconv"
	"strings"
	"time"

	"github.com/kolesnikav/n-dota-stats/internal/analysis"
	"github.com/kolesnikav/n-dota-stats/internal/dota"
	"github.com/kolesnikav/n-dota-stats/internal/mvp"
	"github.com/kolesnikav/n-dota-stats/internal/store"
	"github.com/kolesnikav/n-dota-stats/internal/telegram"
)

// errNotInMatch — игрока нет в составе. Обычное дело при обходе чужих матчей,
// не ошибка.
var errNotInMatch = errors.New("игрока нет в этом матче")

func clock(sec int) string { return fmt.Sprintf("%d:%02d", sec/60, sec%60) }

// matchTime печатает, когда игра началась, по часам сервера.
//
// Близкие даты называем словами: «вчера в 21:14» читается быстрее, чем
// «16.09 в 21:14», а именно близкие матчи и смотрят чаще всего. Год
// добавляется только когда он не нынешний — иначе это шум.
func matchTime(unix int64) string {
	if unix <= 0 {
		return ""
	}
	t := time.Unix(unix, 0)
	now := time.Now()
	day := func(x time.Time) time.Time {
		return time.Date(x.Year(), x.Month(), x.Day(), 0, 0, 0, 0, x.Location())
	}
	// Собираем из кусков, а не одним шаблоном: Go подставляет числа даже в
	// том, что выглядит обычным текстом, и однажды это выстрелит.
	at := t.Format("15:04")
	switch days := int(day(now).Sub(day(t)).Hours() / 24); {
	case days == 0:
		return "сегодня в " + at
	case days == 1:
		return "вчера в " + at
	case t.Year() == now.Year():
		return t.Format("02.01") + " в " + at
	default:
		return t.Format("02.01.2006") + " в " + at
	}
}

func esc(s string) string { return html.EscapeString(s) }

// Report — посчитанная сводка по матчу для одного пользователя.
type Report struct {
	Match  *dota.Match
	Player *dota.Player
	Ranked []mvp.Scored
	Place  int
	Full   []analysis.Line
	Metric map[string]float64 // числовые значения показателей для истории
	Snap   analysis.Snapshot  // то, что кладётся в базу
}

// Build считает всё, что нужно для сводки.
func (a *App) Build(m *dota.Match, accountID int64) (*Report, error) {
	p := m.Find(accountID)
	if p == nil {
		return nil, errNotInMatch
	}
	weights := mvp.EqualWeights()
	if w, ok := a.DB.Weights(accountID); ok && len(w) == mvp.Dim() {
		weights = w
	}
	ranked := mvp.Rank(m, weights)
	snap := analysis.Snap(m, p)
	place := mvp.Place(ranked, p)
	snap.Place, snap.Players = place, len(ranked)
	for i, sc := range ranked {
		if sc.Player == p {
			snap.Score = sc.Score
		}
		if i < 3 {
			snap.Top = append(snap.Top, analysis.SnapTop{
				Name: sc.Player.Name(), Side: sc.Player.SideName(),
				Score: sc.Score, Me: sc.Player == p,
			})
		}
	}
	rep := &Report{
		Match:  m,
		Player: p,
		Ranked: ranked,
		Place:  place,
		Snap:   snap,
		Full:   snap.Render(a.DB, false),
		Metric: snap.Numbers(),
	}
	return rep, nil
}

func (r *Report) header() []string {
	outcome := "Поражение"
	if r.Snap.Win {
		outcome = "Победа"
	}
	line := fmt.Sprintf("<b>%s</b> · %s", outcome, clock(r.Snap.Duration))
	if when := matchTime(r.Snap.StartTime); when != "" {
		line += " · " + when
	}
	head := []string{line}

	second := fmt.Sprintf("%s · %s · %d/%d/%d",
		esc(r.Snap.Hero), esc(r.Snap.Role.String()), r.Snap.Kills, r.Snap.Deaths, r.Snap.Assists)
	if medal := dota.RankTierName(r.Snap.RankTier); medal != "" {
		second += " · " + medal
	}
	return append(head, second)
}

// marker показывает одним знаком, хорошо это или плохо.
func marker(verdict int) string {
	switch verdict {
	case analysis.VerdictGood:
		return "▲"
	case analysis.VerdictBad:
		return "▼"
	case analysis.VerdictEven:
		return "▪"
	default:
		return "·"
	}
}

func (r *Report) body() []string {
	byGroup := map[string][]analysis.Line{}
	for _, l := range r.Full {
		g := l.Group
		if g == "" {
			g = "Прочее"
		}
		byGroup[g] = append(byGroup[g], l)
	}
	// Сначала общие разделы в заданном порядке, затем разделы, названные по
	// герою, — они появляются динамически и в списке Groups их нет.
	order := append([]string(nil), analysis.Groups...)
	seen := map[string]bool{}
	for _, g := range order {
		seen[g] = true
	}
	for _, l := range r.Full {
		g := l.Group
		if g == "" {
			g = "Прочее"
		}
		if !seen[g] {
			seen[g] = true
			order = append(order, g)
		}
	}

	var out []string
	for _, g := range order {
		lines := byGroup[g]
		if len(lines) == 0 {
			continue
		}
		out = append(out, "", "<b>"+strings.ToUpper(g)+"</b>")
		for _, l := range lines {
			row := fmt.Sprintf("%s %s — <b>%s</b>", marker(l.Verdict), esc(l.Label), esc(l.Value))
			if len(l.Notes) > 0 {
				row += " <i>· " + esc(strings.Join(l.Notes, " · ")) + "</i>"
			}
			out = append(out, row)
		}
	}
	return out
}

func (r *Report) top3() []string {
	if len(r.Snap.Top) == 0 {
		return nil
	}
	out := []string{"", "<b>ЛУЧШИЕ ПО МОЕЙ ФОРМУЛЕ</b>"}
	for i, t := range r.Snap.Top {
		mark := ""
		if t.Me {
			mark = " ← ты"
		}
		out = append(out, fmt.Sprintf("%d. %s · %s · %.0f%s",
			i+1, esc(t.Name), t.Side, t.Score*100, mark))
	}
	if r.Snap.Place > 3 {
		row := fmt.Sprintf("Ты — <b>%d-е место</b> из %d", r.Snap.Place, r.Snap.Players)
		// Цифра нужна всегда, а не только когда попал в тройку: по ней видно,
		// отстал ты на волос или вдвое.
		row += fmt.Sprintf(" · <b>%.0f</b>", r.Snap.Score*100)
		out = append(out, row)
	}
	return out
}

func (r *Report) footer() []string {
	roleNote := "роль посчитана"
	if r.Snap.RoleManual {
		roleNote = "роль указана тобой"
	}
	tail := fmt.Sprintf("<i>Матч %d · %s</i>", r.Snap.MatchID, roleNote)
	if r.Snap.Partial {
		tail += "\n<i>Реплей ещё не разобран — часть строк появится позже.</i>"
	}
	return []string{"", tail}
}

// Text собирает всю сводку целиком: без кнопки «подробнее», всё сразу.
func (r *Report) Text() string {
	lines := r.header()
	lines = append(lines, r.body()...)
	lines = append(lines, r.top3()...)
	lines = append(lines, r.footer()...)
	return strings.Join(lines, "\n")
}

// Keyboard — кнопки под сводкой.
func (r *Report) Keyboard() telegram.Keyboard {
	id := strconv.FormatInt(r.Snap.MatchID, 10)
	return telegram.Keyboard{
		{{Text: "сменить роль", Data: "r:" + id}},
	}
}

// roleKeyboard — выбор роли.
func roleKeyboard(matchID int64) telegram.Keyboard {
	id := strconv.FormatInt(matchID, 10)
	var row []telegram.Button
	kb := telegram.Keyboard{}
	for role := dota.RoleCarry; role <= dota.RoleHard; role++ {
		row = append(row, telegram.Button{
			Text: fmt.Sprintf("%d · %s", int(role), role.String()),
			Data: fmt.Sprintf("r:%s:%d", id, int(role)),
		})
		if len(row) == 2 {
			kb = append(kb, row)
			row = nil
		}
	}
	if len(row) > 0 {
		kb = append(kb, row)
	}
	return kb
}

// mvpKeyboard — кнопки разметки реального топ-3.
func mvpKeyboard(matchID int64, ranked []mvp.Scored, step int, chosen map[int]bool) telegram.Keyboard {
	id := strconv.FormatInt(matchID, 10)
	kb := telegram.Keyboard{}
	var row []telegram.Button
	for place, s := range ranked {
		if chosen[s.Player.Slot] {
			continue
		}
		label := fmt.Sprintf("%s %s", s.Player.Name(), sideLetter(s.Player))
		if place < 3 {
			label = fmt.Sprintf("%d· %s", place+1, label)
		}
		row = append(row, telegram.Button{
			Text: label,
			Data: fmt.Sprintf("m:%s:%d:%d", id, s.Player.Slot, step),
		})
		if len(row) == 2 {
			kb = append(kb, row)
			row = nil
		}
	}
	if len(row) > 0 {
		kb = append(kb, row)
	}
	kb = append(kb, []telegram.Button{{Text: "пропустить", Data: fmt.Sprintf("m:%s:x:%d", id, step)}})
	return kb
}

func sideLetter(p *dota.Player) string {
	if p.IsRadiant {
		return "R"
	}
	return "D"
}

var stepQuestion = map[int]string{
	1: "Кого Dota показала <b>лучшим игроком</b>?",
	2: "Кто был <b>вторым</b> на экране?",
	3: "Кто был <b>третьим</b>?",
}

// usersCard рисует карточку пользователя для админки.
func usersCard(users []store.User, idx int, db *store.DB) (string, telegram.Keyboard) {
	if len(users) == 0 {
		return "Пользователей пока нет.", nil
	}
	if idx < 0 {
		idx = 0
	}
	if idx >= len(users) {
		idx = len(users) - 1
	}
	u := users[idx]
	statusName := map[store.Status]string{
		store.StatusPending:  "ожидает подтверждения",
		store.StatusVerified: "подтверждён",
		store.StatusBlocked:  "заблокирован",
		store.StatusAdmin:    "админ",
	}[u.Status]

	lines := []string{
		fmt.Sprintf("<b>Пользователь %d из %d</b>", idx+1, len(users)),
		"",
		fmt.Sprintf("%s · аккаунт <code>%d</code>", esc(u.Nickname), u.AccountID),
		"Статус: <b>" + statusName + "</b>",
	}
	count := db.MatchCount(u.AccountID)
	last, _ := db.UserMatches(u.AccountID, 1)
	tail := fmt.Sprintf("Матчей в базе: %d", count)
	if len(last) > 0 {
		outcome := "поражение"
		if last[0].Win {
			outcome = "победа"
		}
		tail += fmt.Sprintf(" · последний: %s, %s", esc(last[0].HeroName), outcome)
	}
	lines = append(lines, tail)

	nav := []telegram.Button{}
	if idx > 0 {
		nav = append(nav, telegram.Button{Text: "◀", Data: fmt.Sprintf("u:p:%d", idx-1)})
	}
	nav = append(nav, telegram.Button{
		Text: fmt.Sprintf("%d/%d", idx+1, len(users)),
		Data: fmt.Sprintf("u:p:%d", idx),
	})
	if idx < len(users)-1 {
		nav = append(nav, telegram.Button{Text: "▶", Data: fmt.Sprintf("u:p:%d", idx+1)})
	}

	var actions []telegram.Button
	chat := strconv.FormatInt(u.ChatID, 10)
	switch u.Status {
	case store.StatusPending:
		actions = []telegram.Button{
			{Text: "принять", Data: "u:a:" + chat + ":ok"},
			{Text: "отклонить", Data: "u:a:" + chat + ":no"},
		}
	case store.StatusVerified:
		actions = []telegram.Button{
			{Text: "заблокировать", Data: "u:a:" + chat + ":block"},
			{Text: "сделать админом", Data: "u:a:" + chat + ":admin"},
		}
	case store.StatusBlocked:
		actions = []telegram.Button{{Text: "разблокировать", Data: "u:a:" + chat + ":ok"}}
	case store.StatusAdmin:
		if db.AdminCount() > 1 {
			actions = []telegram.Button{{Text: "снять права", Data: "u:a:" + chat + ":demote"}}
		}
	}

	kb := telegram.Keyboard{nav}
	if len(actions) > 0 {
		kb = append(kb, actions)
	}
	kb = append(kb, []telegram.Button{{Text: "игры", Data: "u:g:" + chat + ":" + strconv.Itoa(idx)}})
	return strings.Join(lines, "\n"), kb
}
