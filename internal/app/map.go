package app

import (
	"strconv"

	"github.com/kolesnikav/n-dota-stats/internal/analysis"
	"github.com/kolesnikav/n-dota-stats/internal/telegram"
)

// Карта показывается вместо сводки, в том же месте переписки.
//
// Телеграм не даёт превратить текстовое сообщение в сообщение с картинкой:
// вид задаётся при отправке. Поэтому переключение сделано заменой — старое
// сообщение удаляется, новое встаёт на его место. Новых постов не копится, в
// чате всё время одно сообщение.
//
// Второй способ — держать сводку подписью к картинке — не подошёл: подпись
// ограничена 1024 символами, а сводка в них не помещается. Замер по 260
// сводкам: медиана 973, максимум 1096.

// mapCallback показывает карту вместо сводки.
func (a *App) mapCallback(chatID, msgID int64, parts []string, edit bool) {
	if len(parts) < 4 {
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
	account, ok := a.viewAccount(chatID, parts[3])
	if !ok {
		return
	}
	ctx := viewCtx{Kind: "s"}
	if len(parts) > 4 {
		ctx = decodeCtx(parts[4])
	}

	snap, ok := a.snapshotOf(matchID, account)
	if !ok {
		return
	}
	img := mapFor(snap, window)
	if img == nil {
		_, _ = a.Bot.Send(chatID,
			"Путь героя по этому матчу не сохранён — он появляется после разбора реплея.", nil)
		return
	}
	kb := telegram.Keyboard{
		mapSwitch(matchID, account, window, ctx),
		backRow(matchID, account, ctx),
	}
	name := "map_" + parts[1] + "_" + window + ".jpg"
	text := mapCaption(snap, window)

	if edit {
		// Сообщение уже картинка — её достаточно заменить на месте.
		if err := a.Bot.EditPhoto(chatID, msgID, text, name, img, kb); err != nil {
			a.Log("правка карты %d: %v", matchID, err)
		}
		return
	}
	newID, err := a.Bot.SendPhoto(chatID, text, name, img, kb)
	if err != nil {
		a.Log("отправка карты %d: %v", matchID, err)
		return
	}
	a.replaceMessage(chatID, msgID, newID, matchID, account, ctx)
}

// backCallback возвращает сводку на место карты.
func (a *App) backCallback(chatID, msgID int64, parts []string) {
	if len(parts) < 3 {
		return
	}
	matchID, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return
	}
	account, ok := a.viewAccount(chatID, parts[2])
	if !ok {
		return
	}
	ctx := viewCtx{Kind: "s"}
	if len(parts) > 3 {
		ctx = decodeCtx(parts[3])
	}
	text, extra, ok := a.summaryView(chatID, account, matchID, ctx)
	if !ok {
		return
	}
	rep, ok := a.reportFromSnapshot(account, matchID)
	if !ok {
		return
	}
	newID, err := a.SendSummary(chatID, text, rep, ctx, extra...)
	if err != nil {
		a.Log("возврат к сводке %d: %v", matchID, err)
		return
	}
	a.replaceMessage(chatID, msgID, newID, matchID, account, ctx)
}

// replaceMessage убирает прежнее сообщение и запоминает новое.
//
// Удаляем после отправки, а не до: если отправка не удастся, у человека хотя
// бы останется то, что было.
func (a *App) replaceMessage(chatID, oldID, newID, matchID, account int64, ctx viewCtx) {
	if err := a.Bot.Delete(chatID, oldID); err != nil {
		a.Log("удаление сообщения %d: %v", oldID, err)
	}
	// У обычной сводки номер сообщения хранится: по нему её потом обновляют,
	// когда разберётся реплей.
	if ctx.Kind == "s" {
		_ = a.DB.SetMessageID(matchID, account, newID)
	}
}

// viewAccount разбирает, чью сводку показывать, и проверяет право на это.
func (a *App) viewAccount(chatID int64, raw string) (int64, bool) {
	u, ok := a.DB.User(chatID)
	if !ok || u.AccountID == 0 {
		return 0, false
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id == 0 {
		return u.AccountID, true
	}
	// Чужую сводку и карту показываем только админу: по ним видно, где
	// человек ходил, и это не общее достояние.
	if id != u.AccountID && !a.isAdmin(chatID) {
		return 0, false
	}
	return id, true
}

// mapCaption — короткая подпись под картинкой. Сводка возвращается кнопкой,
// здесь достаточно напомнить, чья это карта и за какой отрезок.
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
	return esc(snap.Hero) + " · " + esc(snap.Role.String()) + " · " + outcomeWord(snap.Win) +
		"\n" + when + " · крестами отмечены смерти (" + strconv.Itoa(died) + ")"
}

func outcomeWord(win bool) string {
	if win {
		return "победа"
	}
	return "поражение"
}
