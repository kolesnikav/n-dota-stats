// Package meta разбирает файл метаданных матча.
//
// Valve выкладывает его рядом с реплеем по тому же адресу, но с расширением
// .meta.bz2 (внутри, несмотря на имя, zstd). Размер — десятки килобайт против
// десятков мегабайт у самого реплея, а внутри уже лежит многое из того, ради
// чего обычно парсят реплей: порядок прокачки, снимки инвентаря каждые 30
// секунд, время получения уровней, секунды контроля.
//
// Разметка полей восстановлена по реальному файлу матча 8999344582 и
// проверена тестом: секунды контроля Мираны совпали с тем, что показывает
// OpenDota (70.7336), а порядок прокачки — с её ability_upgrades_arr.
//
// Формат:
//
//	CDOTAMatchMetadataFile { 1 version, 2 match_id, 3 metadata, 5 подпись }
//	CDOTAMatchMetadata     { 1 repeated Team }
//	Team                   { 1 team_id, 2 repeated Player }
//	Player                 { 2 repeated ability_id, 3 player_slot,
//	                         22 repeated level_up_time, 24 repeated Snapshot,
//	                         45 stuns (float) }
//	Snapshot               { 1 repeated item_id, 2 time }
package meta

import (
	"errors"
	"fmt"
	"math"

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
}

// Metadata — разобранный файл.
type Metadata struct {
	MatchID int64
	Version int
	Players []Player
}

var errShort = errors.New("метаданные обрываются")

type reader struct {
	b []byte
	i int
}

func (r *reader) eof() bool { return r.i >= len(r.b) }

func (r *reader) varint() (uint64, error) {
	var res uint64
	var shift uint
	for {
		if r.eof() {
			return 0, errShort
		}
		b := r.b[r.i]
		r.i++
		res |= uint64(b&0x7f) << shift
		if b&0x80 == 0 {
			return res, nil
		}
		shift += 7
		if shift > 63 {
			return 0, fmt.Errorf("слишком длинный varint")
		}
	}
}

type field struct {
	num  int
	wire int
	val  uint64 // для wire 0
	buf  []byte // для wire 2
	f32  float32
}

func fields(b []byte) ([]field, error) {
	r := &reader{b: b}
	var out []field
	for !r.eof() {
		key, err := r.varint()
		if err != nil {
			return nil, err
		}
		f := field{num: int(key >> 3), wire: int(key & 7)}
		if f.num == 0 {
			return nil, fmt.Errorf("нулевой номер поля")
		}
		switch f.wire {
		case 0:
			if f.val, err = r.varint(); err != nil {
				return nil, err
			}
		case 1:
			if r.i+8 > len(r.b) {
				return nil, errShort
			}
			r.i += 8
		case 2:
			n, err := r.varint()
			if err != nil {
				return nil, err
			}
			if r.i+int(n) > len(r.b) {
				return nil, errShort
			}
			f.buf = r.b[r.i : r.i+int(n)]
			r.i += int(n)
		case 5:
			if r.i+4 > len(r.b) {
				return nil, errShort
			}
			bits := uint32(r.b[r.i]) | uint32(r.b[r.i+1])<<8 |
				uint32(r.b[r.i+2])<<16 | uint32(r.b[r.i+3])<<24
			f.f32 = math.Float32frombits(bits)
			r.i += 4
		default:
			return nil, fmt.Errorf("неизвестный тип поля %d", f.wire)
		}
		out = append(out, f)
	}
	return out, nil
}

// Parse разбирает распакованный файл метаданных.
func Parse(raw []byte) (*Metadata, error) {
	top, err := fields(raw)
	if err != nil {
		return nil, fmt.Errorf("верхний уровень: %w", err)
	}
	md := &Metadata{}
	var body []byte
	for _, f := range top {
		switch {
		case f.num == 1 && f.wire == 0:
			md.Version = int(f.val)
		case f.num == 2 && f.wire == 0:
			md.MatchID = int64(f.val)
		case f.num == 3 && f.wire == 2:
			body = f.buf
		}
	}
	if body == nil {
		return nil, fmt.Errorf("в файле нет блока метаданных")
	}
	inner, err := fields(body)
	if err != nil {
		return nil, fmt.Errorf("блок метаданных: %w", err)
	}
	for _, teamField := range inner {
		if teamField.num != 1 || teamField.wire != 2 || len(teamField.buf) < 32 {
			continue
		}
		teamFields, err := fields(teamField.buf)
		if err != nil {
			continue
		}
		teamID := 0
		for _, tf := range teamFields {
			if tf.num == 1 && tf.wire == 0 {
				teamID = int(tf.val)
			}
		}
		for _, pf := range teamFields {
			if pf.num != 2 || pf.wire != 2 {
				continue
			}
			p, err := parsePlayer(pf.buf)
			if err != nil {
				continue
			}
			p.TeamID = teamID
			md.Players = append(md.Players, p)
		}
	}
	if len(md.Players) == 0 {
		return nil, fmt.Errorf("в метаданных нет игроков")
	}
	return md, nil
}

func parsePlayer(buf []byte) (Player, error) {
	fs, err := fields(buf)
	if err != nil {
		return Player{}, err
	}
	p := Player{FirstItem: map[int]int{}}
	for _, f := range fs {
		switch {
		case f.num == 2 && f.wire == 0:
			p.AbilityUpgrades = append(p.AbilityUpgrades, int(f.val))
		case f.num == 3 && f.wire == 0:
			p.Slot = int(f.val)
		case f.num == 22 && f.wire == 0:
			p.LevelUpTimes = append(p.LevelUpTimes, int(f.val))
		case f.num == 45 && f.wire == 5:
			p.Stuns = float64(f.f32)
		case f.num == 24 && f.wire == 2:
			snapItems, at, err := parseSnapshot(f.buf)
			if err != nil {
				continue
			}
			for _, item := range snapItems {
				if prev, ok := p.FirstItem[item]; !ok || at < prev {
					p.FirstItem[item] = at
				}
			}
		}
	}
	return p, nil
}

// parseSnapshot читает снимок инвентаря: предметы и момент времени.
func parseSnapshot(buf []byte) (items []int, at int, err error) {
	fs, err := fields(buf)
	if err != nil {
		return nil, 0, err
	}
	for _, f := range fs {
		switch {
		case f.num == 1 && f.wire == 0:
			items = append(items, int(f.val))
		case f.num == 2 && f.wire == 0:
			at = int(f.val)
		}
	}
	return items, at, nil
}

// Apply переносит данные метаданных в матч и поднимает уровень детализации.
func (md *Metadata) Apply(m *dota.Match) int {
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
