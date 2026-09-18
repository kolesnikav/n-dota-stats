package app

import (
	"fmt"
	"strconv"

	"github.com/kolesnikav/n-dota-stats/internal/analysis"
	"github.com/kolesnikav/n-dota-stats/internal/telegram"
)

// Тепловая карта приходит отдельной картинкой по кнопке.
//
// Кнопок две: под сводкой — «карта», она присылает картинку; под самой
// картинкой — переключатель окна, который правит её на месте. Разделение
// нужно потому, что телеграм не даёт превратить текстовое сообщение в
// сообщение с картинкой: править на месте можно только то, что уже картинка.

// mapRow — кнопка карты под текстовой сводкой.
func mapRow(matchID, accountID int64) []telegram.Button {
	return []telegram.Button{{
		Text: "тепловая карта",
		Data: fmt.Sprintf("k:%d:%s:%d", matchID, windowMatch, accountID),
	}}
}

// mapSwitch — переключатель окна под самой картинкой. Текущее окно помечено и
// не нажимается: телеграм отвечает ошибкой на правку сообщения тем же самым.
func mapSwitch(matchID, accountID int64, current string) []telegram.Button {
	label := func(window, text string) telegram.Button {
		if window == current {
			return telegram.Button{Text: "· " + text + " ·", Data: "noop"}
		}
		return telegram.Button{
			Text: text,
			Data: fmt.Sprintf("kk:%d:%s:%d", matchID, window, accountID),
		}
	}
	return []telegram.Button{
		label(windowMatch, "вся игра"),
		label(windowLane, "до 10:00"),
	}
}

// mapCallback присылает карту картинкой или правит её на месте.
//
// edit различает два случая: нажали кнопку под сводкой — присылаем новую
// картинку; нажали переключатель под самой картинкой — правим её.
func (a *App) mapCallback(chatID, msgID int64, parts []string, edit bool) {
	if len(parts) < 3 {
		return
	}
	matchID, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return
	}
	window := parts[2]
	if window != windowLane && window != windowMatch {
		return
	}
	u, ok := a.DB.User(chatID)
	if !ok || u.AccountID == 0 {
		return
	}
	account := u.AccountID
	if len(parts) > 3 {
		if id, err := strconv.ParseInt(parts[3], 10, 64); err == nil && id != 0 {
			// Чужую карту показываем только админу: по ней видно, где человек
			// ходил, и это не общее достояние.
			if id != u.AccountID && !a.isAdmin(chatID) {
				return
			}
			account = id
		}
	}
	snap, ok := a.snapshotOf(matchID, account)
	if !ok {
		_, _ = a.Bot.Send(chatID, "По этому матчу нет сохранённой сводки.", nil)
		return
	}
	img := mapFor(snap, window)
	if img == nil {
		_, _ = a.Bot.Send(chatID,
			"Путь героя по этому матчу не сохранён — он появляется после разбора реплея.", nil)
		return
	}

	kb := telegram.Keyboard{mapSwitch(matchID, account, window)}
	name := fmt.Sprintf("map_%d_%s.jpg", matchID, window)
	text := mapCaption(snap, window)
	if edit {
		if err := a.Bot.EditPhoto(chatID, msgID, text, name, img, kb); err != nil {
			a.Log("правка карты %d: %v", matchID, err)
		}
		return
	}
	if _, err := a.Bot.SendPhoto(chatID, text, name, img, kb); err != nil {
		a.Log("отправка карты %d: %v", matchID, err)
	}
}

// mapCaption — короткая подпись под картинкой. Сводка остаётся в своём
// сообщении, здесь достаточно напомнить, чья это карта и за какой отрезок.
func mapCaption(snap analysis.Snapshot, window string) string {
	when := "вся игра"
	if window == windowLane {
		when = "до 10:00"
	}
	died := 0
	for _, d := range snap.DeathsAt {
		if window != windowLane || d.T <= laneWindow {
			died++
		}
	}
	return fmt.Sprintf("<b>%s</b> · %s · %s\n%s · крестами отмечены смерти (%d)",
		esc(snap.Hero), esc(snap.Role.String()), outcomeWord(snap.Win), when, died)
}

func outcomeWord(win bool) string {
	if win {
		return "победа"
	}
	return "поражение"
}
