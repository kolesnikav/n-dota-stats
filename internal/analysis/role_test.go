package analysis_test

import (
	"testing"

	"github.com/kolesnikav/n-dota-stats/internal/analysis"
	"github.com/kolesnikav/n-dota-stats/internal/dota"
	"github.com/kolesnikav/n-dota-stats/internal/fixture"
)

// В матче 8999344582 у Radiant роли однозначны. Отдельно проверяем, что
// эвристика переживает Shadow Shaman, которому OpenDota выставила линию мида.
func TestDetectRolesRadiant(t *testing.T) {
	m, err := fixture.Match8999344582()
	if err != nil {
		t.Fatalf("фикстура: %v", err)
	}
	analysis.DetectRoles(m, nil)

	want := map[string]dota.Role{
		"Drow Ranger":   dota.RoleCarry,
		"Lina":          dota.RoleMid,
		"Night Stalker": dota.RoleOfflane,
		"Shadow Shaman": dota.RoleRoamer,
		"Mirana":        dota.RoleHard,
	}
	for hero, role := range want {
		p := m.FindByHero(hero)
		if p == nil {
			t.Fatalf("нет героя %s", hero)
		}
		if p.Role != role {
			t.Errorf("%s: получили %s, ждали %s", hero, p.Role, role)
		}
		if p.RoleSource != dota.SourceAuto {
			t.Errorf("%s: источник роли должен быть автоматическим", hero)
		}
	}
}

func TestRolesAreUniqueInTeam(t *testing.T) {
	m, _ := fixture.Match8999344582()
	analysis.DetectRoles(m, nil)
	for _, radiant := range []bool{true, false} {
		seen := map[dota.Role]string{}
		for _, p := range m.Team(radiant) {
			if prev, dup := seen[p.Role]; dup {
				t.Errorf("роль %s занята дважды: %s и %s", p.Role, prev, p.Name())
			}
			seen[p.Role] = p.Name()
		}
		if len(seen) != 5 {
			t.Errorf("ожидали пять разных ролей, получили %d", len(seen))
		}
	}
}

func TestHintOverridesHeuristic(t *testing.T) {
	m, _ := fixture.Match8999344582()
	mirana := m.FindByHero("Mirana")
	hint := func(acc int64, hero, lane int) (dota.Role, bool) {
		if acc == mirana.AccountID && hero == mirana.HeroID {
			return dota.RoleRoamer, true
		}
		return dota.RoleUnknown, false
	}
	analysis.DetectRoles(m, hint)
	if mirana.Role != dota.RoleRoamer {
		t.Fatalf("поправка не применилась: %s", mirana.Role)
	}
	if mirana.RoleSource != dota.SourceManual {
		t.Fatal("источник роли должен быть ручным")
	}
	if ss := m.FindByHero("Shadow Shaman"); ss.Role == dota.RoleRoamer {
		t.Error("занятая роль не должна выдаваться второй раз")
	}
}

func TestMetricsCoverEveryRole(t *testing.T) {
	m, _ := fixture.Match8999344582()
	analysis.DetectRoles(m, nil)
	for role := dota.RoleCarry; role <= dota.RoleHard; role++ {
		if len(analysis.MetricsFor(role, 0, false)) == 0 {
			t.Errorf("для роли %s не задано ни одного показателя", role)
		}
		if len(analysis.MetricsFor(role, 0, true)) == 0 {
			t.Errorf("для роли %s пустая короткая сводка", role)
		}
	}
}

func TestShortSummaryStaysShort(t *testing.T) {
	m, _ := fixture.Match8999344582()
	analysis.DetectRoles(m, nil)
	for _, p := range m.Players {
		if lines := analysis.Build(m, p, nil, true); len(lines) > 7 {
			t.Errorf("%s (%s): в короткой сводке %d строк", p.Name(), p.Role, len(lines))
		}
	}
}

// Показатели, требующие разбора реплея, не должны появляться на сыром скорборде.
func TestReplayMetricsHiddenWithoutParse(t *testing.T) {
	m, _ := fixture.Match8999344582()
	analysis.DetectRoles(m, nil)
	m.Detail = dota.DetailScoreboard
	p := m.FindByHero("Mirana")
	for _, l := range analysis.Build(m, p, nil, false) {
		if l.Label == "Стаки" || l.Label == "Варды" || l.Label == "Участие в файтах" {
			t.Errorf("показатель %q не должен показываться без разбора", l.Label)
		}
	}
}
