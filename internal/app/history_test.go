package app

import (
	"strings"
	"testing"
)

// На краях списка стрелка не должна уводить за границы.
func TestHistoryKeyboardEdges(t *testing.T) {
	first := historyKeyboard(0, 5)[0]
	if first[0].Data != "h:0" {
		t.Errorf("на первой странице «назад» ведёт на %q", first[0].Data)
	}
	if first[2].Data != "h:1" {
		t.Errorf("на первой странице «вперёд» ведёт на %q", first[2].Data)
	}

	last := historyKeyboard(4, 5)[0]
	if last[2].Data != "h:4" {
		t.Errorf("на последней странице «вперёд» ведёт на %q", last[2].Data)
	}
	if last[0].Data != "h:3" {
		t.Errorf("на последней странице «назад» ведёт на %q", last[0].Data)
	}

	if mid := historyKeyboard(2, 5)[0]; mid[1].Text != "3/5" {
		t.Errorf("счётчик показывает %q, ждали 3/5", mid[1].Text)
	}

	// Единственная страница: обе стрелки никуда не ведут.
	one := historyKeyboard(0, 1)[0]
	if one[0].Data != "h:0" || one[2].Data != "h:0" {
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
