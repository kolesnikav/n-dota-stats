package meta

import (
	"bytes"
	"compress/bzip2"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/klauspost/compress/zstd"
)

// URL собирает адрес файла метаданных.
//
// Обрати внимание на расширение: Valve отдаёт его как .meta.bz2, но внутри
// давно zstd. Мы проверяем сигнатуру, а не имя.
func URL(cluster int, matchID int64, salt uint32) string {
	return fmt.Sprintf("http://replay%d.valve.net/570/%d_%d.meta.bz2", cluster, matchID, salt)
}

// ReplayURL — адрес полного реплея, на случай когда нужны варды и позиции.
func ReplayURL(cluster int, matchID int64, salt uint32) string {
	return fmt.Sprintf("http://replay%d.valve.net/570/%d_%d.dem.bz2", cluster, matchID, salt)
}

// Decompress распаковывает файл, определяя формат по сигнатуре: zstd или bzip2.
func Decompress(raw []byte) ([]byte, error) {
	switch {
	case len(raw) >= 4 && raw[0] == 0x28 && raw[1] == 0xB5 && raw[2] == 0x2F && raw[3] == 0xFD:
		dec, err := zstd.NewReader(bytes.NewReader(raw))
		if err != nil {
			return nil, err
		}
		defer dec.Close()
		return io.ReadAll(dec)
	case len(raw) >= 3 && raw[0] == 'B' && raw[1] == 'Z' && raw[2] == 'h':
		return io.ReadAll(bzip2.NewReader(bytes.NewReader(raw)))
	default:
		// уже распакован
		return raw, nil
	}
}

// Fetch скачивает и разбирает метаданные матча.
func Fetch(client *http.Client, cluster int, matchID int64, salt uint32) (*Metadata, error) {
	if client == nil {
		client = &http.Client{Timeout: 90 * time.Second}
	}
	resp, err := client.Get(URL(cluster, matchID, salt))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("метаданные матча %d: код %d", matchID, resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	plain, err := Decompress(raw)
	if err != nil {
		return nil, fmt.Errorf("распаковка: %w", err)
	}
	return Parse(plain)
}
