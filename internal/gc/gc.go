// Package gc — доступ к Game Coordinator за ключом реплея.
//
// Ссылку на реплей и на метаданные нельзя собрать без replay_salt, а его
// отдаёт только Game Coordinator по запросу CMsgGCMatchDetailsRequest — то
// есть нужен Steam-аккаунт, залогиненный в Dota 2. В Web API этого поля нет.
//
// Ограничения, которые стоит уважать (они же указаны в коде OpenDota,
// svc/retriever.ts): около 100 запросов матчей на аккаунт в сутки и 500 на IP.
// Поэтому поверх любого провайдера стоит суточный бюджет — см. store.TakeGCBudget.
//
// Запрос работает по любому match_id, а не только по матчам самого аккаунта:
// одного бот-аккаунта хватает на всех пользователей бота.
package gc

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// ErrNotConfigured возвращается, когда доступ к GC не настроен.
var ErrNotConfigured = errors.New("Game Coordinator не настроен")

// Salt — то, что нужно для сборки ссылки на реплей и метаданные.
type Salt struct {
	Cluster int
	Salt    uint32
}

// Provider отдаёт ключ реплея для матча.
type Provider interface {
	ReplaySalt(matchID int64) (Salt, error)
	Name() string
}

// Disabled — заглушка на случай, когда Steam-аккаунт не заведён. Бот с ней
// работает, просто без разбора реплеев: сводка строится по скорборду.
type Disabled struct{}

func (Disabled) Name() string { return "выключен" }

func (Disabled) ReplaySalt(int64) (Salt, error) { return Salt{}, ErrNotConfigured }

// Manual — ключи, введённые руками. Нужен для отладки и для разбора отдельных
// матчей, когда Steam-аккаунта нет: ключ виден в адресе реплея, который можно
// получить в клиенте Dota кнопкой «Скачать реплей».
//
// Формат записи: "матч:кластер:ключ", например
// "8999344582:181:1408381858".
type Manual struct{ entries map[int64]Salt }

func NewManual(specs []string) (*Manual, error) {
	m := &Manual{entries: map[int64]Salt{}}
	for _, spec := range specs {
		spec = strings.TrimSpace(spec)
		if spec == "" {
			continue
		}
		parts := strings.Split(spec, ":")
		if len(parts) != 3 {
			return nil, fmt.Errorf("ожидал матч:кластер:ключ, получил %q", spec)
		}
		matchID, err := strconv.ParseInt(parts[0], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("номер матча в %q: %w", spec, err)
		}
		cluster, err := strconv.Atoi(parts[1])
		if err != nil {
			return nil, fmt.Errorf("кластер в %q: %w", spec, err)
		}
		salt, err := strconv.ParseUint(parts[2], 10, 32)
		if err != nil {
			return nil, fmt.Errorf("ключ в %q: %w", spec, err)
		}
		m.entries[matchID] = Salt{Cluster: cluster, Salt: uint32(salt)}
	}
	return m, nil
}

func (m *Manual) Name() string { return "ручные ключи" }

func (m *Manual) ReplaySalt(matchID int64) (Salt, error) {
	s, ok := m.entries[matchID]
	if !ok {
		return Salt{}, ErrNotConfigured
	}
	return s, nil
}
