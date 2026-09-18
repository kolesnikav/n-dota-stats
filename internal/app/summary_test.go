package app

import (
	"strings"
	"testing"
)

// Подпись к картинке ограничена телеграмом, и разметка в предел не идёт.
func TestVisibleLen(t *testing.T) {
	if got := visibleLen("<b>раз</b> два"); got != 7 {
		t.Errorf("видимых символов %d, ждали 7", got)
	}
	if got := visibleLen("без разметки"); got != 12 {
		t.Errorf("видимых символов %d, ждали 12", got)
	}
}

// Длинная сводка урезается по разделам, а не по символам: обрубок на полуслове
// выглядит поломкой, а сводка без рейтинга — просто короче.
func TestCaptionTrimsBySection(t *testing.T) {
	// Тело помещается в предел, а вместе с рейтингом — уже нет.
	body := strings.Repeat("строка показателя\n", 55)
	text := body + "<b>ЛУЧШИЕ ПО МОЕЙ ФОРМУЛЕ</b>\n1. Кто-то\n2. Кто-то\n3. Кто-то"
	if visibleLen(body) > captionLimit {
		t.Fatalf("тело теста само длиннее предела: %d", visibleLen(body))
	}
	got := caption(text)
	if strings.Contains(got, "ЛУЧШИЕ") {
		t.Error("рейтинг остался, хотя подпись не помещалась")
	}
	if visibleLen(got) > captionLimit {
		t.Errorf("подпись длиной %d, предел %d", visibleLen(got), captionLimit)
	}
	if !strings.HasSuffix(got, "показателя") {
		t.Errorf("подпись обрывается не по разделу: ...%q", got[len(got)-30:])
	}
}

// Короткая сводка не трогается.
func TestCaptionKeepsShort(t *testing.T) {
	text := "<b>Победа</b>\nКто-то · мид · 1/2/3"
	if got := caption(text); got != text {
		t.Errorf("короткую подпись изменили: %q", got)
	}
}
