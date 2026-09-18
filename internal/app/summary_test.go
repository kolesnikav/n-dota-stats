package app

import (
	"strings"
	"testing"
)

// Подпись к картинке ограничена телеграмом, и разметка в предел не идёт.
func TestVisibleLen(t *testing.T) {
	if got := VisibleLen("<b>раз</b> два"); got != 7 {
		t.Errorf("видимых символов %d, ждали 7", got)
	}
	if got := VisibleLen("без разметки"); got != 12 {
		t.Errorf("видимых символов %d, ждали 12", got)
	}
}

// Длинная подпись урезается вместе с разметкой, а не по символам: обрубленный
// тег телеграм не принимает вовсе, и вместо картинки не приходит ничего.
// Именно на этом /history и замолчала.
func TestCaptionStripsTagsWhenTooLong(t *testing.T) {
	text := "<b>шапка</b>\n" + strings.Repeat("<i>строка показателя</i>\n", 70)
	got := caption(text)
	if VisibleLen(got) > captionLimit {
		t.Errorf("подпись длиной %d, предел %d", VisibleLen(got), captionLimit)
	}
	if strings.ContainsAny(got, "<>") {
		t.Errorf("в урезанной подписи осталась разметка: %q", got[:60])
	}
	if !strings.HasSuffix(got, "…") {
		t.Error("обрезка не помечена многоточием")
	}
}

// Короткая сводка не трогается.
func TestCaptionKeepsShort(t *testing.T) {
	text := "<b>Победа</b>\nКто-то · мид · 1/2/3"
	if got := caption(text); got != text {
		t.Errorf("короткую подпись изменили: %q", got)
	}
}
