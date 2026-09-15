package replay

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/kolesnikav/n-dota-stats/internal/dota"
	"github.com/kolesnikav/n-dota-stats/internal/meta"
)

// MaxSize — предел на размер реплея. Обычный матч весит 40–80 МБ.
const MaxSize = 300 << 20

// Fetch скачивает полный реплей и разбирает его.
//
// Файл в память не кладём целиком дважды: качаем, распаковываем и сразу
// отдаём парсеру потоком.
func Fetch(client *http.Client, cluster int, matchID int64, salt uint32, m *dota.Match) (*Result, error) {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Minute}
	}
	resp, err := client.Get(meta.ReplayURL(cluster, matchID, salt))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("реплей матча %d: код %d", matchID, resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, MaxSize))
	if err != nil {
		return nil, err
	}
	plain, err := meta.Decompress(raw)
	if err != nil {
		return nil, fmt.Errorf("распаковка реплея: %w", err)
	}
	return Parse(bytes.NewReader(plain), m)
}
