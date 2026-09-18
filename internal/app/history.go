package app

import (
	"fmt"
	"strconv"

	"github.com/kolesnikav/n-dota-stats/internal/telegram"
)

// Листание своей истории.
//
// Страницы подменяют одно и то же сообщение, а не шлют новые: иначе
// пролистанная история превращается в десяток одинаковых по виду сообщений,
// среди которых не найти нужное.

// cmdHistory открывает историю с самого свежего матча.
func (a *App) cmdHistory(chatID int64) {
	u, ok := a.DB.User(chatID)
	if !ok || u.AccountID == 0 {
		_, _ = a.Bot.Send(chatID, "Сначала привяжи аккаунт: /start", nil)
		return
	}
	page, ok := a.historyPage(u.AccountID, 0)
	if !ok {
		_, _ = a.Bot.Send(chatID, "Матчей пока нет. /backfill — загрузить историю.", nil)
		return
	}
	_, _, err := a.sendSummaryText(chatID, page.Text, page.Report,
		navRow(page.Index, page.Total, ownHistoryData))
	if err != nil {
		a.Log("история у %d: %v", chatID, err)
	}
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
	page, ok := a.historyPage(u.AccountID, index)
	if !ok {
		return
	}
	if err := a.editSummaryText(chatID, msgID, KindPhoto, page.Text, page.Report, windowMatch,
		navRow(page.Index, page.Total, ownHistoryData)); err != nil {
		a.Log("правка истории у %d: %v", chatID, err)
	}
}

// HistoryPage — одна страница истории.
type HistoryPage struct {
	Report *Report
	Text   string
	Index  int
	Total  int
}

// historyPage готовит страницу: сводку матча под номером index.
func (a *App) historyPage(accountID int64, index int) (HistoryPage, bool) {
	matchID, total, err := a.DB.UserMatchAt(accountID, index)
	if err != nil || total == 0 || matchID == 0 {
		return HistoryPage{}, false
	}
	if index < 0 {
		index = 0
	}
	if index >= total {
		index = total - 1
	}
	rep, ok := a.reportFromSnapshot(accountID, matchID)
	if !ok {
		// Снимка нет — матч разобран до того, как их начали хранить.
		// Считаем из матча, как раньше.
		built, err := a.View(accountID, matchID)
		if err != nil {
			return HistoryPage{}, false
		}
		rep = built
	}
	head := fmt.Sprintf("<i>Матч %d из %d</i>\n", index+1, total)
	return HistoryPage{Report: rep, Text: caption(head + rep.Text()), Index: index, Total: total}, true
}

// ownHistoryData — адрес страницы своей истории.
func ownHistoryData(page int) string { return fmt.Sprintf("h:%d", page) }

// navRow — стрелки и счётчик. Стрелка на краю списка не исчезает, а перестаёт
// быть ссылкой: прыгающие кнопки сбивают прицел.
func navRow(index, total int, data func(page int) string) []telegram.Button {
	prev := telegram.Button{Text: "◀", Data: data(index - 1)}
	next := telegram.Button{Text: "▶", Data: data(index + 1)}
	if index <= 0 {
		prev = telegram.Button{Text: "·", Data: "noop"}
	}
	if index >= total-1 {
		next = telegram.Button{Text: "·", Data: "noop"}
	}
	return []telegram.Button{
		prev,
		{Text: fmt.Sprintf("%d/%d", index+1, total), Data: "noop"},
		next,
	}
}
