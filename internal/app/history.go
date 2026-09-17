package app

import (
	"fmt"
	"strconv"

	"github.com/kolesnikav/n-dota-stats/internal/telegram"
)

// Листание своей истории.
//
// Сводка каждой страницы считается заново и подменяет текст того же сообщения,
// а не шлёт новое: иначе пролистанная история превращается в десяток
// одинаковых по виду сообщений, среди которых не найти нужное.

// cmdHistory открывает историю с самого свежего матча.
func (a *App) cmdHistory(chatID int64) {
	u, ok := a.DB.User(chatID)
	if !ok || u.AccountID == 0 {
		_, _ = a.Bot.Send(chatID, "Сначала привяжи аккаунт: /start", nil)
		return
	}
	text, kb, ok := a.historyPage(chatID, u.AccountID, 0)
	if !ok {
		_, _ = a.Bot.Send(chatID, "Матчей пока нет. /backfill — загрузить историю.", nil)
		return
	}
	_, _ = a.Bot.Send(chatID, text, kb)
}

// historyCallback перелистывает историю, переписывая то же сообщение.
func (a *App) historyCallback(chatID, msgID int64, parts []string) {
	u, ok := a.DB.User(chatID)
	if !ok || u.AccountID == 0 {
		return
	}
	index, err := strconv.Atoi(parts[1])
	if err != nil {
		return
	}
	text, kb, ok := a.historyPage(chatID, u.AccountID, index)
	if !ok {
		return
	}
	if err := a.Bot.Edit(chatID, msgID, text, kb); err != nil {
		a.Log("правка истории у %d: %v", chatID, err)
	}
}

// historyPage готовит страницу: сводку матча под номером index и кнопки.
func (a *App) historyPage(chatID, accountID int64, index int) (string, telegram.Keyboard, bool) {
	matchID, total, err := a.DB.UserMatchAt(accountID, index)
	if err != nil || total == 0 || matchID == 0 {
		return "", nil, false
	}
	if index < 0 {
		index = 0
	}
	if index >= total {
		index = total - 1
	}
	rep, err := a.Report(chatID, accountID, matchID)
	if err != nil {
		return fmt.Sprintf("Матч %d не разобрать: %v", matchID, err), historyKeyboard(index, total), true
	}
	head := fmt.Sprintf("<i>Матч %d из %d</i>\n\n", index+1, total)
	return head + rep.Text(), historyKeyboard(index, total), true
}

// historyKeyboard — стрелки и счётчик. Стрелка на краю списка не исчезает, а
// перестаёт быть ссылкой: прыгающие кнопки сбивают прицел.
func historyKeyboard(index, total int) telegram.Keyboard {
	prev := telegram.Button{Text: "◀", Data: fmt.Sprintf("h:%d", index-1)}
	next := telegram.Button{Text: "▶", Data: fmt.Sprintf("h:%d", index+1)}
	if index <= 0 {
		prev = telegram.Button{Text: "·", Data: "h:0"}
	}
	if index >= total-1 {
		next = telegram.Button{Text: "·", Data: fmt.Sprintf("h:%d", total-1)}
	}
	return telegram.Keyboard{{
		prev,
		{Text: fmt.Sprintf("%d/%d", index+1, total), Data: fmt.Sprintf("h:%d", index)},
		next,
	}}
}
