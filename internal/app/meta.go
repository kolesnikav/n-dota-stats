package app

import (
	"encoding/json"
	"errors"

	"github.com/kolesnikav/n-dota-stats/internal/gc"
	"github.com/kolesnikav/n-dota-stats/internal/meta"
	"github.com/kolesnikav/n-dota-stats/internal/store"
)

// metaTick разбирает очередь матчей: берёт ключ реплея в Game Coordinator,
// качает файл метаданных (десятки килобайт вместо десятков мегабайт у полного
// реплея) и дополняет уже отправленные сводки.
//
// Полный .dem нужен только для вардов, позиций и рун — он качается отдельно и
// по явному запросу, чтобы не тратить трафик и процессор на каждый матч.
func (a *App) metaTick() {
	if a.Salt == nil {
		return
	}
	ids, err := a.DB.QueuedMatches(3)
	if err != nil {
		a.Log("очередь разбора: %v", err)
		return
	}
	for _, id := range ids {
		a.processMeta(id)
	}
}

func (a *App) processMeta(matchID int64) {
	salt, err := a.Salt.ReplaySalt(matchID)
	if err != nil {
		if errors.Is(err, gc.ErrNotConfigured) {
			// без доступа к GC матч так и останется на уровне скорборда
			_ = a.DB.SetReplayState(matchID, store.ReplayFailed, "нет ключа реплея")
			return
		}
		a.Log("ключ реплея %d: %v", matchID, err)
		_ = a.DB.SetReplayState(matchID, store.ReplayFailed, err.Error())
		return
	}
	if !a.DB.TakeGCBudget(gcDailyLimit) {
		a.Log("суточный бюджет Game Coordinator исчерпан, матч %d подождёт", matchID)
		return
	}
	_ = a.DB.SetReplayState(matchID, store.ReplayFetching, "")

	md, err := meta.Fetch(nil, salt.Cluster, matchID, salt.Salt)
	if err != nil {
		a.Log("метаданные %d: %v", matchID, err)
		_ = a.DB.SetReplayState(matchID, store.ReplayFailed, err.Error())
		return
	}
	blob, err := json.Marshal(md)
	if err != nil {
		a.Log("сериализация метаданных %d: %v", matchID, err)
		return
	}
	if err := a.DB.SaveMeta(matchID, blob); err != nil {
		a.Log("сохранение метаданных %d: %v", matchID, err)
		return
	}
	_ = a.DB.SetReplayState(matchID, store.ReplayMeta, "")
	a.Log("матч %d: метаданные разобраны", matchID)
	a.Refresh(matchID)
}
