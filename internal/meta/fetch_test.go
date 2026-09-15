package meta_test

import (
	"testing"

	"github.com/kolesnikav/n-dota-stats/internal/fixture"
	"github.com/kolesnikav/n-dota-stats/internal/meta"
)

// Файл приходит с расширением .bz2, но внутри zstd — распаковщик обязан
// разбираться по сигнатуре, а не по имени.
func TestDecompressDetectsZstd(t *testing.T) {
	raw, err := fixture.Raw("match_8999344582.meta.compressed")
	if err != nil {
		t.Fatalf("фикстура: %v", err)
	}
	if raw[0] != 0x28 || raw[1] != 0xB5 {
		t.Fatalf("фикстура не zstd: % x", raw[:4])
	}
	plain, err := meta.Decompress(raw)
	if err != nil {
		t.Fatalf("распаковка: %v", err)
	}
	if len(plain) < len(raw) {
		t.Fatalf("после распаковки стало меньше: %d против %d", len(plain), len(raw))
	}
	md, err := meta.Parse(plain)
	if err != nil {
		t.Fatalf("разбор распакованного: %v", err)
	}
	if md.MatchID != 8999344582 || len(md.Players) != 10 {
		t.Fatalf("матч %d, игроков %d", md.MatchID, len(md.Players))
	}
}

func TestDecompressPassesPlainThrough(t *testing.T) {
	plain := []byte{0x08, 0x03, 0x10, 0x01}
	out, err := meta.Decompress(plain)
	if err != nil || len(out) != len(plain) {
		t.Fatalf("уже распакованные данные должны проходить как есть: %v", err)
	}
}

func TestURLShape(t *testing.T) {
	got := meta.URL(181, 8999344582, 1408381858)
	want := "http://replay181.valve.net/570/8999344582_1408381858.meta.bz2"
	if got != want {
		t.Errorf("получили %s", got)
	}
}
