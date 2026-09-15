package app

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/kolesnikav/n-dota-stats/internal/store"
)

func testDB(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func seed(t *testing.T, db *store.DB) []store.User {
	t.Helper()
	rows := []struct {
		chat   int64
		name   string
		status store.Status
	}{
		{1, "админ", store.StatusAdmin},
		{2, "новичок", store.StatusPending},
		{3, "друг", store.StatusVerified},
		{4, "бывший", store.StatusBlocked},
	}
	for _, r := range rows {
		if err := db.UpsertUser(store.User{
			ChatID: r.chat, AccountID: r.chat * 100, Nickname: r.name, Status: r.status,
		}); err != nil {
			t.Fatal(err)
		}
	}
	users, err := db.Users()
	if err != nil {
		t.Fatal(err)
	}
	return users
}

// Карточка листается и всегда показывает своё место в списке.
func TestUsersCardPagination(t *testing.T) {
	db := testDB(t)
	users := seed(t, db)

	text, kb := usersCard(users, 0, db)
	if !strings.Contains(text, "Пользователь 1 из 4") {
		t.Errorf("нет номера карточки:\n%s", text)
	}
	if len(kb) == 0 {
		t.Fatal("нет клавиатуры")
	}
	// на первой карточке кнопки «назад» быть не должно
	for _, b := range kb[0] {
		if b.Text == "◀" {
			t.Error("на первой карточке есть кнопка назад")
		}
	}
	_, kbLast := usersCard(users, len(users)-1, db)
	for _, b := range kbLast[0] {
		if b.Text == "▶" {
			t.Error("на последней карточке есть кнопка вперёд")
		}
	}
	// выход за границы не должен ронять
	if _, _ = usersCard(users, 99, db); false {
		t.Fatal("недостижимо")
	}
	if _, _ = usersCard(nil, 0, db); false {
		t.Fatal("недостижимо")
	}
}

// Набор действий зависит от статуса.
func TestUsersCardActionsByStatus(t *testing.T) {
	db := testDB(t)
	users := seed(t, db)
	want := map[string][]string{
		"админ":   {}, // единственного админа разжаловать нельзя
		"новичок": {"принять", "отклонить"},
		"друг":    {"заблокировать", "сделать админом"},
		"бывший":  {"разблокировать"},
	}
	for i, u := range users {
		_, kb := usersCard(users, i, db)
		var labels []string
		for _, row := range kb {
			for _, b := range row {
				labels = append(labels, b.Text)
			}
		}
		for _, expect := range want[u.Nickname] {
			found := false
			for _, l := range labels {
				if l == expect {
					found = true
				}
			}
			if !found {
				t.Errorf("%s (%s): нет кнопки %q, есть %v", u.Nickname, u.Status, expect, labels)
			}
		}
		if u.Nickname == "админ" {
			for _, l := range labels {
				if l == "снять права" {
					t.Error("последнего админа разжаловать нельзя")
				}
			}
		}
	}
}

func TestUsersCardCallbackDataFits(t *testing.T) {
	db := testDB(t)
	users := seed(t, db)
	for i := range users {
		_, kb := usersCard(users, i, db)
		for _, row := range kb {
			for _, b := range row {
				if len(b.Data) > 64 {
					t.Errorf("callback_data длиннее 64 байт: %q", b.Data)
				}
			}
		}
	}
}

func TestParseAccountID(t *testing.T) {
	cases := map[string]int64{
		"109779233":    109779233,
		"  109779233 ": 109779233,
		"https://ru.dotabuff.com/players/109779233":  109779233,
		"https://www.opendota.com/players/109779233": 109779233,
	}
	for in, want := range cases {
		got, ok := parseAccountID(in)
		if !ok || got != want {
			t.Errorf("%q: получили %d (%v), ждали %d", in, got, ok, want)
		}
	}
	if _, ok := parseAccountID("привет"); ok {
		t.Error("из текста без чисел ID браться не должен")
	}
}
