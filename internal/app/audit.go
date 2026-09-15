package app

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"

	"github.com/kolesnikav/n-dota-stats/internal/analysis"
	"github.com/kolesnikav/n-dota-stats/internal/dota"
)

// Проверка качества самих показателей.
//
// Смысл: показатель полезен, если он различает победы и поражения И при этом
// остаётся рычагом, а не следствием результата. Золото, опыт и урон по
// строениям почти всегда «сильно различают» — но растут они потому, что игра
// уже складывается, а не наоборот. Поэтому вывод делится на две части.

// Lever помечает показатели, которыми игрок управляет напрямую и которые
// измеримы до того, как исход определился.
var levers = map[string]bool{
	"lh10": true, "xp10": true, "lane_eff": true,
	"wards": true, "stacks": true, "runes": true, "stuns": true,
	"deaths": true, "boots": true, "tp": true, "buybacks": true,
	"teamfight": true, "kill_part": true, "neutrals": true,
}

// MetricAudit — итог проверки одного показателя на одной роли.
type MetricAudit struct {
	Key        string
	Label      string
	Lever      bool
	Wins       int
	Losses     int
	MeanWin    float64
	MeanLoss   float64
	Effect     float64 // разница в единицах разброса
	Coverage   int     // по скольким матчам показатель вообще есть
	TotalGames int
}

// Auditor считает качество показателей по накопленной истории.
type Auditor struct {
	App *App
}

type sample struct {
	role    dota.Role
	win     bool
	metrics map[string]float64
}

// Audit проходит по истории игрока и оценивает каждый показатель.
func (a *App) Audit(accountID int64, minGames int) (map[dota.Role][]MetricAudit, error) {
	rows, err := a.DB.HistoryRows(accountID)
	if err != nil {
		return nil, err
	}
	samples := make([]sample, 0, len(rows))
	for _, r := range rows {
		m := map[string]float64{}
		_ = json.Unmarshal([]byte(r.Metrics), &m)
		samples = append(samples, sample{role: dota.Role(r.Role), win: r.Win, metrics: m})
	}

	labels := map[string]string{}
	for _, m := range analysis.Registry {
		labels[m.Key] = m.Label
	}

	out := map[dota.Role][]MetricAudit{}
	for role := dota.RoleCarry; role <= dota.RoleHard; role++ {
		var roleSamples []sample
		for _, s := range samples {
			if s.role == role {
				roleSamples = append(roleSamples, s)
			}
		}
		if len(roleSamples) < minGames {
			continue
		}
		byKey := map[string][2][]float64{}
		for _, s := range roleSamples {
			for k, v := range s.metrics {
				cur := byKey[k]
				if s.win {
					cur[0] = append(cur[0], v)
				} else {
					cur[1] = append(cur[1], v)
				}
				byKey[k] = cur
			}
		}
		var list []MetricAudit
		for k, pair := range byKey {
			w, l := pair[0], pair[1]
			au := MetricAudit{
				Key: k, Label: labels[k], Lever: levers[k],
				Wins: len(w), Losses: len(l),
				Coverage: len(w) + len(l), TotalGames: len(roleSamples),
			}
			if len(w) >= 5 && len(l) >= 5 {
				au.MeanWin, au.MeanLoss = mean(w), mean(l)
				pooled := math.Sqrt((variance(w) + variance(l)) / 2)
				if pooled > 0 {
					au.Effect = (au.MeanWin - au.MeanLoss) / pooled
				}
			}
			list = append(list, au)
		}
		sort.Slice(list, func(i, j int) bool {
			if list[i].Lever != list[j].Lever {
				return list[i].Lever
			}
			return math.Abs(list[i].Effect) > math.Abs(list[j].Effect)
		})
		out[role] = list
	}
	return out, nil
}

func mean(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	var s float64
	for _, x := range xs {
		s += x
	}
	return s / float64(len(xs))
}

func variance(xs []float64) float64 {
	if len(xs) < 2 {
		return 0
	}
	m := mean(xs)
	var s float64
	for _, x := range xs {
		s += (x - m) * (x - m)
	}
	return s / float64(len(xs)-1)
}

// AuditText печатает отчёт в читаемом виде.
func AuditText(res map[dota.Role][]MetricAudit) string {
	out := "Проверка показателей по накопленной истории\n"
	out += "Рычаг — то, чем игрок управляет и что измеримо до исхода.\n"
	out += "Следствие — растёт само, когда игра складывается.\n"
	for role := dota.RoleCarry; role <= dota.RoleHard; role++ {
		list, ok := res[role]
		if !ok {
			continue
		}
		out += fmt.Sprintf("\n=== %s (матчей: %d) ===\n", role, list[0].TotalGames)
		for _, m := range list {
			kind := "следствие"
			if m.Lever {
				kind = "рычаг"
			}
			label := m.Label
			if label == "" {
				label = m.Key
			}
			if m.Coverage < 10 {
				out += fmt.Sprintf("  %-9s %-28s данных мало: %d из %d матчей\n",
					kind, label, m.Coverage, m.TotalGames)
				continue
			}
			verdict := "почти не различает"
			switch a := math.Abs(m.Effect); {
			case a >= 0.5:
				verdict = "различает сильно"
			case a >= 0.25:
				verdict = "различает слабо"
			}
			out += fmt.Sprintf("  %-9s %-28s побед %8.1f · поражений %8.1f · эффект %+5.2f · %s\n",
				kind, label, m.MeanWin, m.MeanLoss, m.Effect, verdict)
		}
	}
	return out
}
