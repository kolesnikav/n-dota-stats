package analysis

import (
	"sort"

	"github.com/kolesnikav/n-dota-stats/internal/dota"
)

// RoleHint — запомненная поправка пользователя: на этом герое и этой линии
// он играет вот эту роль.
type RoleHint struct {
	AccountID int64
	HeroID    int
	Lane      int
	Role      dota.Role
}

// HintLookup ищет поправку. Возвращает роль и признак попадания.
type HintLookup func(accountID int64, heroID, lane int) (dota.Role, bool)

// DetectRoles проставляет роли всем игрокам матча.
//
// Назначенной роли нет ни в API, ни в реплее — Valve её не отдаёт. Поэтому
// роль выводится из поведения:
//
//  1. поправка пользователя, если она есть;
//  2. саппорты — двое беднейших в команде, но игрок с шестью и более вардами
//     считается саппортом принудительно, а игрок со 150+ добиваниями — кором;
//  3. среди коров: линия 2 — мид, линия 3 — оффлейн, оставшийся — керри;
//  4. среди саппортов: тот, чья линия совпала с линией керри, — хард (поз. 5),
//     второй — роум (поз. 4).
//
// Шаг 2 важнее, чем кажется: OpenDota регулярно ставит саппорту линию мида, и
// без проверки по нетворсу роли разъезжаются.
func DetectRoles(m *dota.Match, hint HintLookup) {
	for _, radiant := range []bool{true, false} {
		assignTeam(m.Team(radiant), hint)
	}
}

func assignTeam(team []*dota.Player, hint HintLookup) {
	if len(team) == 0 {
		return
	}
	for _, p := range team {
		p.Role = dota.RoleUnknown
		p.RoleSource = dota.SourceAuto
	}

	// 1. поправки пользователя
	remaining := make([]*dota.Player, 0, len(team))
	taken := map[dota.Role]bool{}
	for _, p := range team {
		if hint != nil && p.AccountID != 0 {
			if role, ok := hint(p.AccountID, p.HeroID, p.Lane); ok && role.Valid() && !taken[role] {
				p.Role = role
				p.RoleSource = dota.SourceManual
				taken[role] = true
				continue
			}
		}
		remaining = append(remaining, p)
	}

	// 2. деление на коров и саппортов
	var cores, sups []*dota.Player
	byWorth := append([]*dota.Player(nil), remaining...)
	sort.SliceStable(byWorth, func(i, j int) bool { return byWorth[i].NetWorth < byWorth[j].NetWorth })

	supQuota := 2
	for r := dota.RoleRoamer; r <= dota.RoleHard; r++ {
		if taken[r] {
			supQuota--
		}
	}
	for i, p := range byWorth {
		forcedSup := p.ObsPlaced+p.SenPlaced >= 6
		forcedCore := p.LastHits >= 150
		switch {
		case forcedCore && !forcedSup:
			cores = append(cores, p)
		case forcedSup:
			sups = append(sups, p)
		case i < supQuota:
			sups = append(sups, p)
		default:
			cores = append(cores, p)
		}
	}
	// если эвристика перекосила, выравниваем по нетворсу
	for len(sups) > supQuota && supQuota >= 0 {
		sort.SliceStable(sups, func(i, j int) bool { return sups[i].NetWorth > sups[j].NetWorth })
		cores = append(cores, sups[0])
		sups = sups[1:]
	}

	// 3. коры по линиям
	var carry *dota.Player
	rest := make([]*dota.Player, 0, len(cores))
	for _, p := range cores {
		switch {
		case p.LaneRole == 2 && !taken[dota.RoleMid]:
			p.Role = dota.RoleMid
			taken[dota.RoleMid] = true
		case p.LaneRole == 3 && !taken[dota.RoleOfflane]:
			p.Role = dota.RoleOfflane
			taken[dota.RoleOfflane] = true
		default:
			rest = append(rest, p)
		}
	}
	sort.SliceStable(rest, func(i, j int) bool { return rest[i].NetWorth > rest[j].NetWorth })
	for _, p := range rest {
		switch {
		case !taken[dota.RoleCarry]:
			p.Role = dota.RoleCarry
			taken[dota.RoleCarry] = true
			carry = p
		case !taken[dota.RoleMid]:
			p.Role = dota.RoleMid
			taken[dota.RoleMid] = true
		case !taken[dota.RoleOfflane]:
			p.Role = dota.RoleOfflane
			taken[dota.RoleOfflane] = true
		}
	}
	if carry == nil {
		for _, p := range team {
			if p.Role == dota.RoleCarry {
				carry = p
			}
		}
	}

	// 4. саппорты: кто стоял с керри — хард, второй — роум
	sort.SliceStable(sups, func(i, j int) bool {
		li, lj := 0, 0
		if carry != nil {
			if sups[i].Lane == carry.Lane {
				li = 1
			}
			if sups[j].Lane == carry.Lane {
				lj = 1
			}
		}
		if li != lj {
			return li > lj
		}
		return sups[i].NetWorth < sups[j].NetWorth
	})
	for _, p := range sups {
		switch {
		case !taken[dota.RoleHard]:
			p.Role = dota.RoleHard
			taken[dota.RoleHard] = true
		case !taken[dota.RoleRoamer]:
			p.Role = dota.RoleRoamer
			taken[dota.RoleRoamer] = true
		}
	}

	// на всякий случай раздаём оставшиеся роли
	for _, p := range team {
		if p.Role != dota.RoleUnknown {
			continue
		}
		for r := dota.RoleCarry; r <= dota.RoleHard; r++ {
			if !taken[r] {
				p.Role = r
				taken[r] = true
				break
			}
		}
	}
}
