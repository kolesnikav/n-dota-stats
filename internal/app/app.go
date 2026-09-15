// Package app связывает источники данных, хранилище и телеграм.
package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/kolesnikav/n-dota-stats/internal/analysis"
	"github.com/kolesnikav/n-dota-stats/internal/benchmarks"
	"github.com/kolesnikav/n-dota-stats/internal/dota"
	"github.com/kolesnikav/n-dota-stats/internal/gc"
	"github.com/kolesnikav/n-dota-stats/internal/meta"
	"github.com/kolesnikav/n-dota-stats/internal/odota"
	"github.com/kolesnikav/n-dota-stats/internal/store"
	"github.com/kolesnikav/n-dota-stats/internal/telegram"
)

// MatchSource — источник данных о матчах. Реализации: Steam Web API (основной)
// и OpenDota (запасной, он же используется в тестах).
type MatchSource interface {
	RecentMatchIDs(accountID int64) ([]int64, error)
	Match(matchID int64) (*dota.Match, []byte, error)
	Name() string
}

// App — состояние бота.
type App struct {
	DB     *store.DB
	Bot    *telegram.Bot
	Source MatchSource
	OD     *odota.Client
	Salt   gc.Provider
	Log    func(format string, args ...any)

	// незавершённые диалоги: чат -> что ждём от него
	pending map[int64]string
	// частичная разметка MVP: ключ "matchID:accountID" -> слоты
	marking map[string][]int
}

// New собирает приложение.
func New(db *store.DB, bot *telegram.Bot, src MatchSource, od *odota.Client) *App {
	return &App{
		DB:     db,
		Bot:    bot,
		Source: src,
		OD:     od,
		Salt:   gc.Disabled{},
		Log: func(f string, a ...any) {
			fmt.Fprintf(os.Stderr, "[%s] "+f+"\n", append([]any{time.Now().Format("15:04:05")}, a...)...)
		},
		pending: map[int64]string{},
		marking: map[string][]int{},
	}
}

// LoadMatch достаёт матч из базы или у источника, доводит до готового вида:
// перцентили из снимка, роли с учётом поправок пользователя.
func (a *App) LoadMatch(matchID int64, forAccount int64) (*dota.Match, error) {
	var m *dota.Match
	if raw, ok := a.DB.LoadScoreboard(matchID); ok {
		if decoded, err := odota.Decode(raw); err == nil {
			m = decoded
		}
	}
	if m == nil {
		fresh, raw, err := a.Source.Match(matchID)
		if err != nil {
			return nil, err
		}
		m = fresh
		if err := a.DB.SaveMatch(m, raw); err != nil {
			a.Log("сохранение матча %d: %v", matchID, err)
		}
	}
	if blob, ok := a.DB.LoadMeta(matchID); ok {
		var md meta.Metadata
		if json.Unmarshal(blob, &md) == nil {
			md.Apply(m)
		}
	}
	benchmarks.Apply(a.DB, m)
	analysis.DetectRoles(m, func(acc int64, hero, lane int) (dota.Role, bool) {
		return a.DB.RoleHint(acc, hero, lane)
	})
	return m, nil
}

// Report считает сводку и сохраняет связь матча с пользователем.
func (a *App) Report(chatID, accountID, matchID int64) (*Report, error) {
	m, err := a.LoadMatch(matchID, accountID)
	if err != nil {
		return nil, err
	}
	rep, err := a.Build(m, accountID)
	if err != nil {
		return nil, err
	}
	predicted := make([]int, 0, 3)
	for i, s := range rep.Ranked {
		if i >= 3 {
			break
		}
		predicted = append(predicted, s.Player.Slot)
	}
	err = a.DB.LinkMatchUser(store.MatchUser{
		MatchID:   matchID,
		AccountID: accountID,
		ChatID:    chatID,
		Role:      rep.Player.Role,
		Source:    rep.Player.RoleSource,
		Predicted: predicted,
	}, rep.Metric)
	if err != nil {
		a.Log("связь матча %d: %v", matchID, err)
	}
	return rep, nil
}

