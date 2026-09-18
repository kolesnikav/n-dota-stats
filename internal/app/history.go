package app

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/kolesnikav/n-dota-stats/internal/analysis"
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
	text, matchID, index, total, ok := a.historyPage(u.AccountID, 0)
	if !ok {
		_, _ = a.Bot.Send(chatID, "Матчей пока нет. /backfill — загрузить историю.", nil)
		return
	}
	_, _ = a.Bot.Send(chatID, text, historyNav(index, total, ownHistoryData, mapButtons(matchID)))
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
	text, matchID, index, total, ok := a.historyPage(u.AccountID, index)
	if !ok {
		return
	}
	if err := a.Bot.Edit(chatID, msgID, text,
		historyNav(index, total, ownHistoryData, mapButtons(matchID))); err != nil {
		a.Log("правка истории у %d: %v", chatID, err)
	}
}

// historyPage готовит страницу: сводку матча под номером index и кнопки.
func (a *App) historyPage(accountID int64, index int) (text string, matchID int64, page, total int, ok bool) {
	matchID, total, err := a.DB.UserMatchAt(accountID, index)
	if err != nil || total == 0 || matchID == 0 {
		return "", 0, 0, 0, false
	}
	if index < 0 {
		index = 0
	}
	if index >= total {
		index = total - 1
	}
	head := fmt.Sprintf("<i>Матч %d из %d</i>\n\n", index+1, total)

	// Сводка берётся из снимка: значения показателей и рейтинг посчитаны, когда
	// матч разбирали, и с тех пор не меняются. Заново считаются только
	// сравнения — медиана роли и своё среднее, — потому что они растут.
	if blob, ok := a.DB.Snapshot(matchID, accountID); ok {
		var snap analysis.Snapshot
		if json.Unmarshal(blob, &snap) == nil && len(snap.Lines) > 0 {
			rep := &Report{Snap: snap, Full: snap.Render(a.DB, false)}
			return head + rep.Text(), matchID, index, total, true
		}
	}
	// Снимка нет — матч разобран до того, как их начали хранить. Считаем как
	// раньше, из матча.
	rep, err := a.View(accountID, matchID)
	if err != nil {
		return fmt.Sprintf("Матч %d не разобрать: %v", matchID, err), matchID, index, total, true
	}
	return head + rep.Text(), matchID, index, total, true
}

// ownHistoryData — адрес страницы своей истории.
func ownHistoryData(page int) string { return fmt.Sprintf("h:%d", page) }

// historyNav — стрелки и счётчик. Стрелка на краю списка не исчезает, а
// перестаёт быть ссылкой: прыгающие кнопки сбивают прицел.
//
// Адрес страницы задаётся снаружи: тот же виджет листает и свою историю, и
// чужую в админке, отличаются они только тем, куда ведут кнопки.
func historyNav(index, total int, data func(page int) string, extra ...[]telegram.Button) telegram.Keyboard {
	prev := telegram.Button{Text: "◀", Data: data(index - 1)}
	next := telegram.Button{Text: "▶", Data: data(index + 1)}
	if index <= 0 {
		prev = telegram.Button{Text: "·", Data: data(0)}
	}
	if index >= total-1 {
		next = telegram.Button{Text: "·", Data: data(total - 1)}
	}
	kb := telegram.Keyboard{{
		prev,
		{Text: fmt.Sprintf("%d/%d", index+1, total), Data: data(index)},
		next,
	}}
	return append(kb, extra...)
}
