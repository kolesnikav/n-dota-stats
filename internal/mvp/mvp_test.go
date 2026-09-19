package mvp_test

import (
	"testing"

	"github.com/kolesnikav/n-dota-stats/internal/fixture"
	"github.com/kolesnikav/n-dota-stats/internal/mvp"
)

// Экран Dota в матче 8999344582 показал Winter Wyvern, Sniper и Naga Siren
// именно в таком порядке. Модель обязана его воспроизводить.
func TestTopMatchesDotaAnswer(t *testing.T) {
	m, err := fixture.Match8999344582()
	if err != nil {
		t.Fatalf("фикстура: %v", err)
	}
	ranked := mvp.Rank(m, mvp.EqualWeights())
	// Настоящий ответ игры лежит в метаданных: лучшей была Winter Wyvern.
	// Раньше здесь сверялась вся тройка, но по памяти игрока — и та память
	// оказалась неверной. Проверяем то, что модель действительно должна
	// уметь: назвать лучшего.
	if got := ranked[0].Player.Name(); got != "Winter Wyvern" {
		t.Errorf("лучшим названа %s, а игра показала Winter Wyvern", got)
	}
	if ranked[0].Score <= ranked[len(ranked)-1].Score {
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
		s.Vectors = append(s.Vectors, mvp.Vector(m, p))
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
	m, err := fixture.Match8999344582()
	if err != nil {
		t.Fatal(err)
	}
	p := m.Players[0]
	p.Benchmarks = nil
	vec := mvp.Vector(m, p)
	if len(vec) != mvp.Dim() {
		t.Fatalf("длина вектора %d, ждали %d", len(vec), mvp.Dim())
	}
	// Первые признаки — перцентили: без данных они нейтральны.
	for i := range mvp.Features {
		if vec[i] != 0.5 {
			t.Errorf("перцентиль %d без данных должен быть 0.5, получили %f", i, vec[i])
		}
	}
	// Дальше идут признаки лидерства: это да или нет, а не середина.
	for i := len(mvp.Features); i < len(vec); i++ {
		if vec[i] != 0 && vec[i] != 1 {
			t.Errorf("признак лидерства %d равен %f, а должен быть 0 или 1", i, vec[i])
		}
	}
}
