package store_test

import (
	"path/filepath"
	"testing"

	"github.com/kolesnikav/n-dota-stats/internal/dota"
	"github.com/kolesnikav/n-dota-stats/internal/fixture"
	"github.com/kolesnikav/n-dota-stats/internal/mvp"
	"github.com/kolesnikav/n-dota-stats/internal/store"
)

func open(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("база: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// Админом становится первый пользователь при пустой базе. Перезапуск процесса
// не должен открывать эту дверь заново — проверяем через счётчик админов.
func TestAdminBootstrap(t *testing.T) {
	db := open(t)
	if db.AdminCount() != 0 {
		t.Fatal("в свежей базе админов быть не должно")
	}
	first := store.StatusPending
	if db.AdminCount() == 0 {
		first = store.StatusAdmin
	}
	if err := db.UpsertUser(store.User{ChatID: 1, AccountID: 100, Nickname: "первый", Status: first}); err != nil {
		t.Fatal(err)
	}
	if db.AdminCount() != 1 {
		t.Fatal("первый пользователь должен был стать админом")
	}
	second := store.StatusPending
	if db.AdminCount() == 0 {
		second = store.StatusAdmin
	}
	if second != store.StatusPending {
		t.Fatal("второму админ доставаться не должен")
	}
	_ = db.UpsertUser(store.User{ChatID: 2, AccountID: 200, Status: second})
	u, _ := db.User(2)
	if u.Status != store.StatusPending {
		t.Fatalf("второй пользователь получил статус %s", u.Status)
	}
	if len(db.Admins()) != 1 {
		t.Fatal("админ должен быть ровно один")
	}
}

func TestStatusVerified(t *testing.T) {
	cases := map[store.Status]bool{
		store.StatusPending:  false,
		store.StatusBlocked:  false,
		store.StatusVerified: true,
		store.StatusAdmin:    true,
	}
	for s, want := range cases {
		if s.Verified() != want {
			t.Errorf("%s: ждали %v", s, want)
		}
	}
}

// Один матч ставится в очередь ровно один раз, сколько бы участников бота в
// нём ни было.
func TestReplayClaimIsIdempotent(t *testing.T) {
	db := open(t)
	m, err := fixture.Match8999344582()
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SaveMatch(m, []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	first, err := db.ClaimForReplay(m.ID)
	if err != nil || !first {
		t.Fatalf("первая заявка не прошла: %v %v", first, err)
	}
	for i := 0; i < 3; i++ {
		again, err := db.ClaimForReplay(m.ID)
		if err != nil {
			t.Fatal(err)
		}
		if again {
			t.Fatal("матч поставлен в очередь повторно")
		}
	}
	state, ok := db.KnownMatch(m.ID)
	if !ok || state != store.ReplayQueued {
		t.Fatalf("состояние %v", state)
	}
}

// Матч, пропущенный из-за неподтверждённых участников, должен снова попадать в
// очередь, когда среди игроков появляется подтверждённый.
func TestSkippedMatchCanBeQueuedLater(t *testing.T) {
	db := open(t)
	m, _ := fixture.Match8999344582()
	_ = db.SaveMatch(m, []byte(`{}`))
	if err := db.SetReplayState(m.ID, store.ReplaySkipped, ""); err != nil {
		t.Fatal(err)
	}
	ok, err := db.ClaimForReplay(m.ID)
	if err != nil || !ok {
		t.Fatalf("пропущенный матч должен становиться в очередь: %v %v", ok, err)
	}
}

// Разметка у каждого пользователя своя, даже если матч общий.
func TestTwoUsersShareMatchWithOwnLabels(t *testing.T) {
	db := open(t)
	m, _ := fixture.Match8999344582()
	_ = db.SaveMatch(m, []byte(`{}`))

	a := m.Players[0]
	b := m.Players[1]
	for i, p := range []*dota.Player{a, b} {
		if err := db.LinkMatchUser(store.MatchUser{
			MatchID: m.ID, AccountID: p.AccountID, ChatID: int64(i + 1),
			Role: dota.RoleCarry, Predicted: []int{1, 2, 3},
		}, map[string]float64{"gpm": float64(p.GPM)}); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.SetActual(m.ID, a.AccountID, []int{5}); err != nil {
		t.Fatal(err)
	}
	if err := db.SetActual(m.ID, b.AccountID, []int{7}); err != nil {
		t.Fatal(err)
	}
	if got := db.Actual(m.ID, a.AccountID); len(got) != 1 || got[0] != 5 {
		t.Errorf("разметка первого: %v", got)
	}
	if got := db.Actual(m.ID, b.AccountID); len(got) != 1 || got[0] != 7 {
		t.Errorf("разметка второго: %v", got)
	}
	parts, err := db.Participants(m.ID)
	if err != nil || len(parts) != 2 {
		t.Fatalf("участников %d, ошибка %v", len(parts), err)
	}
}

func TestLabelledBuildsVectors(t *testing.T) {
	db := open(t)
	m, _ := fixture.Match8999344582()
	_ = db.SaveMatch(m, []byte(`{}`))
	ww := m.FindByHero("Winter Wyvern")
	me := m.FindByHero("Mirana")
	_ = db.LinkMatchUser(store.MatchUser{
		MatchID: m.ID, AccountID: me.AccountID, ChatID: 1, Role: dota.RoleHard,
	}, nil)
	_ = db.SetActual(m.ID, me.AccountID, []int{ww.Slot})

	keys := make([]string, 0, mvp.Dim())
	for _, f := range mvp.Features {
		keys = append(keys, f.Key)
	}
	labelled, err := db.Labelled(me.AccountID, keys)
	if err != nil {
		t.Fatal(err)
	}
	if len(labelled) != 1 {
		t.Fatalf("размеченных матчей %d", len(labelled))
	}
	if len(labelled[0].Vectors) != 10 || len(labelled[0].Vectors[0]) != mvp.Dim() {
		t.Fatalf("векторы: %d игроков по %d признаков",
			len(labelled[0].Vectors), len(labelled[0].Vectors[0]))
	}
	if labelled[0].Actual[0] != ww.Slot {
		t.Errorf("метка %v, ждали слот %d", labelled[0].Actual, ww.Slot)
	}
}

func TestAverageOverOwnHistory(t *testing.T) {
	db := open(t)
	for i, gpm := range []float64{300, 400, 500} {
		_ = db.LinkMatchUser(store.MatchUser{
			MatchID: int64(1000 + i), AccountID: 7, ChatID: 1, Role: dota.RoleHard,
		}, map[string]float64{"gpm": gpm})
	}
	avg, games, ok := db.Average(7, dota.RoleHard, "gpm")
	if !ok || games != 3 || avg != 400 {
		t.Fatalf("среднее %v по %d играм, ok=%v", avg, games, ok)
	}
	if _, _, ok := db.Average(7, dota.RoleCarry, "gpm"); ok {
		t.Error("для другой роли истории быть не должно")
	}
}

func TestGCBudget(t *testing.T) {
	db := open(t)
	for i := 0; i < 3; i++ {
		if !db.TakeGCBudget(3) {
			t.Fatalf("заявка %d должна проходить", i+1)
		}
	}
	if db.TakeGCBudget(3) {
		t.Fatal("лимит не сработал")
	}
	if db.GCBudgetUsed() != 3 {
		t.Fatalf("израсходовано %d", db.GCBudgetUsed())
	}
}

func TestRoleHintRoundTrip(t *testing.T) {
	db := open(t)
	if _, ok := db.RoleHint(1, 2, 3); ok {
		t.Fatal("поправки взяться неоткуда")
	}
	if err := db.SaveRoleHint(1, 2, 3, dota.RoleOfflane); err != nil {
		t.Fatal(err)
	}
	role, ok := db.RoleHint(1, 2, 3)
	if !ok || role != dota.RoleOfflane {
		t.Fatalf("получили %v %v", role, ok)
	}
}
