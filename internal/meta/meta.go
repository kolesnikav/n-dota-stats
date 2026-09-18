// Package meta разбирает файл метаданных матча.
//
// Valve выкладывает его рядом с реплеем по тому же адресу, но с расширением
// .meta.bz2 (внутри, несмотря на имя, zstd). Размер — десятки килобайт против
// десятков мегабайт у самого реплея, а внутри лежит многое из того, ради чего
// обычно парсят реплей: порядок прокачки, снимки инвентаря, время получения
// уровней, секунды контроля — и, главное, **лучший игрок матча с двумя
// кандидатами**, тот самый список, который Dota показывает после игры.
//
// Разметку полей больше не восстанавливаем вручную: описания сообщений есть в
// manta (dota_match_metadata.proto), и по ним файл разбирается целиком. Первая
// версия читала четыре поля, угаданных по дампу, и из-за этого мимо проходило
// всё остальное — включая список лучших, который лежал в девятом поле.
package meta

import (
	"errors"
	"fmt"
	"strings"

	mdota "github.com/dotabuff/manta/dota"
	"google.golang.org/protobuf/proto"

	"github.com/kolesnikav/n-dota-stats/internal/dota"
)

// Player — то, что удалось достать из метаданных по одному игроку.
type Player struct {
	Slot            int
	TeamID          int
	AbilityUpgrades []int
	LevelUpTimes    []int
	Stuns           float64
	FirstItem       map[int]int // item id -> секунда первого появления

	// Оценки самой Valve. Это не общая шкала, а сырые величины разной
	// природы: бой — доля от нуля до единицы, фарм похож на золото в минуту,
	// поддержка и пуш — накопленные суммы. Складывать их бессмысленно.
	FightScore   float64
	FarmScore    float64
	SupportScore float64
	PushScore    float64
}

// MVP — один из трёх, кого Dota показала после матча.
type MVP struct {
	Slot int
	// Accolades — за что отмечен: «серия убийств», «золото на поддержку»,
	// геройское достижение вроде проклятия трёх героев у Winter Wyvern.
	Accolades []string
}

// Metadata — разобранный файл.
type Metadata struct {
	MatchID int64
	Version int
	Players []Player
	// MVP — лучший игрок и два кандидата, в том порядке, в каком их
	// показывает Dota. Проверено на семи матчах, размеченных вручную:
	// шесть совпали полностью, седьмой — тот, где разметка делалась по
	// памяти и сам игрок в ней сомневался.
	MVP []MVP
}

// Apply переносит данные метаданных в матч и поднимает уровень детализации.
func (md *Metadata) Apply(m *dota.Match) int {
	// Список лучших кладём отдельно от игроков: он про матч целиком, и его
	// не должно потерять, даже если ни один игрок не сопоставится.
	if len(md.MVP) > 0 {
		m.MVP = m.MVP[:0]
		for _, e := range md.MVP {
			m.MVP = append(m.MVP, e.Slot)
		}
	}
	bySlot := map[int]Player{}
	for _, p := range md.Players {
		bySlot[p.Slot] = p
	}
	applied := 0
	for _, p := range m.Players {
		mp, ok := bySlot[p.Slot]
		if !ok {
			continue
		}
		if len(mp.AbilityUpgrades) > 0 {
			p.AbilityUpgrades = mp.AbilityUpgrades
		}
		if len(mp.LevelUpTimes) > 0 {
			p.LevelUpTimes = mp.LevelUpTimes
		}
		if mp.Stuns > 0 {
			p.Stuns = mp.Stuns
		}
		if len(mp.FirstItem) > 0 {
			p.FirstItem = mp.FirstItem
			if p.FirstPurchase == nil {
				p.FirstPurchase = map[string]int{}
			}
			for id, at := range mp.FirstItem {
				if name := dota.ItemName(id); name != "" {
					if prev, ok := p.FirstPurchase[name]; !ok || at < prev {
						p.FirstPurchase[name] = at
					}
				}
			}
		}
		applied++
	}
	if applied > 0 && m.Detail < dota.DetailMeta {
		m.Detail = dota.DetailMeta
	}
	return applied
}

// Parse разбирает файл метаданных.
func Parse(raw []byte) (*Metadata, error) {
	var file mdota.CDOTAMatchMetadataFile
	if err := proto.Unmarshal(raw, &file); err != nil {
		return nil, fmt.Errorf("метаданные: %w", err)
	}
	md := file.GetMetadata()
	if md == nil {
		return nil, errors.New("метаданные пусты")
	}
	out := &Metadata{MatchID: int64(file.GetMatchId()), Version: int(file.GetVersion())}

	for _, team := range md.GetTeams() {
		for _, p := range team.GetPlayers() {
			player := Player{
				Slot:         int(p.GetPlayerSlot()),
				TeamID:       int(team.GetDotaTeam()),
				Stuns:        float64(p.GetStunDuration()),
				FightScore:   float64(p.GetFightScore()),
				FarmScore:    float64(p.GetFarmScore()),
				SupportScore: float64(p.GetSupportScore()),
				PushScore:    float64(p.GetPushScore()),
			}
			for _, a := range p.GetAbilityUpgrades() {
				player.AbilityUpgrades = append(player.AbilityUpgrades, int(a))
			}
			for _, t := range p.GetLevelUpTimes() {
				player.LevelUpTimes = append(player.LevelUpTimes, int(t))
			}
			// Снимки инвентаря идут каждые полминуты. Первое появление
			// предмета — это и есть время покупки с точностью до снимка.
			player.FirstItem = map[int]int{}
			for _, snap := range p.GetInventorySnapshot() {
				at := int(snap.GetGameTime())
				for _, item := range snap.GetItemId() {
					if prev, ok := player.FirstItem[int(item)]; !ok || at < prev {
						player.FirstItem[int(item)] = at
					}
				}
			}
			out.Players = append(out.Players, player)
		}
	}

	for _, m := range md.GetMvpData().GetMvps() {
		entry := MVP{Slot: int(m.GetPlayerSlot())}
		for _, a := range m.GetAccolades() {
			entry.Accolades = append(entry.Accolades, accoladeName(a.GetType()))
		}
		out.MVP = append(out.MVP, entry)
	}
	return out, nil
}

// accoladeName убирает приставки из имени награды: в перечислении они
// называются kKillEaterEventType_Pudge_EnemyHeroesHooked, а читать это
// приходится человеку.
func accoladeName(t mdota.CMvpData_MvpDatum_MvpAccolade_MvpAccoladeType) string {
	name := t.String()
	for _, prefix := range []string{
		"CMvpData_MvpDatum_MvpAccolade_",
		"kKillEaterEventType_",
		"kKillEaterEvent_",
	} {
		name = strings.TrimPrefix(name, prefix)
	}
	return name
}
