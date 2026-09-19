package app

import (
	"errors"
	"time"

	"github.com/kolesnikav/n-dota-stats/internal/store"
)

const (
	// tickInterval — как часто просыпается наблюдатель. Реальная частота
	// опроса каждого игрока считается отдельно, см. pollEvery. Будильник
	// должен звонить чаще, чем нужен самый частый опрос, иначе минута на
	// деле превращается в полторы.
	tickInterval = 20 * time.Second

	// fastPoll — как часто опрашиваем через Steam Web API.
	//
	// Минуты хватает и по деньгам, и по смыслу. Ключ даёт 100 000 запросов в
	// сутки, а один игрок стоит одного запроса в минуту: двое обходятся в
	// 2 900 запросов, меньше трёх процентов лимита, и запаса хватит примерно
	// на шестьдесят человек. Делить игроков на «играющих» и «отдыхающих»
	// больше незачем: именно это деление и задерживало сводку по первому
	// матчу вечера на десять минут.
	fastPoll = time.Minute

	// activeWindow — сколько времени после последнего матча игрок считается
	// играющим. Осталось для OpenDota: у неё лимит 60 запросов в минуту на
	// всех, и щадить её приходится.
	activeWindow = 4 * time.Hour

	slowActivePoll = 3 * time.Minute
	slowIdlePoll   = 20 * time.Minute

	gcDailyLimit = 80 // запас к сотне заявок, о которой пишет OpenDota
)

// SlowSource — источник, который просит опрашивать себя пореже.
//
// Проверять это по имени нельзя: имя — человеческая строка, и когда у
// источника появился запасной ход, оно перестало совпадать буквально. Опрос
// тогда молча замедлился вдвое, и сводка приходила на десять минут позже.
type SlowSource interface {
	SlowPolling() bool
}

// pollEvery — как часто опрашивать этого игрока.
func (a *App) pollEvery(lastMatch time.Time) time.Duration {
	if s, ok := a.Source.(SlowSource); ok && s.SlowPolling() {
		if time.Since(lastMatch) < activeWindow {
			return slowActivePoll
		}
		return slowIdlePoll
	}
	return fastPoll
}

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
		every := a.pollEvery(a.lastMatchTime(u.AccountID))
		if u.LastPoll > 0 && now.Sub(time.Unix(u.LastPoll, 0)) < every {
			continue
		}
		a.pollUser(u)
	}
}

// lastMatchTime — когда игрок в последний раз заканчивал матч.
func (a *App) lastMatchTime(accountID int64) time.Time {
	matches, err := a.DB.UserMatches(accountID, 1)
	if err != nil || len(matches) == 0 {
		return time.Time{}
	}
	return time.Unix(matches[0].StartTime, 0)
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
			// Матч уже скачан — ради товарища по команде или ещё до того, как
			// этот игрок зарегистрировался. Пропускать его целиком нельзя:
			// без связи матч не попадёт ни в историю, ни в средние, и человек
			// недосчитается игр, которые у нас есть.
			//
			// Сводку при этом не шлём: на первом опросе это два десятка
			// сообщений подряд про игры, о которых никто не спрашивал.
			if !a.DB.HasMatchUser(id, u.AccountID) {
				if _, err := a.Report(u.ChatID, u.AccountID, id); err != nil && !errors.Is(err, errNotInMatch) {
					a.Log("связь матча %d с %d: %v", id, u.AccountID, err)
				}
			}
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
		if err := a.SendReport(u.ChatID, u.AccountID, matchID); err != nil {
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
			if err := a.EditSummary(mu.ChatID, mu.MessageID, rep.Text(), rep,
				viewCtx{Kind: "s"}, roleRow(matchID)); err != nil {
				a.Log("обновление сводки %d: %v", matchID, err)
			}
		}
	}
}