// SendReport отправляет сводку и, если разметки ещё нет, спрашивает про MVP.
func (a *App) SendReport(chatID, accountID, matchID int64, askMVP bool) error {
	rep, err := a.Report(chatID, accountID, matchID)
	if err != nil {
		return err
	}
	msgID, err := a.Bot.Send(chatID, rep.Text(false), rep.Keyboard(false))
	if err != nil {
		return err
	}
	_ = a.DB.SetMessageID(matchID, accountID, msgID)
	if askMVP && len(a.DB.Actual(matchID, accountID)) == 0 {
		_, _ = a.Bot.Send(chatID, stepQuestion[1], mvpKeyboard(matchID, rep.Ranked, 1, map[int]bool{}))
	}
	return nil
}

// Run — главный цикл: обновления телеграма плюс фоновые задачи.
func (a *App) Run() error {
	offset := int64(0)
	if s := a.DB.Get("tg_offset"); s != "" {
		_ = json.Unmarshal([]byte(s), &offset)
	}
	// Нумерация обновлений у каждого бота своя. Если подставить новый токен,
	// а позицию оставить от старого, новый бот не увидит ни одного сообщения:
	// его update_id заведомо меньше сохранённого. Поэтому при смене токена
	// сбрасываем позицию. Сам токен не храним — только его отпечаток.
	if fp := tokenFingerprint(a.Bot.Token); fp != "" {
		if prev := a.DB.Get("tg_token"); prev != "" && prev != fp {
			a.Log("токен бота сменился, сбрасываю позицию в очереди обновлений")
			offset = 0
		}
		_ = a.DB.Put("tg_token", fp)
	}

	nextWatch := time.Now()
	nextBench := time.Now()

	a.Log("бот запущен, источник матчей: %s", a.Source.Name())
	for {
		now := time.Now()
		if now.After(nextWatch) {
			nextWatch = now.Add(watchInterval)
			a.watchTick()
			a.metaTick()
		}
		if now.After(nextBench) {
			nextBench = now.Add(12 * time.Hour)
			go a.refreshBenchmarks()
		}

		updates, err := a.Bot.GetUpdates(offset, 25)
		if err != nil {
			a.Log("getUpdates: %v", err)
			time.Sleep(3 * time.Second)
			continue
		}
		for _, u := range updates {
			offset = u.UpdateID + 1
			_ = a.DB.Put("tg_offset", fmt.Sprintf("%d", offset))
			a.handle(u)
		}
	}
}

// tokenFingerprint — короткий отпечаток токена. Нужен, чтобы замечать смену
// бота, не сохраняя сам секрет в базу.
func tokenFingerprint(token string) string {
	if token == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:8])
}

func (a *App) refreshBenchmarks() {
	if !benchmarks.Stale(a.DB) {
		return
	}
	heroes, err := a.OD.Heroes()
	if err != nil {
		a.Log("справочник героев: %v", err)
		return
	}
	dota.SetHeroNames(heroes)
	ids := make([]int, 0, len(heroes))
	for id := range heroes {
		ids = append(ids, id)
	}
	a.Log("обновляю снимок перцентилей по %d героям", len(ids))
	benchmarks.Refresh(a.OD, a.DB, ids, 2*time.Second, a.Log)
	a.Log("снимок перцентилей обновлён: %d героев", a.DB.BenchmarksHeroCount())
}

func (a *App) handle(u telegram.Update) {
	defer func() {
		if r := recover(); r != nil {
			a.Log("паника в обработчике: %v", r)
		}
	}()
	switch {
	case u.CallbackQuery != nil:
		a.onCallback(u)
	case u.Message != nil:
		chatID := u.Message.Chat.ID
		text := strings.TrimSpace(u.Message.Text)
		name := u.Message.From.Username
		if name == "" {
			name = u.Message.From.FirstName
		}
		if strings.HasPrefix(text, "/") {
			a.onCommand(chatID, text, name)
			return
		}
		if what, ok := a.pending[chatID]; ok && what == "account" {
			a.finishRegistration(chatID, text, name)
			return
		}
		_, _ = a.Bot.Send(chatID, "Команды — /help", nil)
	}
}

// blocked отвечает отказом заблокированным пользователям.
func (a *App) blocked(chatID int64) bool {
	u, ok := a.DB.User(chatID)
	if ok && u.Status == store.StatusBlocked {
		_, _ = a.Bot.Send(chatID, "Доступ закрыт.", nil)
		return true
	}
	return false
}

func (a *App) isAdmin(chatID int64) bool {
	u, ok := a.DB.User(chatID)
	return ok && u.Status == store.StatusAdmin
}
