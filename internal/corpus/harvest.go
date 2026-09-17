package corpus

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/kolesnikav/n-dota-stats/internal/dota"
)

// Source — поток публичных матчей. Реализуется клиентом Steam Web API:
// GetMatchHistoryBySequenceNum отдаёт сто матчей за запрос со всеми полями
// скорборда. Это единственный доступный способ набрать корпус самим —
// по одному матчу за запрос сто тысяч игр не собрать.
type Source interface {
	MatchesBySeq(start int64, count int) ([]*dota.Match, int64, error)
	MatchSeqNum(matchID int64) (int64, error)
}

// KV — место под курсор обхода.
type KV interface {
	Get(key string) string
	Put(key, value string) error
}

const cursorKey = "corpus_seq"

// Lookback — на сколько номеров отступаем назад от свежего матча при первом
// запуске. В Dota примерно миллион матчей в сутки, так что 300 000 — это
// около семи часов игрового потока: достаточно свежо, чтобы отражать текущий
// патч, и достаточно много, чтобы редкие герои успели набраться.
const Lookback = 300000

// Harvester наполняет корпус публичными матчами.
type Harvester struct {
	Src    Source
	DB     Store
	KV     KV
	Corpus *Corpus
	Log    func(string, ...any)
	// Pause — задержка между запросами. У Valve лимит 100 000 запросов в
	// сутки, то есть чуть больше одного в секунду; секунда держит нас внутри
	// с запасом и не мешает остальным вызовам того же ключа.
	Pause time.Duration
	// SeedMatch — матч, от которого отсчитываем начало обхода при первом
	// запуске. Обычно самый свежий известный нам матч.
	SeedMatch int64
}

func (h *Harvester) log(format string, args ...any) {
	if h.Log != nil {
		h.Log(format, args...)
	}
}

// cursor возвращает номер, с которого продолжать обход, поднимая его при
// первом запуске от свежего матча.
func (h *Harvester) cursor() (int64, error) {
	if v := h.KV.Get(cursorKey); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			return n, nil
		}
	}
	if h.SeedMatch == 0 {
		return 0, fmt.Errorf("не от чего начать обход: нет ни курсора, ни матча-ориентира")
	}
	seq, err := h.Src.MatchSeqNum(h.SeedMatch)
	if err != nil {
		return 0, fmt.Errorf("номер последовательности матча %d: %w", h.SeedMatch, err)
	}
	start := seq - Lookback
	if start < 1 {
		start = 1
	}
	h.log("корпус: начинаю обход с номера %d (матч %d минус %d)", start, h.SeedMatch, Lookback)
	return start, nil
}

// Stats — итог одного прохода.
type Stats struct {
	Requests int
	Scanned  int // сколько матчей пришло
	Added    int // сколько попало в корпус
	Head     bool
}

// Run делает не больше requests запросов, добавляя матчи в корпус.
// Возвращается раньше, если поток кончился (дошли до «головы») или отменён ctx.
func (h *Harvester) Run(ctx context.Context, requests int) (Stats, error) {
	var st Stats
	seq, err := h.cursor()
	if err != nil {
		return st, err
	}
	pause := h.Pause
	if pause <= 0 {
		pause = time.Second
	}
	for i := 0; i < requests; i++ {
		if i > 0 {
			select {
			case <-ctx.Done():
				return st, h.finish(seq, st)
			case <-time.After(pause):
			}
		}
		matches, next, err := h.Src.MatchesBySeq(seq, 100)
		st.Requests++
		if err != nil {
			_ = h.finish(seq, st)
			return st, err
		}
		if len(matches) == 0 || next == seq {
			// Дошли до конца потока: свежее пока нет.
			st.Head = true
			return st, h.finish(seq, st)
		}
		for _, m := range matches {
			st.Scanned++
			if h.DB.CorpusHasMatch(m.ID) {
				continue
			}
			if !h.Corpus.AddMatch(m) {
				continue
			}
			if err := h.DB.CorpusAddMatch(m.ID); err != nil {
				return st, err
			}
			st.Added++
		}
		seq = next
	}
	return st, h.finish(seq, st)
}

// finish сохраняет курсор и выборки. Курсор двигаем только вместе с записью
// выборок: иначе обрыв посередине потерял бы матчи, которые уже «пройдены».
func (h *Harvester) finish(seq int64, st Stats) error {
	if err := h.Corpus.Flush(h.DB); err != nil {
		return err
	}
	return h.KV.Put(cursorKey, strconv.FormatInt(seq, 10))
}
