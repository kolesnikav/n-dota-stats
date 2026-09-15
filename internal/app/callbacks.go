package app

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/kolesnikav/n-dota-stats/internal/dota"
	"github.com/kolesnikav/n-dota-stats/internal/store"
	"github.com/kolesnikav/n-dota-stats/internal/telegram"
)

func (a *App) onCallback(u telegram.Update) {
	cq := u.CallbackQuery
	_ = a.Bot.Answer(cq.ID, "")
	if cq.Message == nil {
		return
	}
	chatID := cq.Message.Chat.ID
	msgID := cq.Message.MessageID
	parts := strings.Split(cq.Data, ":")
	if len(parts) < 2 {
		return
	}
	if a.blocked(chatID) {
		return
	}

	switch parts[0] {
	case "r": // роль
		a.roleCallback(chatID, msgID, parts)
	case "m": // разметка MVP
		a.markCallback(chatID, msgID, parts)
	case "u": // админка
		a.adminCallback(chatID, msgID, parts)
	}
}

func (a *App) userFor(chatID int64) (store.User, bool) { return a.DB.User(chatID) }

func (a *App) roleCallback(chatID, msgID int64, parts []string) {
	u, ok := a.userFor(chatID)
	if !ok {
		return
	}
	matchID, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return
	}
	if len(parts) == 2 {
		_ = a.Bot.Edit(chatID, msgID, "Какая у тебя была роль?", roleKeyboard(matchID))
		return
	}
	roleNum, err := strconv.Atoi(parts[2])
	if err != nil {
		return
	}
	role := dota.Role(roleNum)
	if !role.Valid() {
		return
	}
	m, err := a.LoadMatch(matchID, u.AccountID)
	if err != nil {
		return
	}
	p := m.Find(u.AccountID)
	if p == nil {
		return
	}
	if err := a.DB.SaveRoleHint(u.AccountID, p.HeroID, p.Lane, role); err != nil {
		a.Log("сохранение роли: %v", err)
	}
	_ = a.DB.SetRole(matchID, u.AccountID, role, dota.SourceManual)

	rep, err := a.Report(chatID, u.AccountID, matchID)
	if err != nil {
		return
	}
	_ = a.Bot.Edit(chatID, msgID, rep.Text(), rep.Keyboard())
}

func (a *App) markCallback(chatID, msgID int64, parts []string) {
	if len(parts) < 4 {
		return
	}
	u, ok := a.userFor(chatID)
	if !ok {
		return
	}
	matchID, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return
	}
	step, err := strconv.Atoi(parts[3])
	if err != nil {
		return
	}
	key := fmt.Sprintf("%d:%d", matchID, u.AccountID)
	chosen := a.marking[key]

	if parts[2] == "x" { // пропустить
		delete(a.marking, key)
		_ = a.DB.SetActual(matchID, u.AccountID, chosen)
		_ = a.Bot.Edit(chatID, msgID, a.markSummary(matchID, u.AccountID, chosen), nil)
		return
	}
	slot, err := strconv.Atoi(parts[2])
	if err != nil {
		return
	}
	chosen = append(chosen, slot)
	a.marking[key] = chosen

	if step < 3 {
		rep, err := a.Report(chatID, u.AccountID, matchID)
		if err == nil {
			taken := map[int]bool{}
			for _, s := range chosen {
				taken[s] = true
			}
			_ = a.Bot.Edit(chatID, msgID, stepQuestion[step+1],
				mvpKeyboard(matchID, rep.Ranked, step+1, taken))
			return
		}
	}
	delete(a.marking, key)
	_ = a.DB.SetActual(matchID, u.AccountID, chosen)
	_ = a.Bot.Edit(chatID, msgID, a.markSummary(matchID, u.AccountID, chosen), nil)
}

