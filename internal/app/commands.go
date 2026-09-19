package app

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/kolesnikav/n-dota-stats/internal/dota"
	"github.com/kolesnikav/n-dota-stats/internal/mvp"
	"github.com/kolesnikav/n-dota-stats/internal/odota"
	"github.com/kolesnikav/n-dota-stats/internal/store"
)

const helpText = `<b>Что я умею</b>

/last — разобрать последний матч
/match <i>id</i> — разобрать конкретный матч
/history — листать свои матчи
/watch on|off — слежение за новыми матчами
/backfill <i>дней</i> — загрузить историю матчей (по умолчанию год)
/me — мои настройки
/link — сменить привязанный аккаунт
/forget — удалить мои данные
/help — это сообщение

После матча я показываю показатели твоей роли и свой топ-3. Нажми кнопки и
укажи, кого Dota показала на самом деле — на этих исправлениях учится формула.`

// adminHelp — то, что дописывается к справке для админа. Команды про формулу
// вынесены сюда намеренно: веса общие на бота, и обычному пользователю нечего
// их пересчитывать.
const adminHelp = `

<b>Только для админа</b>

/users — карточки пользователей
/budget — остаток суточного лимита Game Coordinator
/stats — сколько накоплено и как часто формула угадывает
/fit — пересчитать веса по исправлениям
/weights — текущие веса`

var accountRe = regexp.MustCompile(`(\d{4,12})`)

