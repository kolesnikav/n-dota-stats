// Команда n-dota-stats — телеграм-бот с разбором матчей Dota 2.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/kolesnikav/n-dota-stats/internal/analysis"
	"github.com/kolesnikav/n-dota-stats/internal/app"
	"github.com/kolesnikav/n-dota-stats/internal/benchmarks"
	"github.com/kolesnikav/n-dota-stats/internal/dota"
	"github.com/kolesnikav/n-dota-stats/internal/gc"
	"github.com/kolesnikav/n-dota-stats/internal/meta"
	"github.com/kolesnikav/n-dota-stats/internal/odota"
	"github.com/kolesnikav/n-dota-stats/internal/store"
	"github.com/kolesnikav/n-dota-stats/internal/telegram"
	"github.com/kolesnikav/n-dota-stats/internal/valve"
)

func main() {
	var (
		dryRun   = flag.Bool("dry-run", false, "напечатать сводку по матчу и выйти, без телеграма")
		account  = flag.Int64("account", 0, "id аккаунта для --dry-run")
		match    = flag.Int64("match", 0, "id матча для --dry-run")
		dbPath   = flag.String("db", env("DB_PATH", "bot.db"), "путь к базе")
		metaPath = flag.String("meta", "", "файл метаданных матча для --dry-run")
		showMet  = flag.Bool("metrics", false, "напечатать набор показателей по ролям и выйти")
		backfill = flag.Int("backfill", 0, "загрузить историю матчей за N дней и выйти")
		audit    = flag.Bool("audit", false, "проверить качество показателей по истории и выйти")
		reparse  = flag.Int("reparse", 0, "попросить разобрать реплеи матчей за N дней и перечитать их")
		gcTest   = flag.Int64("gc-test", 0, "проверить цепочку: ключ реплея, метаданные, разбор — и выйти")
	)
	flag.Parse()

	if *showMet {
		printMetrics()
		return
	}

	db, err := store.Open(*dbPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "база:", err)
		os.Exit(1)
	}
	defer db.Close()

	od := odota.New(os.Getenv("OPENDOTA_API_KEY"))

	// Основной источник — Steam Web API. Без ключа откатываемся на OpenDota:
	// бот остаётся рабочим, просто данные идут через посредника.
	var source app.MatchSource = odota.NewSource(od)
	if key := os.Getenv("STEAM_API_KEY"); key != "" {
		source = valve.New(key)
	}

	if *gcTest > 0 {
		user, pass := os.Getenv("STEAM_BOT_USER"), os.Getenv("STEAM_BOT_PASS")
		gcClient := gc.NewSteam(user, pass, os.Getenv("STEAM_GUARD_CODE"),
			os.Getenv("STEAM_TWO_FACTOR_CODE"),
			func(f string, a ...any) { fmt.Printf(f+"\n", a...) })
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		if err := gcClient.Start(ctx, 90*time.Second); err != nil {
			fmt.Fprintln(os.Stderr, "Game Coordinator:", err)
			os.Exit(1)
		}
		defer gcClient.Close()

		salt, err := gcClient.ReplaySalt(*gcTest)
		if err != nil {
			fmt.Fprintln(os.Stderr, "ключ реплея:", err)
			os.Exit(1)
		}
		fmt.Printf("ключ получен: кластер %d, salt %d\n", salt.Cluster, salt.Salt)
		fmt.Println("адрес метаданных:", meta.URL(salt.Cluster, *gcTest, salt.Salt))

		md, err := meta.Fetch(nil, salt.Cluster, *gcTest, salt.Salt)
		if err != nil {
			fmt.Fprintln(os.Stderr, "метаданные:", err)
			os.Exit(1)
		}
		fmt.Printf("метаданные разобраны: матч %d, игроков %d\n", md.MatchID, len(md.Players))
		for _, p := range md.Players {
			fmt.Printf("  слот %-3d прокачка %2d шагов · уровни %2d · контроль %6.1f с · предметов %d\n",
				p.Slot, len(p.AbilityUpgrades), len(p.LevelUpTimes), p.Stuns, len(p.FirstItem))
		}
		return
	}

	if *reparse > 0 {
		if *account == 0 {
			fmt.Fprintln(os.Stderr, "нужен --account")
			os.Exit(1)
		}
		src := odota.NewSource(od)
		src.NoParseRequests = true
		a := app.New(db, nil, src, od)
		var chatID int64
		for _, u := range mustUsers(db) {
			if u.AccountID == *account {
				chatID = u.ChatID
			}
		}
		req, upd, err := a.Reparse(chatID, *account, *reparse, 4*time.Minute,
			func(f string, args ...any) { fmt.Printf(f+"\n", args...) })
		if err != nil {
			fmt.Fprintln(os.Stderr, "ошибка:", err)
			os.Exit(1)
		}
		fmt.Printf("запрошено разборов: %d, обновилось матчей: %d\n", req, upd)
		return
	}

	if *audit {
		if *account == 0 {
			fmt.Fprintln(os.Stderr, "нужен --account")
			os.Exit(1)
		}
		a := app.New(db, nil, odota.NewSource(od), od)
		res, err := a.Audit(*account, 15)
		if err != nil {
			fmt.Fprintln(os.Stderr, "ошибка:", err)
			os.Exit(1)
		}
		fmt.Print(app.AuditText(res))
		return
	}

	if *backfill > 0 {
		if *account == 0 {
			fmt.Fprintln(os.Stderr, "нужен --account")
			os.Exit(1)
		}
		src := odota.NewSource(od)
		src.NoParseRequests = true
		a := app.New(db, nil, src, od)
		var u store.User
		found := false
		for _, candidate := range mustUsers(db) {
			if candidate.AccountID == *account {
				u, found = candidate, true
			}
		}
		if !found {
			fmt.Fprintf(os.Stderr, "аккаунт %d не подключён к боту\n", *account)
			os.Exit(1)
		}
		res, err := a.Backfill(u.ChatID, *account, *backfill, func(done, total int) {
			fmt.Printf("  %d из %d\n", done, total)
		})
		if err != nil {
			fmt.Fprintln(os.Stderr, "ошибка:", err)
			os.Exit(1)
		}
		fmt.Println()
		fmt.Println(strip(res.Text()))
		fmt.Printf("заняло: %s\n", res.Elapsed.Round(time.Second))
		return
	}

	if *dryRun {
		if err := dry(db, source, od, *account, *match, *metaPath); err != nil {
			fmt.Fprintln(os.Stderr, "ошибка:", err)
			os.Exit(1)
		}
		return
	}

	token := strings.TrimSpace(os.Getenv("TELEGRAM_TOKEN"))
	if token == "" {
		fmt.Fprintln(os.Stderr, "не задан TELEGRAM_TOKEN")
		os.Exit(1)
	}
	a := app.New(db, telegram.New(token), source, od)

	// Живая сессия Game Coordinator: только она отдаёт ключ реплея. Без неё
	// бот работает, просто разбор матчей ограничен тем, что успела разобрать
	// сторонняя очередь.
	if user := os.Getenv("STEAM_BOT_USER"); user != "" {
		gcClient := gc.NewSteam(user, os.Getenv("STEAM_BOT_PASS"),
			os.Getenv("STEAM_GUARD_CODE"), os.Getenv("STEAM_TWO_FACTOR_CODE"),
			func(f string, args ...any) { fmt.Fprintf(os.Stderr, f+"\n", args...) })
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		if err := gcClient.Start(ctx, 90*time.Second); err != nil {
			fmt.Fprintln(os.Stderr, "Game Coordinator недоступен:", err)
			fmt.Fprintln(os.Stderr, "бот продолжит работу без своего разбора реплеев")
		} else {
			a.Salt = gcClient
			defer gcClient.Close()
		}
	}

	if specs := os.Getenv("DOTA_MANUAL_SALTS"); specs != "" {
		manual, err := gc.NewManual(strings.Split(specs, ","))
		if err != nil {
			fmt.Fprintln(os.Stderr, "ключи реплеев:", err)
			os.Exit(1)
		}
		a.Salt = manual
	}
	if err := a.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "бот остановлен:", err)
		os.Exit(1)
	}
}

