package app

import (
	"fmt"
	"time"

	"github.com/kolesnikav/n-dota-stats/internal/dota"
)

// HistorySource умеет отдавать историю матчей глубже последних игр.
// Реализуется источником OpenDota; Steam Web API такого среза не даёт.
type HistorySource interface {
	MatchIDsSince(accountID int64, days int) ([]int64, error)
}

// BackfillResult — что получилось загрузить.
type BackfillResult struct {
	Total   int // матчей в истории за период
	Loaded  int // загружено сейчас
	Skipped int // уже были в базе
	Failed  int // не удалось получить
	ByRole  map[dota.Role]int
	Elapsed time.Duration
}

// Backfill подтягивает историю матчей и считает по ним показатели.
//
// Сводки при этом не рассылаются: на сотне матчей это была бы сотня сообщений.
// Разбор реплеев тоже не запрашивается — они живут около двух недель, для
// старых игр запрашивать нечего, а лимит Game Coordinator общий.
func (a *App) Backfill(chatID, accountID int64, days int, progress func(done, total int)) (*BackfillResult, error) {
	hist, ok := a.Source.(HistorySource)
	if !ok {
		return nil, fmt.Errorf("источник %s не отдаёт историю глубже последних матчей", a.Source.Name())
	}
	ids, err := hist.MatchIDsSince(accountID, days)
	if err != nil {
		return nil, err
	}
	started := time.Now()
	res := &BackfillResult{Total: len(ids), ByRole: map[dota.Role]int{}}

	for i, id := range ids {
		if a.DB.HasMatchUser(id, accountID) {
			res.Skipped++
		} else {
			rep, err := a.Report(chatID, accountID, id)
			if err != nil {
				a.Log("история: матч %d: %v", id, err)
				res.Failed++
			} else {
				res.Loaded++
				res.ByRole[rep.Player.Role]++
			}
		}
		if progress != nil && (i+1)%15 == 0 {
			progress(i+1, len(ids))
		}
	}
	res.Elapsed = time.Since(started)
	return res, nil
}

// Text описывает итог загрузки.
func (r *BackfillResult) Text() string {
	lines := []string{
		"<b>История загружена</b>",
		fmt.Sprintf("Матчей за период: <b>%d</b>", r.Total),
		fmt.Sprintf("Разобрано сейчас: <b>%d</b>, уже было: %d", r.Loaded, r.Skipped),
	}
	if r.Failed > 0 {
		lines = append(lines, fmt.Sprintf("Не удалось получить: %d", r.Failed))
	}
	if len(r.ByRole) > 0 {
		lines = append(lines, "", "<b>По ролям</b>")
		for role := dota.RoleCarry; role <= dota.RoleHard; role++ {
			if n := r.ByRole[role]; n > 0 {
				lines = append(lines, fmt.Sprintf("%s — %d", role, n))
			}
		}
	}
	lines = append(lines, "",
		"Теперь в сводках появится сравнение с твоими прошлыми играми — оно включается от пяти матчей на роли.")
	return joinLines(lines)
}

func joinLines(lines []string) string {
	out := ""
	for i, l := range lines {
		if i > 0 {
			out += "\n"
		}
		out += l
	}
	return out
}

// Reparse просит разобрать реплеи ещё не разобранных матчей, а затем
// перечитывает их — в несколько заходов, потому что очередь на той стороне
// разбирает не мгновенно.
//
// Смысл: поведенческие показатели — варды, стаки, линия к 10:00, точность
// умений — живут только в разобранном матче. Пока их нет, половина сводки
// показывает числа без всякой базы.
//
// Про срок жизни реплеев: опытным путём разобрались матчи и 54-дневной
// давности, так что ограничивать окно двумя неделями не нужно.
func (a *App) Reparse(chatID, accountID int64, days int, wait time.Duration, log func(string, ...any)) (requested, updated int, err error) {
	ids, err := a.DB.UnparsedMatches(days)
	if err != nil {
		return 0, 0, err
	}
	if len(ids) == 0 {
		return 0, 0, nil
	}
	for _, id := range ids {
		a.OD.RequestParse(id)
		requested++
	}
	log("заявок на разбор отправлено: %d", requested)

	pending := ids
	for round := 1; round <= 4 && len(pending) > 0; round++ {
		log("жду %s, затем проверяю %d матчей (заход %d)", wait, len(pending), round)
		time.Sleep(wait)
		var still []int64
		for _, id := range pending {
			m, raw, err := a.Source.Match(id)
			if err != nil {
				still = append(still, id)
				continue
			}
			if m.Detail == dota.DetailScoreboard {
				still = append(still, id)
				continue
			}
			if err := a.DB.SaveMatch(m, raw); err != nil {
				continue
			}
			if _, err := a.Report(chatID, accountID, id); err == nil {
				updated++
			}
		}
		log("разобрано за заход: %d, осталось: %d", len(pending)-len(still), len(still))
		pending = still
	}
	return requested, updated, nil
}
