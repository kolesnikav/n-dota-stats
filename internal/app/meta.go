package app

import (
	"encoding/json"
	"errors"

	"github.com/kolesnikav/n-dota-stats/internal/gc"
	"github.com/kolesnikav/n-dota-stats/internal/meta"
	"github.com/kolesnikav/n-dota-stats/internal/replay"
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

// errBudget — суточный лимит обращений к Game Coordinator исчерпан.
var errBudget = errors.New("бюджет Game Coordinator исчерпан")

// replaySalt достаёт ключ реплея, обращаясь к Game Coordinator только если
// ключа ещё нет в базе.
//
// Ключ постоянный, а запросы к GC — единственный дефицит во всей цепочке:
// около сотни на аккаунт в сутки. Поэтому сначала база, и только потом GC —
// и бюджет списывается до запроса, а не после: иначе лимит защищал бы от
// скачивания реплея, а не от того, ради чего он заведён.
func (a *App) replaySalt(matchID int64) (gc.Salt, error) {
	if cluster, salt, ok := a.DB.ReplaySalt(matchID); ok {
		return gc.Salt{Cluster: cluster, Salt: salt}, nil
	}
	// Второй бесплатный источник: в ответе OpenDota ключ приходит вместе с
	// матчем. Раз он уже скачан и лежит в базе, обращаться к GC незачем.
	if m, err := a.LoadMatch(matchID, 0); err == nil && m.ReplaySalt > 0 {
		if err := a.DB.SaveReplaySalt(matchID, m.Cluster, m.ReplaySalt); err != nil {
			a.Log("сохранение ключа реплея %d: %v", matchID, err)
		}
		return gc.Salt{Cluster: m.Cluster, Salt: m.ReplaySalt}, nil
	}
	if !a.DB.TakeGCBudget(gcDailyLimit) {
		return gc.Salt{}, errBudget
	}
	salt, err := a.Salt.ReplaySalt(matchID)
	if err != nil {
		return gc.Salt{}, err
	}
	if err := a.DB.SaveReplaySalt(matchID, salt.Cluster, salt.Salt); err != nil {
		a.Log("сохранение ключа реплея %d: %v", matchID, err)
	}
	return salt, nil
}

func (a *App) processMeta(matchID int64) {
	salt, err := a.replaySalt(matchID)
	if err != nil {
		if errors.Is(err, errBudget) {
			a.Log("суточный бюджет Game Coordinator исчерпан, матч %d подождёт", matchID)
			return
		}
		if errors.Is(err, gc.ErrNotConfigured) {
			// без доступа к GC матч так и останется на уровне скорборда
			_ = a.DB.SetReplayState(matchID, store.ReplayFailed, "нет ключа реплея")
			return
		}
		a.Log("ключ реплея %d: %v", matchID, err)
		_ = a.DB.SetReplayState(matchID, store.ReplayFailed, err.Error())
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

	a.processReplay(matchID, salt)
}

// processReplay качает полный реплей и разбирает его своими силами: варды,
// поминутные кривые и смерти с координатами берутся отсюда, а не из чужого
// разбора. Файл весит десятки мегабайт, зато сам разбор занимает пару секунд.
func (a *App) processReplay(matchID int64, salt gc.Salt) {
	m, err := a.LoadMatch(matchID, 0)
	if err != nil {
		return
	}
	res, err := replay.Fetch(nil, salt.Cluster, matchID, salt.Salt, m)
	if err != nil {
		a.Log("реплей %d: %v", matchID, err)
		return
	}
	blob, err := json.Marshal(res)
	if err != nil {
		return
	}
	if err := a.DB.SaveReplay(matchID, blob); err != nil {
		a.Log("сохранение разбора %d: %v", matchID, err)
		return
	}
	_ = a.DB.SetReplayState(matchID, store.ReplayParsed, "")
	a.Log("матч %d: реплей разобран своими силами", matchID)
	a.Refresh(matchID)
}
