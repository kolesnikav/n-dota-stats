package app

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/kolesnikav/n-dota-stats/internal/telegram"
)

// Откуда открыта сводка.
//
// Кнопки карты подменяют сообщение целиком, а вернуться нужно ровно туда, где
// человек был: в свою историю на ту же страницу, в админский список на того же
// пользователя или к обычной сводке матча. Этот контекст едет в данных кнопки —
// запоминать его негде, а телеграм присылает обратно только их.
type viewCtx struct {
	Kind   string // "s" сводка, "h" своя история, "u" админский список
	Page   int    // страница истории
	Target int64  // чью историю смотрит админ
	Card   int    // номер карточки в /users
}

func (c viewCtx) encode() string {
	switch c.Kind {
	case "h":
		return "h" + strconv.Itoa(c.Page)
	case "u":
		return fmt.Sprintf("u%d_%d_%d", c.Target, c.Card, c.Page)
	default:
		return "s"
	}
}

func decodeCtx(s string) viewCtx {
	if s == "" || s[0] == 's' {
		return viewCtx{Kind: "s"}
	}
	// Непонятное считаем обычной сводкой, а не «страницей ноль»: кнопка
	// пришла из будущей или прошлой версии бота, и лучше показать матч, чем
	// увести в чужую историю.
	if s[0] == 'h' {
		page, err := strconv.Atoi(s[1:])
		if err != nil {
			return viewCtx{Kind: "s"}
		}
		return viewCtx{Kind: "h", Page: page}
	}
	if s[0] == 'u' {
		parts := strings.Split(s[1:], "_")
		if len(parts) != 3 {
			return viewCtx{Kind: "s"}
		}
		target, err1 := strconv.ParseInt(parts[0], 10, 64)
		card, err2 := strconv.Atoi(parts[1])
		page, err3 := strconv.Atoi(parts[2])
		if err1 != nil || err2 != nil || err3 != nil {
			return viewCtx{Kind: "s"}
		}
		return viewCtx{Kind: "u", Target: target, Card: card, Page: page}
	}
	return viewCtx{Kind: "s"}
}

// mapRow — кнопка карты под сводкой.
func mapRow(matchID, accountID int64, ctx viewCtx) []telegram.Button {
	return []telegram.Button{{
		Text: "тепловая карта",
		Data: fmt.Sprintf("k:%d:%s:%d:%s", matchID, windowMatch, accountID, ctx.encode()),
	}}
}

// mapSwitch — переключатель окна под картинкой. Текущее окно помечено и не
// нажимается: телеграм отвечает ошибкой на правку сообщения тем же самым.
func mapSwitch(matchID, accountID int64, current string, ctx viewCtx) []telegram.Button {
	label := func(window, text string) telegram.Button {
		if window == current {
			return telegram.Button{Text: "· " + text + " ·", Data: "noop"}
		}
		return telegram.Button{
			Text: text,
			Data: fmt.Sprintf("kk:%d:%s:%d:%s", matchID, window, accountID, ctx.encode()),
		}
	}
	return []telegram.Button{
		label(windowMatch, "вся игра"),
		label(windowLane, "до 10:00"),
	}
}

// backRow — возврат от карты к сводке.
func backRow(matchID, accountID int64, ctx viewCtx) []telegram.Button {
	return []telegram.Button{{
		Text: "← к сводке",
		Data: fmt.Sprintf("b:%d:%d:%s", matchID, accountID, ctx.encode()),
	}}
}
