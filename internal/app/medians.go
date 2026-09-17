package app

import (
	"fmt"
	"time"

	"github.com/kolesnikav/n-dota-stats/internal/analysis"
	"github.com/kolesnikav/n-dota-stats/internal/dota"
)

// Медианы по ролям берутся у OpenDota запросами к /api/explorer.
//
// Своей базы публичных матчей нет и не планируется, а explorer отдаёт агрегат
// по тысячам игр одним запросом. Раз в неделю — потому что медианы двигает
// патч, а не отдельные матчи: чаще незачем, реже — отстанет от метагейма.

// medianQuery — тот же запрос, что в docs/methodology.md. LIMIT обязателен:
// без него explorer упирается в таймаут.
const medianQuery = `
WITH s AS (
  SELECT gold_per_min g, xp_per_min x, tower_damage td, camps_stacked cs,
         obs_placed op, lh_t[11] lh10, xp_t[11] xp10, gold_t[11] g10
  FROM player_matches
  WHERE %s AND gold_t IS NOT NULL
  ORDER BY match_id DESC LIMIT 6000
)
SELECT count(*) n,
 round(percentile_cont(0.5) within group (order by g))    gpm,
 round(percentile_cont(0.5) within group (order by x))    xpm,
 round(percentile_cont(0.5) within group (order by lh10)) lh10,
 round(percentile_cont(0.5) within group (order by xp10)) xp10,
 round(percentile_cont(0.5) within group (order by g10))  g10,
 round(percentile_cont(0.5) within group (order by td))   tower,
 round(percentile_cont(0.5) within group (order by cs))   stacks,
 round(percentile_cont(0.5) within group (order by op))   obs
FROM s`

// medianGroups — как выделяется каждая роль. Роли в данных OpenDota нет, есть
// только линия, поэтому коры отсекаются от саппортов по вардам: без этого в
// выборку «оффлейна» попадают пятёрки, стоящие на той же линии, и медиана
// эффективности падает с 71% до 55%.
var medianGroups = []struct {
	Roles []dota.Role
	Where string
}{
	{[]dota.Role{dota.RoleCarry}, "lane_role = 1 AND obs_placed <= 2 AND last_hits > 120"},
	{[]dota.Role{dota.RoleMid}, "lane_role = 2 AND obs_placed <= 2"},
	{[]dota.Role{dota.RoleOfflane}, "lane_role = 3 AND obs_placed <= 2"},
	// Роум и хард в данных неразличимы: разделить их нечем, поэтому у обоих
	// одна и та же медиана саппорта.
	{[]dota.Role{dota.RoleRoamer, dota.RoleHard}, "obs_placed >= 5"},
}

// medianKeys — как столбцы ответа ложатся на ключи показателей.
var medianKeys = map[string]string{
	"gpm": "gpm", "xpm": "xpm", "lh10": "lh10", "xp10": "xp10",
	"tower": "tower_damage", "stacks": "stacks", "obs": "wards",
}

const medianInterval = 7 * 24 * time.Hour

// RefreshMedians пересчитывает медианы по ролям и сохраняет их.
func (a *App) RefreshMedians() error {
	out := map[dota.Role]map[string]float64{}
	for _, g := range medianGroups {
		rows, err := a.OD.Explorer(fmt.Sprintf(medianQuery, g.Where))
		if err != nil {
			return fmt.Errorf("медианы (%s): %w", g.Where, err)
		}
		if len(rows) == 0 {
			return fmt.Errorf("медианы (%s): пустой ответ", g.Where)
		}
		row := rows[0]
		if n := number(row["n"]); n < 500 {
			return fmt.Errorf("медианы (%s): слишком мало игр в выборке: %.0f", g.Where, n)
		}
		vals := map[string]float64{}
		for col, key := range medianKeys {
			if v, ok := row[col]; ok && v != nil {
				vals[key] = number(v)
			}
		}
		// Эффективность линии не хранится столбцом: это золото на 10:00,
		// делённое на стоимость полной линии крипов. Та же формула, что в
		// показателе, иначе медиана и значение считались бы по-разному.
		if g10 := number(row["g10"]); g10 > 0 {
			vals["lane_eff"] = float64(int(g10 / 4948.0 * 100))
		}
		for _, role := range g.Roles {
			out[role] = vals
		}
	}
	if err := a.DB.SaveRoleMedians(out); err != nil {
		return err
	}
	analysis.SetRoleMedians(out)
	a.Log("медианы ролей обновлены по %d группам", len(medianGroups))
	return nil
}

// LoadMedians поднимает сохранённые медианы при старте. Если их нет, остаётся
// таблица, вшитая в код.
func (a *App) LoadMedians() {
	saved, err := a.DB.RoleMedians()
	if err != nil || len(saved) == 0 {
		return
	}
	analysis.SetRoleMedians(saved)
}

// mediansStale — пора ли пересчитывать.
func (a *App) mediansStale() bool {
	at := a.DB.RoleMediansFetchedAt()
	return at == 0 || time.Since(time.Unix(at, 0)) > medianInterval
}

func number(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case int:
		return float64(x)
	case string:
		var f float64
		_, _ = fmt.Sscanf(x, "%g", &f)
		return f
	}
	return 0
}
