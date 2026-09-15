package app

import (
	"time"

	"github.com/kolesnikav/n-dota-stats/internal/store"
)

const (
	watchInterval = 15 * time.Minute
	idleAfter     = 3 * time.Hour // если давно не играл — опрашиваем реже
	idleInterval  = 60 * time.Minute
	gcDailyLimit  = 80 // запас к сотне заявок, о которой пишет OpenDota
)

// watchTick опрашивает пользователей и рассылает сводки по новым матчам.
//
// Матч скачивается один раз на всех: список участников-пользователей строится
// до отправки, а состояние разбора хранится в самом матче, не в связке с
// пользователем.
func (a *App) watchTick() {
	users, err := a.DB.Users()
	if err != nil {
		a.Log("опрос: %v", err)
		return
	}
	now := time.Now()
	for _, u := range users {
		if !u.Watch || u.Status == store.StatusBlocked || u.Status == store.StatusPending {
			continue
		}
		// Тех, кто давно не играл, опрашиваем реже: обычный интервал 15 минут,
		// после трёх часов затишья — раз в час.
		if u.LastPoll > 0 {
			idle := now.Sub(time.Unix(u.LastPoll, 0))
			if idle > idleAfter && idle < idleInterval {
				continue
			}
		}
		a.pollUser(u)
	}
}

func (a *App) pollUser(u store.User) {
	ids, err := a.Source.RecentMatchIDs(u.AccountID)
	if err != nil {
		a.Log("история матчей %d: %v", u.AccountID, err)
		return
	}
	a.DB.TouchPoll(u.ChatID)

	for _, id := range ids {
		if _, known := a.DB.KnownMatch(id); known {
			continue
		}
		m, raw, err := a.Source.Match(id)
		if err != nil {
			a.Log("матч %d: %v", id, err)
			continue
		}
		if err := a.DB.SaveMatch(m, raw); err != nil {
			a.Log("сохранение матча %d: %v", id, err)
			continue
		}
		a.announce(id)
		a.maybeQueueReplay(id)
	}
}

// announce рассылает сводку всем пользователям бота, игравшим в этом матче.
func (a *App) announce(matchID int64) {
	m, err := a.LoadMatch(matchID, 0)
	if err != nil {
		return
	}
	users, err := a.DB.Users()
	if err != nil {
		return
	}
	byAccount := map[int64]store.User{}
	for _, u := range users {
		byAccount[u.AccountID] = u
	}
	for _, p := range m.Players {
		u, ok := byAccount[p.AccountID]
		if !ok || u.Status == store.StatusBlocked || !u.Watch {
			continue
		}
		if err := a.SendReport(u.ChatID, u.AccountID, matchID, true); err != nil {
			a.Log("сводка %d для %d: %v", matchID, u.ChatID, err)
		}
	}
}

// maybeQueueReplay ставит матч в очередь разбора, только если среди участников
// есть подтверждённый пользователь. Иначе помечаем матч пропущенным: сводка по
// скорборду у человека уже есть, а лимит Game Coordinator мы не тратим.
func (a *App) maybeQueueReplay(matchID int64) {
	m, err := a.LoadMatch(matchID, 0)
	if err != nil {
		return
	}
	users, err := a.DB.Users()
	if err != nil {
		return
	}
	verified := map[int64]bool{}
	for _, u := range users {
		if u.Status.Verified() {
			verified[u.AccountID] = true
		}
	}
	has := false
	for _, p := range m.Players {
		if verified[p.AccountID] {
			has = true
			break
		}
	}
	if !has {
		_ = a.DB.SetReplayState(matchID, store.ReplaySkipped, "")
		return
	}
	ok, err := a.DB.ClaimForReplay(matchID)
	if err != nil {
		a.Log("очередь разбора %d: %v", matchID, err)
		return
	}
	if ok {
		a.Log("матч %d поставлен в очередь разбора", matchID)
	}
}

// Refresh пересылает обновлённые сводки всем участникам матча. Вызывается,
// когда появились новые данные — например, разобрались метаданные.
func (a *App) Refresh(matchID int64) {
	parts, err := a.DB.Participants(matchID)
	if err != nil {
		return
	}
	for _, mu := range parts {
		rep, err := a.Report(mu.ChatID, mu.AccountID, matchID)
		if err != nil {
			continue
		}
		if mu.MessageID > 0 {
			_ = a.Bot.Edit(mu.ChatID, mu.MessageID, rep.Text(), rep.Keyboard())
		}
	}
}