// parseAccountID принимает число, ссылку на dotabuff или opendota.
func parseAccountID(s string) (int64, bool) {
	s = strings.TrimSpace(s)
	m := accountRe.FindAllString(s, -1)
	if len(m) == 0 {
		return 0, false
	}
	id, err := strconv.ParseInt(m[len(m)-1], 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}

func (a *App) onCommand(chatID int64, text, name string) {
	fields := strings.Fields(text)
	cmd := strings.ToLower(strings.SplitN(fields[0], "@", 2)[0])
	arg := ""
	if len(fields) > 1 {
		arg = fields[1]
	}

	if cmd != "/start" && a.blocked(chatID) {
		return
	}

	switch cmd {
	case "/start":
		a.cmdStart(chatID, name)
	case "/link":
		a.pending[chatID] = "account"
		_, _ = a.Bot.Send(chatID, "Пришли ID аккаунта Dota или ссылку на профиль.", nil)
	case "/help":
		_, _ = a.Bot.Send(chatID, a.helpFor(chatID), nil)
	case "/me":
		a.cmdMe(chatID)
	case "/last":
		a.cmdLast(chatID)
	case "/match":
		a.cmdMatch(chatID, arg)
	case "/history":
		a.cmdHistory(chatID)
	case "/stats":
		if a.adminOnly(chatID) {
			a.cmdStats(chatID)
		}
	case "/fit":
		if a.adminOnly(chatID) {
			a.cmdFit(chatID)
		}
	case "/weights":
		if a.adminOnly(chatID) {
			a.cmdWeights(chatID)
		}
	case "/watch":
		a.cmdWatch(chatID, arg)
	case "/backfill":
		a.cmdBackfill(chatID, arg)
	case "/forget":
		_ = a.DB.DeleteUser(chatID)
		_, _ = a.Bot.Send(chatID, "Удалил тебя и твою разметку. /start — начать заново.", nil)
	case "/users":
		a.cmdUsers(chatID)
	case "/budget":
		if a.isAdmin(chatID) {
			_, _ = a.Bot.Send(chatID, fmt.Sprintf("Заявок в Game Coordinator сегодня: <b>%d</b> из %d.",
				a.DB.GCBudgetUsed(), gcDailyLimit), nil)
		}
	default:
		_, _ = a.Bot.Send(chatID, "Не знаю такой команды. /help", nil)
	}
}

// helpFor — справка с учётом прав: админские команды видит только админ.
func (a *App) helpFor(chatID int64) string {
	if a.isAdmin(chatID) {
		return helpText + adminHelp
	}
	return helpText
}

// adminOnly отвечает отказом, если команда не для этого пользователя.
// Отвечаем так же, как на неизвестную команду: незачем сообщать, что такая
// команда существует.
func (a *App) adminOnly(chatID int64) bool {
	if a.isAdmin(chatID) {
		return true
	}
	_, _ = a.Bot.Send(chatID, "Не знаю такой команды. /help", nil)
	return false
}

func (a *App) cmdStart(chatID int64, name string) {
	if u, ok := a.DB.User(chatID); ok {
		switch u.Status {
		case store.StatusBlocked:
			_, _ = a.Bot.Send(chatID, "Доступ закрыт.", nil)
		case store.StatusPending:
			_, _ = a.Bot.Send(chatID, "Заявка уже отправлена, жду подтверждения админа.", nil)
		default:
			_, _ = a.Bot.Send(chatID, fmt.Sprintf("Уже слежу за аккаунтом <b>%d</b>.\n\n%s", u.AccountID, a.helpFor(chatID)), nil)
		}
		return
	}
	a.pending[chatID] = "account"
	_, _ = a.Bot.Send(chatID,
		"Привет. Пришли <b>ID аккаунта Dota</b> — число или ссылку на профиль "+
			"dotabuff либо opendota.\n\nИстория матчей должна быть открыта: "+
			"Dota → Настройки → Приватность → «Показывать публично данные о матчах».", nil)
}

func (a *App) finishRegistration(chatID int64, text, name string) {
	accountID, ok := parseAccountID(text)
	if !ok {
		_, _ = a.Bot.Send(chatID, "Не нашёл ID в сообщении. Пришли число или ссылку на профиль.", nil)
		return
	}
	ids, err := a.Source.RecentMatchIDs(accountID)
	if err != nil {
		a.Log("проверка аккаунта %d: %v", accountID, err)
		_, _ = a.Bot.Send(chatID, busyText(err), nil)
		return
	}
	if len(ids) == 0 {
		_, _ = a.Bot.Send(chatID, a.emptyHistoryText(accountID), nil)
		return
	}
	delete(a.pending, chatID)

	status := store.StatusPending
	first := a.DB.AdminCount() == 0
	if first {
		status = store.StatusAdmin
	}
	if err := a.DB.UpsertUser(store.User{
		ChatID: chatID, AccountID: accountID, Nickname: name, Status: status,
	}); err != nil {
		a.Log("сохранение пользователя: %v", err)
		_, _ = a.Bot.Send(chatID, "Не смог сохранить. Попробуй ещё раз.", nil)
		return
	}

	if first {
		_, _ = a.Bot.Send(chatID, fmt.Sprintf(
			"Аккаунт <b>%d</b> привязан. Ты первый — значит, ты <b>админ</b>: "+
				"новых пользователей подтверждаешь через /users.\n\n%s", accountID, a.helpFor(chatID)), nil)
	} else {
		_, _ = a.Bot.Send(chatID,
			"Аккаунт привязан. Жду подтверждения админа — после него начну разбирать твои матчи.", nil)
		a.notifyAdmins(chatID, accountID, name)
	}
	go func() {
		if err := a.SendReport(chatID, accountID, ids[0]); err != nil {
			a.Log("первая сводка: %v", err)
		}
	}()
}

// emptyHistoryText различает два совсем разных случая: аккаунта нет вовсе и
// аккаунт есть, но Dota не публикует его матчи. Раньше и то и другое сводилось
// к «проверь ID», хотя проверять надо разное.
func (a *App) emptyHistoryText(accountID int64) string {
	prof, err := a.OD.Profile(accountID)
	if err != nil || !prof.Known {
		return "Такого аккаунта не вижу. Проверь номер — он должен быть из ссылки " +
			"вида dotabuff.com/players/<b>109779233</b>."
	}
	medal := ""
	if m := dota.RankTierName(prof.RankTier); m != "" {
		medal = ", " + m
	}
	return fmt.Sprintf(
		"Аккаунт нашёлся — <b>%s</b>%s. Но матчей по нему не видно: в Dota выключено "+
			"«Показывать публично данные о матчах».\n\n"+
			"Включается так: Dota 2 → Настройки → Параметры → Приватность → "+
			"«Показывать публично данные о матчах».\n\n"+
			"Учти: публичными станут новые игры, уже сыгранные задним числом не появятся. "+
			"После первой же игры напиши /start снова.",
		esc(prof.Nickname), medal)
}

func (a *App) notifyAdmins(chatID, accountID int64, name string) {
	users, _ := a.DB.Users()
	idx := 0
	for i, u := range users {
		if u.ChatID == chatID {
			idx = i
		}
	}
	text, kb := usersCard(users, idx, a.DB)
	for _, admin := range a.DB.Admins() {
		_, _ = a.Bot.Send(admin, "<b>Новая заявка</b>\n\n"+text, kb)
	}
}

// busyText объясняет отказ так, чтобы человек понял, что делать. Отдельно
// разбираем ограничение частоты: это не его проблема и не повод править ID.
func busyText(err error) string {
	var limited *odota.TooManyRequests
	if errors.As(err, &limited) {
		return "Сервис статистики сейчас ограничивает частоту запросов. " +
			"Подожди минуту и повтори — с твоим аккаунтом всё в порядке."
	}
	return "Источник данных не отвечает. Попробуй ещё раз через минуту."
}

func (a *App) cmdMe(chatID int64) {
	u, ok := a.DB.User(chatID)
	if !ok {
		_, _ = a.Bot.Send(chatID, "Ты ещё не подключён. /start", nil)
		return
	}
	statusName := map[store.Status]string{
		store.StatusPending:  "ожидает подтверждения",
		store.StatusVerified: "подтверждён",
		store.StatusBlocked:  "заблокирован",
		store.StatusAdmin:    "админ",
	}[u.Status]
	watch := "включено"
	if !u.Watch {
		watch = "выключено"
	}
	_, _ = a.Bot.Send(chatID, fmt.Sprintf(
		"Аккаунт: <code>%d</code>\nСтатус: <b>%s</b>\nСлежение: %s\nМатчей в базе: %d",
		u.AccountID, statusName, watch, a.DB.MatchCount(u.AccountID)), nil)
}

func (a *App) cmdLast(chatID int64) {
	u, ok := a.DB.User(chatID)
	if !ok {
		_, _ = a.Bot.Send(chatID, "Сначала /start", nil)
		return
	}
	ids, err := a.Source.RecentMatchIDs(u.AccountID)
	if err != nil {
		a.Log("история матчей %d: %v", u.AccountID, err)
		_, _ = a.Bot.Send(chatID, busyText(err), nil)
		return
	}
	if len(ids) == 0 {
		_, _ = a.Bot.Send(chatID, "Не вижу матчей. История матчей открыта?", nil)
		return
	}
	if err := a.SendReport(chatID, u.AccountID, ids[0]); err != nil {
		_, _ = a.Bot.Send(chatID, "Не смог разобрать матч: "+esc(err.Error()), nil)
	}
}

func (a *App) cmdMatch(chatID int64, arg string) {
	u, ok := a.DB.User(chatID)
	if !ok {
		_, _ = a.Bot.Send(chatID, "Сначала /start", nil)
		return
	}
	id, err := strconv.ParseInt(arg, 10, 64)
	if err != nil {
		_, _ = a.Bot.Send(chatID, "Нужен номер матча: <code>/match 8999344582</code>", nil)
		return
	}
	if err := a.SendReport(chatID, u.AccountID, id); err != nil {
		_, _ = a.Bot.Send(chatID, "Не смог разобрать матч: "+esc(err.Error()), nil)
	}
}

func (a *App) cmdStats(chatID int64) {
	u, ok := a.DB.User(chatID)
	if !ok {
		_, _ = a.Bot.Send(chatID, "Сначала /start", nil)
		return
	}
	samples := a.Samples(u.AccountID)
	weights := mvp.EqualWeights()
	if w, ok := a.DB.Weights(u.AccountID); ok {
		weights = w
	}
	top1, top3, n := mvp.Accuracy(samples, weights)
	b1, b3, _ := mvp.Accuracy(samples, mvp.EqualWeights())

	lines := []string{
		"<b>Накоплено</b>",
		fmt.Sprintf("Матчей в базе: <b>%d</b>", a.DB.MatchCount(u.AccountID)),
		fmt.Sprintf("С твоей разметкой: <b>%d</b>", len(samples)),
	}
	if n > 0 {
		lines = append(lines, "",
			"<b>Как я угадываю</b>",
			fmt.Sprintf("Текущие веса: MVP точно %d из %d, в тройку %d из %d", top1, n, top3, n),
			fmt.Sprintf("Равные веса: MVP точно %d из %d, в тройку %d из %d", b1, n, b3, n))
	}
	if len(samples) < 15 {
		lines = append(lines, "", "<i>Для осмысленного обучения нужно хотя бы 15 размеченных матчей.</i>")
	}
	_, _ = a.Bot.Send(chatID, strings.Join(lines, "\n"), nil)
}

// samples собирает выборку для обучения.
//
// Ответ берётся из метаданных матча — тот самый список, который Dota
// показывает после игры. Раньше приходилось спрашивать человека, и ответов
// было семь; теперь их столько же, сколько разобранных матчей.
func (a *App) Samples(accountID int64) []mvp.Sample {
	ids, err := a.DB.AccountMatches(accountID)
	if err != nil {
		a.Log("выборка для обучения: %v", err)
		return nil
	}
	out := make([]mvp.Sample, 0, len(ids))
	for _, id := range ids {
		best := a.DB.MVP(id)
		if len(best) == 0 {
			continue
		}
		m, err := a.LoadMatch(id, 0)
		if err != nil || len(m.Players) < 2 {
			continue
		}
		s := mvp.Sample{MVPSlot: best[0]}
		for _, p := range m.Players {
			s.Vectors = append(s.Vectors, mvp.Vector(m, p))
			s.Slots = append(s.Slots, p.Slot)
		}
		out = append(out, s)
	}
	return out
}

func (a *App) cmdFit(chatID int64) {
	u, ok := a.DB.User(chatID)
	if !ok {
		return
	}
	samples := a.Samples(u.AccountID)
	if len(samples) < 5 {
		_, _ = a.Bot.Send(chatID, fmt.Sprintf(
			"Пока мало данных: размечено %d матчей, нужно хотя бы 5.", len(samples)), nil)
		return
	}
	weights, ll := mvp.Train(samples, mvp.FitL2, mvp.FitSteps, mvp.FitRate)
	if err := a.DB.SaveWeights(u.AccountID, weights); err != nil {
		a.Log("сохранение весов: %v", err)
	}
	top1, top3, n := mvp.Accuracy(samples, weights)
	lines := []string{fmt.Sprintf("<b>Веса пересчитаны</b> по %d матчам", len(samples)), ""}
	for _, w := range mvp.Describe(weights) {
		lines = append(lines, fmt.Sprintf("%s: <b>%.0f%%</b>", esc(w.Label), w.Share))
	}
	lines = append(lines, "",
		fmt.Sprintf("MVP угадан %d из %d, в тройку %d из %d", top1, n, top3, n),
		fmt.Sprintf("Средняя правдоподобность: %.3f", ll))
	_, _ = a.Bot.Send(chatID, strings.Join(lines, "\n"), nil)
}

func (a *App) cmdWeights(chatID int64) {
	u, ok := a.DB.User(chatID)
	if !ok {
		return
	}
	weights := mvp.EqualWeights()
	if w, ok := a.DB.Weights(u.AccountID); ok {
		weights = w
	}
	lines := []string{"<b>Текущие веса</b>"}
	for _, w := range mvp.Describe(weights) {
		lines = append(lines, fmt.Sprintf("%s: <b>%.0f%%</b> (%.2f)", esc(w.Label), w.Share, w.Raw))
	}
	_, _ = a.Bot.Send(chatID, strings.Join(lines, "\n"), nil)
}

func (a *App) cmdWatch(chatID int64, arg string) {
	u, ok := a.DB.User(chatID)
	if !ok {
		return
	}
	switch arg {
	case "on", "off":
		_ = a.DB.SetWatch(chatID, arg == "on")
		state := "включено"
		if arg == "off" {
			state = "выключено"
		}
		_, _ = a.Bot.Send(chatID, "Слежение <b>"+state+"</b>.", nil)
	default:
		state := "включено"
		if !u.Watch {
			state = "выключено"
		}
		_, _ = a.Bot.Send(chatID, "Слежение сейчас <b>"+state+"</b>. Меняется: /watch on или /watch off", nil)
	}
}

func (a *App) cmdBackfill(chatID int64, arg string) {
	u, ok := a.DB.User(chatID)
	if !ok {
		_, _ = a.Bot.Send(chatID, "Сначала /start", nil)
		return
	}
	days := 365
	if arg != "" {
		if n, err := strconv.Atoi(arg); err == nil && n > 0 && n <= 3650 {
			days = n
		}
	}
	statusID, _ := a.Bot.Send(chatID,
		fmt.Sprintf("Загружаю историю за %d дней. Это займёт несколько минут — сводки по каждому матчу слать не буду.", days), nil)

	go func() {
		res, err := a.Backfill(chatID, u.AccountID, days, func(done, total int) {
			if statusID > 0 {
				_ = a.Bot.Edit(chatID, statusID,
					fmt.Sprintf("Загружаю историю: <b>%d</b> из %d…", done, total), nil)
			}
		})
		if err != nil {
			a.Log("загрузка истории %d: %v", u.AccountID, err)
			_, _ = a.Bot.Send(chatID, "Не смог загрузить историю: "+esc(err.Error()), nil)
			return
		}
		if statusID > 0 {
			_ = a.Bot.Edit(chatID, statusID, res.Text(), nil)
		} else {
			_, _ = a.Bot.Send(chatID, res.Text(), nil)
		}
	}()
}

func (a *App) cmdUsers(chatID int64) {
	if !a.isAdmin(chatID) {
		_, _ = a.Bot.Send(chatID, "Команда только для админов.", nil)
		return
	}
	users, err := a.DB.Users()
	if err != nil {
		a.Log("список пользователей: %v", err)
		return
	}
	text, kb := usersCard(users, 0, a.DB)
	_, _ = a.Bot.Send(chatID, text, kb)
}
