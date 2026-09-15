// Package fixture загружает сохранённый матч для тестов и примеров.
package fixture

import (
	"os"
	"path/filepath"
	"runtime"

	"github.com/kolesnikav/n-dota-stats/internal/dota"
	"github.com/kolesnikav/n-dota-stats/internal/odota"
)

// Dir возвращает каталог testdata репозитория.
func Dir() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "testdata")
}

// Raw читает файл из testdata.
func Raw(name string) ([]byte, error) { return os.ReadFile(filepath.Join(Dir(), name)) }

// Match8999344582 — реальный матч: Mirana на пятой позиции, поражение Radiant.
// На нём проверяется и модель MVP, и определение ролей.
func Match8999344582() (*dota.Match, error) {
	raw, err := Raw("match_8999344582_opendota.json")
	if err != nil {
		return nil, err
	}
	return odota.Decode(raw)
}
