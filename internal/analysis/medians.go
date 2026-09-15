package analysis

import "github.com/kolesnikav/n-dota-stats/internal/dota"

// Медианы по ролям. Посчитаны запросами к /api/explorer OpenDota по последним
// 6000 разобранным матчам на каждую группу, сентябрь 2026. SQL и разбивка —
// в docs/methodology.md.
//
// Роли в данных OpenDota нет, есть только линия, поэтому группы выделены так:
//
//	керри    lane_role = 1 AND obs_placed <= 2 AND last_hits > 120
//	мид      lane_role = 2 AND obs_placed <= 2
//	оффлейн  lane_role = 3 AND obs_placed <= 2
//	саппорт  obs_placed >= 5   (роум и хард вместе — разделить их в данных нечем)
//
// Отсюда же важная поправка: если не отсекать саппортов по вардам, медиана
// «оффлейна» падает с 71% до 55% lane efficiency — в выборку попадают пятёрки,
// стоящие на той же линии.
var roleMedians = map[dota.Role]map[string]float64{
	dota.RoleCarry: {
		"gpm": 683, "xpm": 814, "lh10": 64, "lane_eff": 81, "tower_damage": 4504, "stacks": 2,
	},
	dota.RoleMid: {
		"gpm": 588, "xpm": 763, "lh10": 59, "xp10": 4578, "lane_eff": 81, "tower_damage": 1048, "stacks": 1,
	},
	dota.RoleOfflane: {
		"gpm": 534, "xpm": 663, "lh10": 51, "lane_eff": 71, "tower_damage": 690, "stacks": 1,
	},
	dota.RoleRoamer: {
		"gpm": 330, "xpm": 475, "lane_eff": 37, "stacks": 2, "wards": 9,
	},
	dota.RoleHard: {
		"gpm": 330, "xpm": 475, "lane_eff": 37, "stacks": 2, "wards": 9,
	},
}

// RoleMedian возвращает медиану показателя для роли.
func RoleMedian(role dota.Role, key string) (float64, bool) {
	m, ok := roleMedians[role]
	if !ok {
		return 0, false
	}
	v, ok := m[key]
	return v, ok
}
