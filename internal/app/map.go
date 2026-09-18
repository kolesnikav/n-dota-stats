package app

import (
	"fmt"
	"strconv"

	"github.com/kolesnikav/n-dota-stats/internal/telegram"
)

// Переключатель тепловой карты.
//
// Карта показывается прямо в сводке, поэтому кнопки не «показать карту», а
// «какую карту». Активное окно помечено, и нажать на него нельзя — иначе
// сообщение правилось бы само в себя, а телеграм на это отвечает ошибкой.

// mapSwitch — ряд кнопок выбора окна карты.
func mapSwitch(matchID, accountID int64, current string) []telegram.Button {
	label := func(window, text string) telegram.Button {
		if window == current {
			return telegram.Button{Text: "· " + text + " ·", Data: "noop"}
		}
		return telegram.Button{Text: text, Data: fmt.Sprintf("k:%d:%s:%d", matchID, window, accountID)}
	}
	return []telegram.Button{
		label(windowMatch, "вся игра"),
		label(windowLane, "до 10:00"),
	}
}

// mapCallback перерисовывает карту в том же сообщении.
func (a *App) mapCallback(chatID, msgID int64, parts []string, kb telegram.Keyboard) {
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
	rep, ok := a.reportFromSnapshot(account, matchID)
	if !ok {
		return
	}
	// Остальные ряды кнопок оставляем как были: под сводкой это смена роли,
	// в истории — стрелки, в админском виджете ещё и «назад». Телеграм
	// присылает клавиатуру вместе с нажатием, поэтому её не нужно ни
	// запоминать, ни передавать в данных кнопки.
	extra := make([]([]telegram.Button), 0, len(kb))
	if len(kb) > 1 {
		extra = append(extra, kb[1:]...)
	}
	if err := a.EditSummary(chatID, msgID, KindPhoto, rep, window, extra...); err != nil {
		a.Log("правка карты %d: %v", matchID, err)
	}
}
