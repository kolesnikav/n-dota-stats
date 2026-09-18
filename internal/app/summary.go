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

// Сводка отправляется картинкой с подписью, когда карта есть.
//
// Телеграм не даёт превратить текстовое сообщение в сообщение с картинкой:
// тип задаётся при отправке. Поэтому, чтобы карту можно было листать в том же
// сообщении, сводка сразу уходит картинкой, а текст идёт подписью. Подпись
// ограничена 1024 символами — сводки укладываются с запасом, самая длинная из
// накопленных занимала 880.
const captionLimit = 1024

// KindPhoto помечает сводку, отправленную картинкой.
const KindPhoto = "photo"

// Окна тепловой карты.
const (
	windowMatch = "all"
	windowLane  = "lane"
)

// mapFor рисует карту нужного окна по снимку.
//
// Пустой путь — не повод отказываться от картинки: рисуем голую карту. Так все
// сводки остаются сообщениями одного вида, и когда реплей разберётся, картинка
// заменится на месте. Иначе пришлось бы держать два вида сообщений и знать,
// каким именно отправлена каждая сводка.
func mapFor(snap analysis.Snapshot, window string) []byte {
	path := snap.Path.Points()
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

// laneWindow — до какой секунды карта считается картой стадии линий.
const laneWindow = 600

// caption готовит подпись: тот же текст сводки, урезанный под предел телеграма.
//
// Урезаем с конца и по разделам, а не по символам: обрубленная на полуслове
// строка выглядит поломкой, а сводка без рейтинга — просто короче.
func caption(text string) string {
	if visibleLen(text) <= captionLimit {
		return text
	}
	if cut := strings.Index(text, "ЛУЧШИЕ ПО МОЕЙ ФОРМУЛЕ"); cut > 0 {
		short := strings.TrimRight(text[:cut], "<b>\n ")
		if visibleLen(short) <= captionLimit {
			return short
		}
	}
	runes := []rune(text)
	return string(runes[:captionLimit-1]) + "…"
}

// visibleLen считает символы так же, как телеграм: разметка в предел не идёт.
func visibleLen(html string) int {
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

// summaryKeyboard — кнопки под сводкой с учётом того, какая карта показана.
func summaryKeyboard(matchID, accountID int64, window string, extra ...[]telegram.Button) telegram.Keyboard {
	kb := telegram.Keyboard{mapSwitch(matchID, accountID, window)}
	kb = append(kb, extra...)
	return kb
}

// SendSummary отправляет сводку: картинкой с подписью, если карта есть.
// Возвращает номер сообщения и его вид.
func (a *App) SendSummary(chatID int64, rep *Report, extra ...[]telegram.Button) (int64, string, error) {
	return a.sendSummaryText(chatID, caption(rep.Text()), rep, extra...)
}

// sendSummaryText отправляет готовую подпись картинкой.
func (a *App) sendSummaryText(chatID int64, text string, rep *Report, extra ...[]telegram.Button) (int64, string, error) {
	kb := summaryKeyboard(rep.Snap.MatchID, rep.Snap.AccountID, windowMatch, extra...)
	if img := mapFor(rep.Snap, windowMatch); img != nil {
		name := fmt.Sprintf("map_%d.jpg", rep.Snap.MatchID)
		id, err := a.Bot.SendPhoto(chatID, text, name, img, kb)
		if err == nil {
			return id, KindPhoto, nil
		}
		a.Log("сводка картинкой %d: %v", rep.Snap.MatchID, err)
	}
	id, err := a.Bot.Send(chatID, text, telegram.Keyboard(extra))
	return id, "", err
}

// EditSummary правит уже отправленную сводку тем же способом, каким она была
// отправлена.
func (a *App) EditSummary(chatID, msgID int64, kind string, rep *Report, window string, extra ...[]telegram.Button) error {
	return a.editSummaryText(chatID, msgID, kind, caption(rep.Text()), rep, window, extra...)
}

// reportFromSnapshot собирает сводку из сохранённого снимка, без матча.
func (a *App) reportFromSnapshot(accountID, matchID int64) (*Report, bool) {
	snap, ok := a.snapshotOf(matchID, accountID)
	if !ok || len(snap.Lines) == 0 {
		return nil, false
	}
	return &Report{Snap: snap, Full: snap.Render(a.DB, false)}, true
}

// editSummaryText правит сообщение готовой подписью.
func (a *App) editSummaryText(chatID, msgID int64, kind, text string, rep *Report, window string, extra ...[]telegram.Button) error {
	kb := summaryKeyboard(rep.Snap.MatchID, rep.Snap.AccountID, window, extra...)
	if kind == KindPhoto {
		img := mapFor(rep.Snap, window)
		if img == nil {
			return fmt.Errorf("карта матча %d не нарисовалась", rep.Snap.MatchID)
		}
		name := fmt.Sprintf("map_%d_%s.jpg", rep.Snap.MatchID, window)
		return a.Bot.EditPhoto(chatID, msgID, text, name, img, kb)
	}
	return a.Bot.Edit(chatID, msgID, text, telegram.Keyboard(extra))
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