// printMetrics печатает реестр показателей: что показывается для каждой роли,
// откуда берутся данные и с чем сравнивается значение.
func mustUsers(db *store.DB) []store.User {
	users, err := db.Users()
	if err != nil {
		fmt.Fprintln(os.Stderr, "пользователи:", err)
		os.Exit(1)
	}
	return users
}

func printMetrics() {
	needs := map[dota.Detail]string{
		dota.DetailScoreboard: "таблица",
		dota.DetailMeta:       "метаданные",
		dota.DetailReplay:     "реплей",
	}
	compare := map[analysis.CompareKind]string{
		analysis.CompareHeroPercentile: "перцентиль героя",
		analysis.CompareRoleMedian:     "медиана роли",
		analysis.CompareOwnHistory:     "своя история",
		analysis.CompareLaneOpponent:   "соперник по линии",
	}
	for role := dota.RoleCarry; role <= dota.RoleHard; role++ {
		metrics := analysis.MetricsFor(role, 0, false)
		fmt.Printf("\n%d · %s — показателей: %d\n", int(role), role, len(metrics))
		for _, g := range append(append([]string{}, analysis.Groups...), "") {
			for _, m := range metrics {
				if m.Group != g {
					continue
				}
				var bases []string
				for _, k := range []analysis.CompareKind{
					analysis.CompareRoleMedian, analysis.CompareOwnHistory,
					analysis.CompareLaneOpponent, analysis.CompareHeroPercentile,
				} {
					for _, have := range m.Compare {
						if have == k {
							bases = append(bases, compare[k])
						}
					}
				}
				base := "—"
				if len(bases) > 0 {
					base = strings.Join(bases, ", ")
				}
				group := m.Group
				if group == "" {
					group = "прочее"
				}
				fmt.Printf("   %-9s %-32s %-11s %s\n", group, m.Label, needs[m.Needs], base)
			}
		}
	}
	scopes := map[analysis.Scope]string{
		analysis.ScopeRole:     "по роли",
		analysis.ScopeHero:     "по герою",
		analysis.ScopeHeroRole: "по герою в роли",
	}
	fmt.Printf("\nПоказатели под конкретных героев\n")
	for _, m := range analysis.Registry {
		if !m.HeroSpecific() {
			continue
		}
		heroes := make([]string, 0, len(m.Heroes))
		for _, id := range m.Heroes {
			heroes = append(heroes, dota.HeroName(id))
		}
		fmt.Printf("   %-32s %-11s %-16s %s\n", m.Label, needs[m.Needs],
			scopes[m.EffectiveScope()], strings.Join(heroes, ", "))
	}

	fmt.Printf("\nПоказатели, которые сравниваются не по роли\n")
	for _, m := range analysis.Registry {
		if m.HeroSpecific() || m.EffectiveScope() == analysis.ScopeRole {
			continue
		}
		fmt.Printf("   %-32s %s\n", m.Label, scopes[m.EffectiveScope()])
	}

	fmt.Printf("\nВсего показателей в реестре: %d\n", len(analysis.Registry))
}

