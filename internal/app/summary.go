package app

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/kolesnikav/n-dota-stats/internal/analysis"
	"github.com/kolesnikav/n-dota-stats/internal/dota"
	"github.com/kolesnikav/n-dota-stats/internal/heatmap"
	"github.com/kolesnikav/n-dota-stats/internal/telegram"
)

// Сводка уходит текстом, карта — отдельной картинкой по кнопке.
//
// Иначе нельзя: подпись к картинке ограничена 1024 символами, а сводка в них
// не помещается. Замерено по 260 накопленным сводкам — медиана 973, максимум
// 1096, за предел выходит каждая восьмая. Урезать сводку ради того, чтобы она
// влезла под картинку, значит выкидывать показатели, ради которых всё и
// затевалось.
const captionLimit = 1024

// Окна тепловой карты.
const (
	windowMatch = "all"
	windowLane  = "lane"
)

// laneWindow — до какой секунды карта считается картой стадии линий.
const laneWindow = 600

// mapFor рисует карту нужного окна по снимку.
func mapFor(snap analysis.Snapshot, window string) []byte {
	path := snap.Path.Points()
	if len(path) == 0 {
		return nil
	}
	deaths := make([]dota.Point, 0, len(snap.DeathsAt))
	for _, d := range snap.DeathsAt {
		deaths = append(deaths, dota.Point{T: d.T, X: float64(d.X) / 4, Y: float64(d.Y) / 4})
	}
	to := 0
	if window == windowLane {
		to = laneWindow
	}
	img, err := heatmap.Render(heatmap.Options{Path: path, Deaths: deaths, To: to})
	if err != nil {
		return nil
	}
	return img
}

// VisibleLen считает символы так же, как телеграм: разметка в предел не идёт.
func VisibleLen(html string) int {
	var n, depth int
	for _, r := range html {
		switch {
		case r == '<':
			depth++
		case r == '>' && depth > 0:
			depth--
		case depth == 0:
			n++
		}
	}
	return n
}

// caption готовит подпись к картинке. Разметку срезаем целиком, а не режем
// строку по символам: обрубленный тег телеграм не принимает вовсе, и вместо
// картинки не приходит ничего.
func caption(text string) string {
	if VisibleLen(text) <= captionLimit {
		return text
	}
	plain := stripTags(text)
	runes := []rune(plain)
	if len(runes) > captionLimit-1 {
		runes = runes[:captionLimit-1]
	}
	return string(runes) + "…"
}

func stripTags(html string) string {
	var b strings.Builder
	depth := 0
	for _, r := range html {
		switch {
		case r == '<':
			depth++
		case r == '>' && depth > 0:
			depth--
		case depth == 0:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// snapshotOf достаёт сохранённую сводку.
func (a *App) snapshotOf(matchID, accountID int64) (analysis.Snapshot, bool) {
	blob, ok := a.DB.Snapshot(matchID, accountID)
	if !ok {
		return analysis.Snapshot{}, false
	}
	var snap analysis.Snapshot
	if err := json.Unmarshal(blob, &snap); err != nil {
		a.Log("снимок матча %d: %v", matchID, err)
		return analysis.Snapshot{}, false
	}
	return snap, true
}

// reportFromSnapshot собирает сводку из сохранённого снимка, без матча.
func (a *App) reportFromSnapshot(accountID, matchID int64) (*Report, bool) {
	snap, ok := a.snapshotOf(matchID, accountID)
	if !ok || len(snap.Lines) == 0 {
		return nil, false
	}
	return &Report{Snap: snap, Full: snap.Render(a.DB, false)}, true
}

// summaryKeyboard — кнопки под текстовой сводкой.
//
// Разметку предлагаем только пока её нет: повторно указывать лучших незачем,
// а кнопка, которая ничего не меняет, только занимает место.
func (a *App) summaryKeyboard(matchID, accountID int64, ctx viewCtx, extra ...[]telegram.Button) telegram.Keyboard {
	kb := telegram.Keyboard{mapRow(matchID, accountID, ctx)}
	if len(a.DB.Actual(matchID, accountID)) == 0 {
		kb = append(kb, markRow(matchID, accountID, ctx))
	}
	return append(kb, extra...)
}

// SendSummary отправляет текстовую сводку.
func (a *App) SendSummary(chatID int64, text string, rep *Report, ctx viewCtx, extra ...[]telegram.Button) (int64, error) {
	return a.Bot.Send(chatID, text,
		a.summaryKeyboard(rep.Snap.MatchID, rep.Snap.AccountID, ctx, extra...))
}

// EditSummary правит текстовую сводку на месте.
func (a *App) EditSummary(chatID, msgID int64, text string, rep *Report, ctx viewCtx, extra ...[]telegram.Button) error {
	return a.Bot.Edit(chatID, msgID, text,
		a.summaryKeyboard(rep.Snap.MatchID, rep.Snap.AccountID, ctx, extra...))
}

// summaryView собирает сводку и кнопки для места, откуда её открыли.
func (a *App) summaryView(chatID, accountID, matchID int64, ctx viewCtx) (string, [][]telegram.Button, bool) {
	switch ctx.Kind {
	case "h":
		page, ok := a.historyPage(accountID, ctx.Page)
		if !ok {
			return "", nil, false
		}
		return page.Text, [][]telegram.Button{navRow(page.Index, page.Total, ownHistoryData)}, true
	case "u":
		u, ok := a.DB.User(ctx.Target)
		if !ok {
			return "", nil, false
		}
		page, ok := a.historyPage(u.AccountID, ctx.Page)
		if !ok {
			return "", nil, false
		}
		data := func(n int) string { return fmt.Sprintf("u:g:%d:%d:%d", ctx.Target, ctx.Card, n) }
		back := []telegram.Button{{Text: "назад", Data: fmt.Sprintf("u:p:%d", ctx.Card)}}
		title := fmt.Sprintf("<b>Матчи %s</b>\n", esc(u.Nickname))
		return title + page.Text, [][]telegram.Button{
			navRow(page.Index, page.Total, data), back,
		}, true
	default:
		rep, ok := a.reportFromSnapshot(accountID, matchID)
		if !ok {
			return "", nil, false
		}
		return rep.Text(), [][]telegram.Button{roleRow(matchID)}, true
	}
}
