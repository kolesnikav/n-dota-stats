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

// Кнопки несут всё, что нужно, чтобы вернуться туда, откуда пришли: матч,
// игрока и место просмотра. Запоминать это негде — телеграм присылает обратно
// только данные кнопки.
func TestMapButtons(t *testing.T) {
	ctx := viewCtx{Kind: "h", Page: 7}
	if btn := mapRow(9003682980, 109779233, ctx)[0]; btn.Data != "k:9003682980:all:109779233:h7" {
		t.Errorf("кнопка под сводкой ведёт на %q", btn.Data)
	}
	row := mapSwitch(9003682980, 109779233, windowMatch, ctx)
	if row[0].Data != "noop" {
		t.Errorf("текущее окно должно быть неактивным, а ведёт на %q", row[0].Data)
	}
	if row[1].Data != "kk:9003682980:lane:109779233:h7" {
		t.Errorf("переключатель ведёт на %q", row[1].Data)
	}
	if back := backRow(9003682980, 109779233, ctx)[0]; back.Data != "b:9003682980:109779233:h7" {
		t.Errorf("возврат ведёт на %q", back.Data)
	}
	// Самый длинный случай — админский просмотр чужой истории.
	admin := viewCtx{Kind: "u", Target: 5238663801, Card: 12, Page: 120}
	for _, b := range append(mapRow(9003682980, 1469925236, admin),
		append(mapSwitch(9003682980, 1469925236, windowMatch, admin),
			backRow(9003682980, 1469925236, admin)...)...) {
		if len(b.Data) > 64 {
			t.Errorf("данные кнопки длиной %d: %q", len(b.Data), b.Data)
		}
	}
}

// Место просмотра должно пережить дорогу туда и обратно через кнопку.
func TestViewCtxRoundTrip(t *testing.T) {
	cases := []viewCtx{
		{Kind: "s"},
		{Kind: "h", Page: 0},
		{Kind: "h", Page: 119},
		{Kind: "u", Target: 5238663801, Card: 3, Page: 42},
	}
	for _, want := range cases {
		if got := decodeCtx(want.encode()); got != want {
			t.Errorf("%+v превратился в %+v (через %q)", want, got, want.encode())
		}
	}
	// Мусор не должен ронять разбор: возвращаем обычную сводку.
	for _, junk := range []string{"", "x", "u1_2", "hабв"} {
		if got := decodeCtx(junk); got.Kind != "s" {
			t.Errorf("из %q получился %+v", junk, got)
		}
	}
}