func (a *App) markSummary(matchID, accountID int64, chosen []int) string {
	m, err := a.LoadMatch(matchID, accountID)
	if err != nil {
		return "Записал."
	}
	names := map[int]string{}
	for _, p := range m.Players {
		names[p.Slot] = p.Name()
	}
	rep, err := a.Build(m, accountID)
	if err != nil {
		return "Записал."
	}
	var predicted []int
	for i, s := range rep.Ranked {
		if i >= 3 {
			break
		}
		predicted = append(predicted, s.Player.Slot)
	}

	line := func(slots []int) string {
		parts := make([]string, 0, len(slots))
		for _, s := range slots {
			parts = append(parts, esc(names[s]))
		}
		return strings.Join(parts, " → ")
	}
	lines := []string{"<b>Записал</b>"}
	if len(chosen) > 0 {
		lines = append(lines, "Dota: "+line(chosen))
	}
	lines = append(lines, "Я: "+line(predicted))
	switch {
	case len(chosen) == 0:
		lines = append(lines, "Пропущено.")
	case len(predicted) > 0 && predicted[0] == chosen[0]:
		lines = append(lines, "MVP угадан.")
	case contains(predicted, chosen[0]):
		lines = append(lines, "MVP не первый, но в тройке был.")
	default:
		lines = append(lines, "Мимо — для обучения это полезнее попадания.")
	}
	labelled := len(a.samples(accountID))
	lines = append(lines, "", fmt.Sprintf("Размечено матчей: <b>%d</b>. /fit пересчитает веса.", labelled))
	return strings.Join(lines, "\n")
}

func contains(xs []int, v int) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

func (a *App) adminCallback(chatID, msgID int64, parts []string) {
	if !a.isAdmin(chatID) {
		return
	}
	users, err := a.DB.Users()
	if err != nil {
		return
	}
	switch parts[1] {
	case "p": // листание
		idx, _ := strconv.Atoi(parts[2])
		text, kb := usersCard(users, idx, a.DB)
		_ = a.Bot.Edit(chatID, msgID, text, kb)

	case "g": // игры пользователя
		if len(parts) < 4 {
			return
		}
		target, _ := strconv.ParseInt(parts[2], 10, 64)
		idx, _ := strconv.Atoi(parts[3])
		a.showUserMatches(chatID, msgID, target, idx)

	case "a": // действие
		if len(parts) < 4 {
			return
		}
		target, _ := strconv.ParseInt(parts[2], 10, 64)
		if target == chatID && (parts[3] == "block" || parts[3] == "demote") {
			_, _ = a.Bot.Send(chatID, "Себя трогать нельзя.", nil)
			return
		}
		var status store.Status
		switch parts[3] {
		case "ok":
			status = store.StatusVerified
		case "no", "block":
			status = store.StatusBlocked
		case "admin":
			status = store.StatusAdmin
		case "demote":
			if a.DB.AdminCount() <= 1 {
				_, _ = a.Bot.Send(chatID, "Это последний админ, снять права нельзя.", nil)
				return
			}
			status = store.StatusVerified
		default:
			return
		}
		if err := a.DB.SetStatus(target, status, chatID); err != nil {
			a.Log("смена статуса: %v", err)
			return
		}
		a.notifyStatus(target, status)

		users, _ = a.DB.Users()
		idx := 0
		for i, u := range users {
			if u.ChatID == target {
				idx = i
			}
		}
		text, kb := usersCard(users, idx, a.DB)
		_ = a.Bot.Edit(chatID, msgID, text, kb)
	}
}

func (a *App) notifyStatus(chatID int64, s store.Status) {
	switch s {
	case store.StatusVerified:
		_, _ = a.Bot.Send(chatID, "Тебя подтвердили — теперь я разбираю твои матчи полностью.", nil)
	case store.StatusAdmin:
		_, _ = a.Bot.Send(chatID, "Тебе выданы права админа: /users.", nil)
	case store.StatusBlocked:
		_, _ = a.Bot.Send(chatID, "Доступ закрыт.", nil)
	}
}

func (a *App) showUserMatches(chatID, msgID, target int64, idx int) {
	u, ok := a.DB.User(target)
	if !ok {
		return
	}
	matches, err := a.DB.UserMatches(u.AccountID, 10)
	if err != nil {
		return
	}
	lines := []string{fmt.Sprintf("<b>Матчи %s</b>", esc(u.Nickname))}
	if len(matches) == 0 {
		lines = append(lines, "", "Пока ничего не разобрано.")
	}
	for _, m := range matches {
		outcome := "поражение"
		if m.Win {
			outcome = "победа"
		}
		role := ""
		if m.Role.Valid() {
			role = " · " + m.Role.String()
		}
		lines = append(lines, fmt.Sprintf("<code>%d</code> · %s%s · %s",
			m.MatchID, esc(m.HeroName), role, outcome))
	}
	kb := telegram.Keyboard{{{Text: "назад", Data: fmt.Sprintf("u:p:%d", idx)}}}
	_ = a.Bot.Edit(chatID, msgID, strings.Join(lines, "\n"), kb)
}
