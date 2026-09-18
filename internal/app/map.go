package app

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/kolesnikav/n-dota-stats/internal/analysis"
	"github.com/kolesnikav/n-dota-stats/internal/dota"
	"github.com/kolesnikav/n-dota-stats/internal/heatmap"
	"github.com/kolesnikav/n-dota-stats/internal/telegram"
)

// Тепловая карта отправляется картинкой по кнопке, а не вместе со сводкой.
//
// Причин две. Подпись к картинке в телеграме ограничена тысячей символов, а
// сводка длиннее, так что уместить одно в другое нельзя. И присылать по две
// картинки на каждый матч, когда смотрят далеко не каждый, — лишний шум.

// mapButtons — кнопки карт под сводкой.
//
// Номер аккаунта в кнопке нужен потому, что админ смотрит и чужие игры: без
// него карта строилась бы по его собственной сводке этого матча, а её может и
// не быть вовсе.
func mapButtons(matchID, accountID int64) []telegram.Button {
	id := strconv.FormatInt(matchID, 10) + ":%s:" + strconv.FormatInt(accountID, 10)
	return []telegram.Button{
		{Text: "карта линии", Data: "k:" + fmt.Sprintf(id, "lane")},
		{Text: "карта матча", Data: "k:" + fmt.Sprintf(id, "all")},
	}
}

// mapCallback рисует тепловую карту и присылает её картинкой.
func (a *App) mapCallback(chatID int64, parts []string) {
	if len(parts) < 3 {
		return
	}
	matchID, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return
	}
	u, ok := a.DB.User(chatID)
	if !ok || u.AccountID == 0 {
		return
	}
	account := u.AccountID
	if len(parts) > 3 {
		if id, err := strconv.ParseInt(parts[3], 10, 64); err == nil && id != 0 {
			// Чужую карту показываем только админу: в сводке чужого матча
			// видно, где человек ходил, и это не общее достояние.
			if id != u.AccountID && !a.isAdmin(chatID) {
				return
			}
			account = id
		}
	}
	blob, ok := a.DB.Snapshot(matchID, account)
	if !ok {
		_, _ = a.Bot.Send(chatID, "По этому матчу нет сохранённой сводки.", nil)
		return
	}
	var snap analysis.Snapshot
	if err := json.Unmarshal(blob, &snap); err != nil {
		a.Log("снимок матча %d: %v", matchID, err)
		return
	}
	path := snap.Path.Points()
	if len(path) == 0 {
		_, _ = a.Bot.Send(chatID,
			"Путь героя по этому матчу не сохранён — он появляется только после разбора реплея.", nil)
		return
	}

	to, window := 0, "весь матч"
	if parts[2] == "lane" {
		to, window = 600, "до 10:00"
	}
	deaths := make([]dota.Point, 0, len(snap.DeathsAt))
	for _, d := range snap.DeathsAt {
		deaths = append(deaths, dota.Point{T: d.T, X: float64(d.X) / 4, Y: float64(d.Y) / 4})
	}
	png, err := heatmap.Render(heatmap.Options{Path: path, Deaths: deaths, To: to})
	if err != nil {
		a.Log("карта матча %d: %v", matchID, err)
		return
	}

	var died int
	for _, d := range snap.DeathsAt {
		if to == 0 || d.T <= to {
			died++
		}
	}
	caption := fmt.Sprintf("<b>%s</b> · %s · %s\nГде ты был, %s. Крестами — смерти (%s).",
		esc(snap.Hero), esc(snap.Role.String()), outcomeWord(snap.Win), window,
		plural(died, "смерть", "смерти", "смертей"))
	name := fmt.Sprintf("map_%d_%s.jpg", matchID, parts[2])
	if _, err := a.Bot.SendPhoto(chatID, caption, name, png, nil); err != nil {
		a.Log("отправка карты %d: %v", matchID, err)
	}
}

func outcomeWord(win bool) string {
	if win {
		return "победа"
	}
	return "поражение"
}

// plural склоняет число по-русски.
func plural(n int, one, few, many string) string {
	mod100, mod10 := n%100, n%10
	switch {
	case mod100 >= 11 && mod100 <= 14:
		return fmt.Sprintf("%d %s", n, many)
	case mod10 == 1:
		return fmt.Sprintf("%d %s", n, one)
	case mod10 >= 2 && mod10 <= 4:
		return fmt.Sprintf("%d %s", n, few)
	default:
		return fmt.Sprintf("%d %s", n, many)
	}
}
