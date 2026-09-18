package app

import (
	"strings"
	"testing"
)

// На краях списка стрелка не должна никуда вести.
//
// Раньше она вела на ту же страницу, и телеграм на повторную отрисовку того же
// содержимого отвечает ошибкой «сообщение не изменилось». Теперь такая кнопка
// помечена как ничего не делающая.
func TestNavRowEdges(t *testing.T) {
	first := navRow(0, 5, ownHistoryData)
	if first[0].Data != "noop" {
		t.Errorf("на первой странице «назад» ведёт на %q", first[0].Data)
	}
	if first[2].Data != "h:1" {
		t.Errorf("на первой странице «вперёд» ведёт на %q", first[2].Data)
	}

	last := navRow(4, 5, ownHistoryData)
	if last[2].Data != "noop" {
		t.Errorf("на последней странице «вперёд» ведёт на %q", last[2].Data)
	}
	if last[0].Data != "h:3" {
		t.Errorf("на последней странице «назад» ведёт на %q", last[0].Data)
	}

	if mid := navRow(2, 5, ownHistoryData); mid[1].Text != "3/5" {
		t.Errorf("счётчик показывает %q, ждали 3/5", mid[1].Text)
	}

	one := navRow(0, 1, ownHistoryData)
	if one[0].Data != "noop" || one[2].Data != "noop" {
		t.Errorf("на единственной странице стрелки ведут на %q и %q", one[0].Data, one[2].Data)
	}
}

// Команды про формулу видит только админ, и обычному пользователю о них не
// сообщается даже упоминанием в справке.
func TestHelpHidesAdminCommands(t *testing.T) {
	for _, cmd := range []string{"/stats", "/fit", "/weights", "/users", "/budget"} {
		if strings.Contains(helpText, cmd) {
			t.Errorf("обычная справка упоминает %s", cmd)
		}
		if !strings.Contains(adminHelp, cmd) {
			t.Errorf("админская справка не упоминает %s", cmd)
		}
	}
	for _, cmd := range []string{"/history", "/last", "/match", "/watch"} {
		if !strings.Contains(helpText, cmd) {
			t.Errorf("обычная справка не упоминает %s", cmd)
		}
	}
}

// Кнопки несут матч и игрока: админ смотрит и чужие игры, и без аккаунта
// карта строилась бы по его собственной сводке матча.
//
// Под сводкой кнопка присылает картинку (k:), под самой картинкой —
// переключает окно правкой на месте (kk:).
func TestMapSwitch(t *testing.T) {
	if btn := mapRow(9003682980, 109779233)[0]; btn.Data != "k:9003682980:all:109779233" {
		t.Errorf("кнопка под сводкой ведёт на %q", btn.Data)
	}
	row := mapSwitch(9003682980, 109779233, windowMatch)
	if len(row) != 2 {
		t.Fatalf("кнопок %d, ждали 2", len(row))
	}
	if row[0].Data != "noop" {
		t.Errorf("текущее окно должно быть неактивным, а ведёт на %q", row[0].Data)
	}
	if row[1].Data != "kk:9003682980:lane:109779233" {
		t.Errorf("вторая кнопка ведёт на %q", row[1].Data)
	}
	for _, b := range row {
		if len(b.Data) > 64 {
			t.Errorf("данные кнопки длиной %d", len(b.Data))
		}
	}
}