func dry(db *store.DB, source app.MatchSource, od *odota.Client, account, matchID int64, metaPath string) error {
	if account == 0 || matchID == 0 {
		return fmt.Errorf("нужны --account и --match")
	}
	a := app.New(db, nil, source, od)
	// Тем же путём, что и бот: сохранённый скорборд, затем метаданные и
	// реплей из базы. Иначе сухой прогон показывает не то, что видит
	// пользователь, и проверять им нечего.
	m, err := a.LoadMatch(matchID, account)
	if err != nil {
		return err
	}
	if metaPath != "" {
		blob, err := os.ReadFile(metaPath)
		if err != nil {
			return fmt.Errorf("метаданные: %w", err)
		}
		plain, err := meta.Decompress(blob)
		if err != nil {
			return fmt.Errorf("распаковка метаданных: %w", err)
		}
		md, err := meta.Parse(plain)
		if err != nil {
			return fmt.Errorf("разбор метаданных: %w", err)
		}
		fmt.Printf("метаданные применены к %d игрокам\n\n", md.Apply(m))
		// Метаданные из файла меняют показатели, от которых зависят
		// перцентили и раскладка ролей, — пересчитываем.
		benchmarks.Apply(db, m)
		analysis.DetectRoles(m, db.RoleHint)
	}
	rep, err := a.Build(m, account)
	if err != nil {
		return err
	}
	fmt.Println(strip(rep.Text()))
	fmt.Println()
	fmt.Println("Полный рейтинг:")
	for i, s := range rep.Ranked {
		fmt.Printf("%2d. %-16s %-7s %s  %.3f\n", i+1, s.Player.Name(),
			s.Player.SideName(), s.Player.Role, s.Score)
	}
	return nil
}

func strip(s string) string {
	r := strings.NewReplacer("<b>", "", "</b>", "", "<i>", "", "</i>", "",
		"<code>", "", "</code>", "", "&amp;", "&", "&lt;", "<", "&gt;", ">", "&#39;", "'", "&#34;", `"`)
	return r.Replace(s)
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
