package meta_test

import (
	"math"
	"strings"
	"testing"

	"github.com/kolesnikav/n-dota-stats/internal/dota"
	"github.com/kolesnikav/n-dota-stats/internal/fixture"
	"github.com/kolesnikav/n-dota-stats/internal/meta"
)

func load(t *testing.T) *meta.Metadata {
	t.Helper()
	raw, err := fixture.Raw("match_8999344582.meta")
	if err != nil {
		t.Fatalf("фикстура метаданных: %v", err)
	}
	md, err := meta.Parse(raw)
	if err != nil {
		t.Fatalf("разбор: %v", err)
	}
	return md
}

func TestParseStructure(t *testing.T) {
	md := load(t)
	if md.MatchID != 8999344582 {
		t.Errorf("match_id %d", md.MatchID)
	}
	if len(md.Players) != 10 {
		t.Fatalf("игроков %d", len(md.Players))
	}
	slots := map[int]bool{}
	for _, p := range md.Players {
		slots[p.Slot] = true
		if p.TeamID != 2 && p.TeamID != 3 {
			t.Errorf("слот %d: команда %d", p.Slot, p.TeamID)
		}
	}
	for _, want := range []int{0, 1, 2, 3, 4, 128, 129, 130, 131, 132} {
		if !slots[want] {
			t.Errorf("нет слота %d", want)
		}
	}
}

// Секунды контроля Мираны в метаданных должны совпасть с тем, что
// показывает OpenDota (70.7336).
func TestStunsMatchOpenDota(t *testing.T) {
	md := load(t)
	m, err := fixture.Match8999344582()
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, mp := range md.Players {
		p := m.FindBySlot(mp.Slot)
		if p == nil || p.Stuns == 0 {
			continue
		}
		if diff := math.Abs(mp.Stuns - p.Stuns); diff > 0.01 {
			t.Errorf("слот %d: контроль %.4f против %.4f у OpenDota", mp.Slot, mp.Stuns, p.Stuns)
		}
		checked++
	}
	if checked < 3 {
		t.Fatalf("сверено всего %d игроков", checked)
	}
}

// Порядок прокачки из метаданных должен совпасть с ability_upgrades_arr.
func TestAbilityOrderMatchesOpenDota(t *testing.T) {
	md := load(t)
	m, _ := fixture.Match8999344582()
	checked := 0
	for _, mp := range md.Players {
		p := m.FindBySlot(mp.Slot)
		if p == nil || len(p.AbilityUpgrades) == 0 {
			continue
		}
		if len(mp.AbilityUpgrades) != len(p.AbilityUpgrades) {
			t.Errorf("слот %d: длина прокачки %d против %d",
				mp.Slot, len(mp.AbilityUpgrades), len(p.AbilityUpgrades))
			continue
		}
		for i := range mp.AbilityUpgrades {
			if mp.AbilityUpgrades[i] != p.AbilityUpgrades[i] {
				t.Errorf("слот %d, шаг %d: %d против %d",
					mp.Slot, i, mp.AbilityUpgrades[i], p.AbilityUpgrades[i])
				break
			}
		}
		checked++
	}
	if checked < 5 {
		t.Fatalf("сверено всего %d игроков", checked)
	}
}

func TestLevelUpTimesAreIncreasing(t *testing.T) {
	md := load(t)
	for _, p := range md.Players {
		if len(p.LevelUpTimes) == 0 {
			continue
		}
		for i := 1; i < len(p.LevelUpTimes); i++ {
			if p.LevelUpTimes[i] < p.LevelUpTimes[i-1] {
				t.Errorf("слот %d: время уровней не возрастает: %v", p.Slot, p.LevelUpTimes)
				break
			}
		}
	}
}

// Из снимков инвентаря должны получаться осмысленные тайминги предметов.
func TestItemTimings(t *testing.T) {
	md := load(t)
	m, _ := fixture.Match8999344582()
	applied := md.Apply(m)
	if applied != 10 {
		t.Fatalf("применено к %d игрокам", applied)
	}
	if m.Detail < dota.DetailMeta {
		t.Error("уровень детализации не поднялся")
	}
	mirana := m.FindByHero("Mirana")
	if len(mirana.FirstItem) == 0 {
		t.Fatal("нет таймингов предметов")
	}
	// Мирана купила ботинки на 5:03 — снимки идут раз в 30 секунд,
	// поэтому ждём попадание в ближайший снимок после покупки.
	boots, ok := mirana.FirstPurchase["boots"]
	if !ok {
		t.Fatalf("ботинок нет среди %d предметов", len(mirana.FirstItem))
	}
	if boots < 280 || boots > 340 {
		t.Errorf("ботинки на %d секунде, ждали около 303", boots)
	}
}

// Лучший игрок матча и два кандидата лежат в метаданных — тот самый список,
// который Dota показывает после игры.
//
// Матч 8999344582: на экране была Winter Wyvern, слот 130. Вручную этот матч
// размечен неточно (игрок сам сомневался в третьем), поэтому проверяем по
// первому месту и по составу, а не по памяти.
func TestMVPFromMetadata(t *testing.T) {
	md := load(t)
	if len(md.MVP) != 3 {
		t.Fatalf("записей о лучших %d, ждали 3", len(md.MVP))
	}
	if got := md.MVP[0].Slot; got != 130 {
		t.Errorf("лучший в слоте %d, ждали 130 (Winter Wyvern)", got)
	}
	// Первое место всегда объяснено наградами — по ним Dota и выбирает.
	if len(md.MVP[0].Accolades) == 0 {
		t.Error("у лучшего игрока нет ни одной награды")
	}
	var hasHeroSpecific bool
	for _, a := range md.MVP[0].Accolades {
		if a == "WinterWyvern_ThreeHeroCurses" {
			hasHeroSpecific = true
		}
		if strings.HasPrefix(a, "kKillEater") || strings.HasPrefix(a, "CMvpData") {
			t.Errorf("имя награды не очищено от приставки: %q", a)
		}
	}
	if !hasHeroSpecific {
		t.Errorf("не нашлось геройской награды Winter Wyvern: %v", md.MVP[0].Accolades)
	}
	// Слоты не повторяются: это три разных игрока.
	seen := map[int]bool{}
	for _, m := range md.MVP {
		if seen[m.Slot] {
			t.Errorf("слот %d встретился дважды", m.Slot)
		}
		seen[m.Slot] = true
	}
}

// Оценки Valve тоже лежат в метаданных. Они разной природы, поэтому проверяем
// только то, что они прочитались и попали в разумные пределы.
func TestValveScores(t *testing.T) {
	md := load(t)
	var withFight int
	for _, p := range md.Players {
		if p.FightScore > 0 {
			withFight++
		}
		if p.FightScore < 0 || p.FightScore > 1.01 {
			t.Errorf("слот %d: оценка боя %v вне 0…1", p.Slot, p.FightScore)
		}
		if p.FarmScore < 0 || p.SupportScore < 0 || p.PushScore < 0 {
			t.Errorf("слот %d: отрицательная оценка", p.Slot)
		}
	}
	// Ноль — законное значение: в этом матче один игрок так и не поучаствовал
	// ни в одной драке. Но если нулей много, значит поле просто не читается.
	if withFight < 8 {
		t.Errorf("оценка боя ненулевая только у %d игроков из 10", withFight)
	}
}
