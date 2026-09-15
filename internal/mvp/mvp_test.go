package mvp_test

import (
	"testing"

	"github.com/kolesnikav/n-dota-stats/internal/fixture"
	"github.com/kolesnikav/n-dota-stats/internal/mvp"
)

// Экран Dota в матче 8999344582 показал Winter Wyvern, Sniper и Naga Siren
// именно в таком порядке. Модель обязана его воспроизводить.
func TestTop3MatchesDotaScreen(t *testing.T) {
	m, err := fixture.Match8999344582()
	if err != nil {
		t.Fatalf("фикстура: %v", err)
	}
	ranked := mvp.Rank(m, mvp.EqualWeights())
	want := []string{"Winter Wyvern", "Sniper", "Naga Siren"}
	for i, name := range want {
		if got := ranked[i].Player.Name(); got != name {
			t.Errorf("место %d: получили %s, ждали %s", i+1, got, name)
		}
	}
	if ranked[0].Score <= ranked[1].Score {
		t.Error("оценки должны убывать")
	}
}

func TestTrainLearnsLabelledMVP(t *testing.T) {
	m, err := fixture.Match8999344582()
	if err != nil {
		t.Fatalf("фикстура: %v", err)
	}
	var s mvp.Sample
	for _, p := range m.Players {
		s.Vectors = append(s.Vectors, mvp.Vector(p))
		s.Slots = append(s.Slots, p.Slot)
		if p.Name() == "Naga Siren" {
			s.MVPSlot = p.Slot // намеренно «неправильный» ответ
		}
	}
	w, ll := mvp.Train([]mvp.Sample{s}, 0.01, 400, 0.6)
	if len(w) != mvp.Dim() {
		t.Fatalf("размерность весов %d", len(w))
	}
	top1, _, total := mvp.Accuracy([]mvp.Sample{s}, w)
	if total != 1 || top1 != 1 {
		t.Errorf("после обучения на одном матче ответ обязан угадываться: %d из %d", top1, total)
	}
	if ll > 0 {
		t.Errorf("лог-правдоподобие должно быть отрицательным, получили %f", ll)
	}
}

func TestVectorMissingBenchmarkIsNeutral(t *testing.T) {
	m, _ := fixture.Match8999344582()
	p := m.Players[0]
	p.Benchmarks = nil
	for i, v := range mvp.Vector(p) {
		if v != 0.5 {
			t.Fatalf("признак %d без данных должен быть 0.5, получили %f", i, v)
		}
	}
}
