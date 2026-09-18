package app

import (
	"fmt"
	"strings"
	"testing"

	"github.com/kolesnikav/n-dota-stats/internal/telegram"
)

// На краях списка стрелка не должна уводить за границы.
func TestHistoryKeyboardEdges(t *testing.T) {
	first := historyNav(0, 5, ownHistoryData)[0]
	if first[0].Data != "h:0" {
		t.Errorf("на первой странице «назад» ведёт на %q", first[0].Data)
	}
	if first[2].Data != "h:1" {
		t.Errorf("на первой странице «вперёд» ведёт на %q", first[2].Data)
	}

	last := historyNav(4, 5, ownHistoryData)[0]
	if last[2].Data != "h:4" {
		t.Errorf("на последней странице «вперёд» ведёт на %q", last[2].Data)
	}
	if last[0].Data != "h:3" {
		t.Errorf("на последней странице «назад» ведёт на %q", last[0].Data)
	}

	if mid := historyNav(2, 5, ownHistoryData)[0]; mid[1].Text != "3/5" {
		t.Errorf("счётчик показывает %q, ждали 3/5", mid[1].Text)
	}

	// Единственная страница: обе стрелки никуда не ведут.
	one := historyNav(0, 1, ownHistoryData)[0]
	if one[0].Data != "h:0" || one[2].Data != "h:0" {
		t.Errorf("на единственной странице стрелки ведут на %q и %q", one[0].Data, one[2].Data)
	}
}

// Тот же виджет листает и чужую историю в админке: меняется только адрес
// страницы, и кнопка «назад» добавляется отдельным рядом.
func TestHistoryNavForAdmin(t *testing.T) {
	data := func(p int) string { return fmt.Sprintf("u:g:777:3:%d", p) }
	back := []telegram.Button{{Text: "назад", Data: "u:p:3"}}
	kb := historyNav(1, 4, data, back)
	if len(kb) != 2 {
		t.Fatalf("рядов кнопок %d, ждали 2", len(kb))
	}
	if kb[0][0].Data != "u:g:777:3:0" || kb[0][2].Data != "u:g:777:3:2" {
		t.Errorf("стрелки ведут на %q и %q", kb[0][0].Data, kb[0][2].Data)
	}
	if kb[1][0].Text != "назад" {
		t.Errorf("второй ряд: %q", kb[1][0].Text)
	}
	// Данные кнопки должны влезать в предел телеграма.
	for _, row := range kb {
		for _, b := range row {
			if len(b.Data) > 64 {
				t.Errorf("данные кнопки длиной %d: %q", len(b.Data), b.Data)
			}
		}
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

// Кнопки карты должны нести номер матча и номер игрока: админ смотрит и чужие
// игры, и без аккаунта карта строилась бы по его собственной сводке.
func TestMapButtons(t *testing.T) {
	btns := mapButtons(9003682980, 109779233)
	if len(btns) != 2 {
		t.Fatalf("кнопок %d, ждали 2", len(btns))
	}
	if btns[0].Data != "k:9003682980:lane:109779233" {
		t.Errorf("кнопка линии ведёт на %q", btns[0].Data)
	}
	if btns[1].Data != "k:9003682980:all:109779233" {
		t.Errorf("кнопка матча ведёт на %q", btns[1].Data)
	}
	for _, b := range btns {
		if len(b.Data) > 64 {
			t.Errorf("данные кнопки длиной %d: %q", len(b.Data), b.Data)
		}
	}
}

// В админском виджете карта идёт отдельным рядом, «назад» — следующим.
func TestHistoryNavRows(t *testing.T) {
	data := func(p int) string { return fmt.Sprintf("u:g:1:0:%d", p) }
	kb := historyNav(0, 5, data, mapButtons(7, 42), []telegram.Button{{Text: "назад", Data: "u:p:0"}})
	if len(kb) != 3 {
		t.Fatalf("рядов %d, ждали 3", len(kb))
	}
	if kb[1][0].Text != "карта линии" {
		t.Errorf("второй ряд начинается с %q", kb[1][0].Text)
	}
	if kb[2][0].Text != "назад" {
		t.Errorf("третий ряд: %q", kb[2][0].Text)
	}
}
