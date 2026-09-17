package analysis_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/kolesnikav/n-dota-stats/internal/analysis"
	"github.com/kolesnikav/n-dota-stats/internal/fixture"
)

// Снимок должен пережить запись в базу и дать те же строки, что и разбор
// матча напрямую.
func TestSnapshotRoundTrip(t *testing.T) {
	m, err := fixture.Match8999344582()
	if err != nil {
		t.Fatal(err)
	}
	analysis.DetectRoles(m, nil)
	p := m.FindByHero("Mirana")

	direct := analysis.Build(m, p, nil, false)

	blob, err := json.Marshal(analysis.Snap(m, p))
	if err != nil {
		t.Fatal(err)
	}
	var back analysis.Snapshot
	if err := json.Unmarshal(blob, &back); err != nil {
		t.Fatal(err)
	}
	restored := back.Render(nil, false)

	if len(restored) != len(direct) {
		t.Fatalf("после записи и чтения строк %d, было %d", len(restored), len(direct))
	}
	for i := range direct {
		if direct[i].String() != restored[i].String() {
			t.Errorf("строка %d разошлась:\n  было:  %s\n  стало: %s", i, direct[i], restored[i])
		}
		if direct[i].Verdict != restored[i].Verdict {
			t.Errorf("строка %d: вердикт %d вместо %d", i, restored[i].Verdict, direct[i].Verdict)
		}
	}

	// Шапка тоже должна уцелеть: без неё историю не показать.
	if back.Hero != "Mirana" || back.Duration != m.Duration || back.Kills != p.Kills {
		t.Errorf("шапка снимка потеряна: %+v", back)
	}
}

// Сравнения, зависящие от накопленного, должны считаться при показе, а не
// храниться: иначе история застынет на том, что было в день матча.
func TestSnapshotKeepsOnlyMatchFacts(t *testing.T) {
	m, _ := fixture.Match8999344582()
	analysis.DetectRoles(m, nil)
	snap := analysis.Snap(m, m.FindByHero("Mirana"))
	for _, l := range snap.Lines {
		for _, note := range l.Fixed {
			if strings.Contains(note, "медиана") || strings.Contains(note, "твоё среднее") {
				t.Errorf("в снимок попала меняющаяся пометка: %q у %q", note, l.Label)
			}
		}
	}
}
