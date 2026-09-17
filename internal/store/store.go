// Package store — хранилище на SQLite (modernc, чистый Go, без cgo).
package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/kolesnikav/n-dota-stats/internal/corpus"
	"github.com/kolesnikav/n-dota-stats/internal/dota"

	_ "modernc.org/sqlite"
)

// Status — состояние пользователя.
type Status string

const (
	StatusPending  Status = "pending"
	StatusVerified Status = "verified"
	StatusBlocked  Status = "blocked"
	StatusAdmin    Status = "admin"
)

// Verified сообщает, можно ли ради этого пользователя тратить лимит GC.
func (s Status) Verified() bool { return s == StatusVerified || s == StatusAdmin }

// ReplayState — состояние разбора матча.
type ReplayState string

const (
	ReplayNone     ReplayState = "none"
	ReplayQueued   ReplayState = "queued"
	ReplayFetching ReplayState = "fetching"
	ReplayMeta     ReplayState = "meta"
	ReplayParsed   ReplayState = "parsed"
	ReplayFailed   ReplayState = "failed"
	ReplaySkipped  ReplayState = "skipped_unverified"
)

const schema = `
CREATE TABLE IF NOT EXISTS kv (key TEXT PRIMARY KEY, value TEXT);

CREATE TABLE IF NOT EXISTS users (
    chat_id     INTEGER PRIMARY KEY,
    account_id  INTEGER NOT NULL,
    nickname    TEXT,
    status      TEXT NOT NULL DEFAULT 'pending',
    changed_at  INTEGER,
    changed_by  INTEGER,
    main_role   INTEGER DEFAULT 0,
    watch       INTEGER DEFAULT 1,
    created_at  INTEGER,
    last_poll   INTEGER DEFAULT 0
);

CREATE TABLE IF NOT EXISTS matches (
    match_id     INTEGER PRIMARY KEY,
    start_time   INTEGER,
    duration     INTEGER,
    radiant_win  INTEGER,
    lobby_type   INTEGER,
    cluster      INTEGER,
    replay_salt  INTEGER,
    replay_state TEXT DEFAULT 'none',
    replay_error TEXT,
    detail       INTEGER DEFAULT 0,
    parsed_at    INTEGER,
    scoreboard   TEXT
);

CREATE TABLE IF NOT EXISTS match_users (
    match_id    INTEGER NOT NULL,
    account_id  INTEGER NOT NULL,
    chat_id     INTEGER NOT NULL,
    role        INTEGER,
    role_source TEXT,
    message_id  INTEGER,
    predicted   TEXT,
    actual      TEXT,
    metrics     TEXT,
    PRIMARY KEY (match_id, account_id)
);

CREATE TABLE IF NOT EXISTS players (
    match_id    INTEGER NOT NULL,
    player_slot INTEGER NOT NULL,
    account_id  INTEGER,
    hero_id     INTEGER,
    hero_name   TEXT,
    rank_tier   INTEGER,
    features    TEXT,
    PRIMARY KEY (match_id, player_slot)
);

CREATE TABLE IF NOT EXISTS role_hints (
    account_id INTEGER NOT NULL,
    hero_id    INTEGER NOT NULL,
    lane       INTEGER NOT NULL,
    role       INTEGER NOT NULL,
    PRIMARY KEY (account_id, hero_id, lane)
);

CREATE TABLE IF NOT EXISTS benchmarks (
    hero_id    INTEGER NOT NULL,
    metric     TEXT NOT NULL,
    percentile REAL NOT NULL,
    value      REAL NOT NULL,
    fetched_at INTEGER,
    PRIMARY KEY (hero_id, metric, percentile)
);

CREATE TABLE IF NOT EXISTS weights (
    account_id INTEGER PRIMARY KEY,
    vector     TEXT,
    fitted_at  INTEGER
);

CREATE TABLE IF NOT EXISTS gc_budget (day TEXT PRIMARY KEY, requests INTEGER);

CREATE TABLE IF NOT EXISTS match_meta (match_id INTEGER PRIMARY KEY, data TEXT);

CREATE TABLE IF NOT EXISTS match_replay (match_id INTEGER PRIMARY KEY, data TEXT);

CREATE TABLE IF NOT EXISTS corpus (
    hero_id INTEGER NOT NULL,
    metric  TEXT NOT NULL,
    seen    INTEGER NOT NULL,
    vals    BLOB NOT NULL,
    PRIMARY KEY (hero_id, metric)
);

-- Какие матчи уже учтены: один матч не должен попасть в выборку дважды,
-- иначе его игроки получат двойной вес.
CREATE TABLE IF NOT EXISTS corpus_matches (match_id INTEGER PRIMARY KEY);

CREATE INDEX IF NOT EXISTS idx_match_users_chat ON match_users(chat_id);
CREATE INDEX IF NOT EXISTS idx_players_account ON players(account_id);
`

// DB — обёртка над sqlite.
type DB struct{ sql *sql.DB }

// Open открывает базу и накатывает схему.
func Open(path string) (*DB, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(schema); err != nil {
		return nil, fmt.Errorf("схема: %w", err)
	}
	// Досыпаем колонки, появившиеся позже схемы. Ошибка «уже есть» — не ошибка.
	for _, stmt := range []string{
		`ALTER TABLE matches ADD COLUMN retry_after INTEGER DEFAULT 0`,
	} {
		_, _ = db.Exec(stmt)
	}
	return &DB{sql: db}, nil
}

func (d *DB) Close() error { return d.sql.Close() }

// ---------------------------------------------------------------- kv

func (d *DB) Get(key string) string {
	var v string
	_ = d.sql.QueryRow(`SELECT value FROM kv WHERE key=?`, key).Scan(&v)
	return v
}

func (d *DB) Put(key, value string) error {
	_, err := d.sql.Exec(
		`INSERT INTO kv(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`,
		key, value)
	return err
}

// ---------------------------------------------------------------- users

// User — пользователь бота.
type User struct {
	ChatID    int64
	AccountID int64
	Nickname  string
	Status    Status
	MainRole  dota.Role
	Watch     bool
	CreatedAt int64
	LastPoll  int64
}

// UpsertUser создаёт или обновляет пользователя, сохраняя статус.
func (d *DB) UpsertUser(u User) error {
	_, err := d.sql.Exec(`
		INSERT INTO users(chat_id,account_id,nickname,status,changed_at,created_at,watch)
		VALUES(?,?,?,?,?,?,1)
		ON CONFLICT(chat_id) DO UPDATE SET account_id=excluded.account_id, nickname=excluded.nickname`,
		u.ChatID, u.AccountID, u.Nickname, string(u.Status), time.Now().Unix(), time.Now().Unix())
	return err
}

func scanUser(row interface{ Scan(...any) error }) (User, error) {
	var u User
	var status string
	var role, watch int
	err := row.Scan(&u.ChatID, &u.AccountID, &u.Nickname, &status, &role, &watch, &u.CreatedAt, &u.LastPoll)
	u.Status = Status(status)
	u.MainRole = dota.Role(role)
	u.Watch = watch == 1
	return u, err
}

const userCols = `chat_id,account_id,COALESCE(nickname,''),status,COALESCE(main_role,0),COALESCE(watch,1),COALESCE(created_at,0),COALESCE(last_poll,0)`

// User возвращает пользователя по чату.
func (d *DB) User(chatID int64) (User, bool) {
	row := d.sql.QueryRow(`SELECT `+userCols+` FROM users WHERE chat_id=?`, chatID)
	u, err := scanUser(row)
	return u, err == nil
}

// Users возвращает всех пользователей, новые сверху по дате.
func (d *DB) Users() ([]User, error) {
	rows, err := d.sql.Query(`SELECT ` + userCols + ` FROM users ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// SetStatus меняет статус пользователя.
func (d *DB) SetStatus(chatID int64, s Status, by int64) error {
	_, err := d.sql.Exec(`UPDATE users SET status=?, changed_at=?, changed_by=? WHERE chat_id=?`,
		string(s), time.Now().Unix(), by, chatID)
	return err
}

// SetWatch включает или выключает слежение.
func (d *DB) SetWatch(chatID int64, on bool) error {
	v := 0
	if on {
		v = 1
	}
	_, err := d.sql.Exec(`UPDATE users SET watch=? WHERE chat_id=?`, v, chatID)
	return err
}

// TouchPoll отмечает время последнего опроса.
func (d *DB) TouchPoll(chatID int64) {
	_, _ = d.sql.Exec(`UPDATE users SET last_poll=? WHERE chat_id=?`, time.Now().Unix(), chatID)
}

// AdminCount — сколько сейчас админов.
func (d *DB) AdminCount() int {
	var n int
	_ = d.sql.QueryRow(`SELECT count(*) FROM users WHERE status='admin'`).Scan(&n)
	return n
}

// Admins возвращает чаты всех админов.
func (d *DB) Admins() []int64 {
	rows, err := d.sql.Query(`SELECT chat_id FROM users WHERE status='admin'`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			out = append(out, id)
		}
	}
	return out
}

// DeleteUser удаляет пользователя и его разметку.
func (d *DB) DeleteUser(chatID int64) error {
	if _, err := d.sql.Exec(`DELETE FROM match_users WHERE chat_id=?`, chatID); err != nil {
		return err
	}
	_, err := d.sql.Exec(`DELETE FROM users WHERE chat_id=?`, chatID)
	return err
}

// ---------------------------------------------------------------- matches

// KnownMatch сообщает, есть ли матч в базе, и его состояние разбора.
func (d *DB) KnownMatch(matchID int64) (ReplayState, bool) {
	var s string
	err := d.sql.QueryRow(`SELECT replay_state FROM matches WHERE match_id=?`, matchID).Scan(&s)
	if err != nil {
		return ReplayNone, false
	}
	return ReplayState(s), true
}

// SaveMatch сохраняет матч и игроков. Скорборд кладётся как есть.
func (d *DB) SaveMatch(m *dota.Match, raw []byte) error {
	win := 0
	if m.RadiantWin {
		win = 1
	}
	_, err := d.sql.Exec(`
		INSERT INTO matches(match_id,start_time,duration,radiant_win,lobby_type,cluster,detail,scoreboard)
		VALUES(?,?,?,?,?,?,?,?)
		ON CONFLICT(match_id) DO UPDATE SET
			duration=excluded.duration, detail=excluded.detail, scoreboard=excluded.scoreboard`,
		m.ID, m.StartTime, m.Duration, win, m.LobbyType, m.Cluster, int(m.Detail), string(raw))
	if err != nil {
		return err
	}
	for _, p := range m.Players {
		feat, _ := json.Marshal(p.Benchmarks)
		_, err = d.sql.Exec(`
			INSERT INTO players(match_id,player_slot,account_id,hero_id,hero_name,rank_tier,features)
			VALUES(?,?,?,?,?,?,?)
			ON CONFLICT(match_id,player_slot) DO UPDATE SET features=excluded.features, rank_tier=excluded.rank_tier`,
			m.ID, p.Slot, p.AccountID, p.HeroID, p.Name(), p.RankTier, string(feat))
		if err != nil {
			return err
		}
	}
	return nil
}

// LoadScoreboard возвращает сырой сохранённый матч.
func (d *DB) LoadScoreboard(matchID int64) ([]byte, bool) {
	var s string
	err := d.sql.QueryRow(`SELECT COALESCE(scoreboard,'') FROM matches WHERE match_id=?`, matchID).Scan(&s)
	if err != nil || s == "" {
		return nil, false
	}
	return []byte(s), true
}

// SetReplayState переводит матч в новое состояние разбора. Неудача — не
// приговор: матч будет взят снова через полчаса, потому что причина обычно
// временная (сессия Game Coordinator переподключается, сервис не отвечает).
func (d *DB) SetReplayState(matchID int64, s ReplayState, errText string) error {
	retry := int64(0)
	if s == ReplayFailed {
		retry = time.Now().Add(30 * time.Minute).Unix()
	}
	_, err := d.sql.Exec(
		`UPDATE matches SET replay_state=?, replay_error=?, retry_after=? WHERE match_id=?`,
		string(s), errText, retry, matchID)
	return err
}

// ClaimForReplay атомарно переводит матч из none в queued. Возвращает true,
// если заявку поставил именно этот вызов — так один матч не качается дважды.
func (d *DB) ClaimForReplay(matchID int64) (bool, error) {
	res, err := d.sql.Exec(
		`UPDATE matches SET replay_state='queued' WHERE match_id=? AND replay_state IN ('none','skipped_unverified')`,
		matchID)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// SaveMeta кладёт разобранные метаданные матча.
func (d *DB) SaveMeta(matchID int64, data []byte) error {
	_, err := d.sql.Exec(`
		INSERT INTO match_meta(match_id,data) VALUES(?,?)
		ON CONFLICT(match_id) DO UPDATE SET data=excluded.data`, matchID, string(data))
	return err
}

// LoadMeta возвращает сохранённые метаданные матча.
func (d *DB) LoadMeta(matchID int64) ([]byte, bool) {
	var s string
	if d.sql.QueryRow(`SELECT COALESCE(data,'') FROM match_meta WHERE match_id=?`, matchID).Scan(&s) != nil || s == "" {
		return nil, false
	}
	return []byte(s), true
}

// SaveReplay кладёт результат своего разбора реплея.
func (d *DB) SaveReplay(matchID int64, data []byte) error {
	_, err := d.sql.Exec(`
		INSERT INTO match_replay(match_id,data) VALUES(?,?)
		ON CONFLICT(match_id) DO UPDATE SET data=excluded.data`, matchID, string(data))
	return err
}

// LoadReplay возвращает сохранённый разбор реплея.
func (d *DB) LoadReplay(matchID int64) ([]byte, bool) {
	var s string
	if d.sql.QueryRow(`SELECT COALESCE(data,'') FROM match_replay WHERE match_id=?`, matchID).Scan(&s) != nil || s == "" {
		return nil, false
	}
	return []byte(s), true
}

// QueuedMatches возвращает матчи, ждущие разбора.
func (d *DB) QueuedMatches(limit int) ([]int64, error) {
	rows, err := d.sql.Query(`
		SELECT match_id FROM matches
		WHERE replay_state = 'queued'
		   OR (replay_state = 'failed' AND COALESCE(retry_after,0) > 0 AND COALESCE(retry_after,0) < ?)
		ORDER BY start_time DESC LIMIT ?`, time.Now().Unix(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// ---------------------------------------------------------------- match_users

// MatchUser — связь матча и пользователя бота.
type MatchUser struct {
	MatchID   int64
	AccountID int64
	ChatID    int64
	Role      dota.Role
	Source    dota.Source
	MessageID int64
	Predicted []int
	Actual    []int
}

// LinkMatchUser сохраняет связь и прогноз модели.
func (d *DB) LinkMatchUser(mu MatchUser, metrics map[string]float64) error {
	pred, _ := json.Marshal(mu.Predicted)
	met, _ := json.Marshal(metrics)
	_, err := d.sql.Exec(`
		INSERT INTO match_users(match_id,account_id,chat_id,role,role_source,message_id,predicted,metrics)
		VALUES(?,?,?,?,?,?,?,?)
		ON CONFLICT(match_id,account_id) DO UPDATE SET
			role=excluded.role, role_source=excluded.role_source,
			predicted=excluded.predicted, metrics=excluded.metrics`,
		mu.MatchID, mu.AccountID, mu.ChatID, int(mu.Role), string(mu.Source), mu.MessageID,
		string(pred), string(met))
	return err
}

// HasMatchUser сообщает, связан ли матч с этим игроком.
func (d *DB) HasMatchUser(matchID, accountID int64) bool {
	var one int
	err := d.sql.QueryRow(`SELECT 1 FROM match_users WHERE match_id=? AND account_id=?`,
		matchID, accountID).Scan(&one)
	return err == nil
}

// SetMessageID запоминает сообщение со сводкой, чтобы потом его дополнить.
func (d *DB) SetMessageID(matchID, accountID, messageID int64) error {
	_, err := d.sql.Exec(`UPDATE match_users SET message_id=? WHERE match_id=? AND account_id=?`,
		messageID, matchID, accountID)
	return err
}

// SetActual сохраняет реальную тройку с экрана Dota.
func (d *DB) SetActual(matchID, accountID int64, slots []int) error {
	b, _ := json.Marshal(slots)
	_, err := d.sql.Exec(`UPDATE match_users SET actual=? WHERE match_id=? AND account_id=?`,
		string(b), matchID, accountID)
	return err
}

// Actual возвращает сохранённую разметку.
func (d *DB) Actual(matchID, accountID int64) []int {
	var s string
	if d.sql.QueryRow(`SELECT COALESCE(actual,'') FROM match_users WHERE match_id=? AND account_id=?`,
		matchID, accountID).Scan(&s) != nil || s == "" {
		return nil
	}
	var out []int
	_ = json.Unmarshal([]byte(s), &out)
	return out
}

// SetRole сохраняет роль в матче.
func (d *DB) SetRole(matchID, accountID int64, role dota.Role, src dota.Source) error {
	_, err := d.sql.Exec(`UPDATE match_users SET role=?, role_source=? WHERE match_id=? AND account_id=?`,
		int(role), string(src), matchID, accountID)
	return err
}

// Participants возвращает пользователей бота, игравших в матче.
func (d *DB) Participants(matchID int64) ([]MatchUser, error) {
	rows, err := d.sql.Query(
		`SELECT match_id,account_id,chat_id,COALESCE(role,0),COALESCE(role_source,''),COALESCE(message_id,0)
		 FROM match_users WHERE match_id=?`, matchID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MatchUser
	for rows.Next() {
		var mu MatchUser
		var role int
		var src string
		if err := rows.Scan(&mu.MatchID, &mu.AccountID, &mu.ChatID, &role, &src, &mu.MessageID); err != nil {
			return nil, err
		}
		mu.Role = dota.Role(role)
		mu.Source = dota.Source(src)
		out = append(out, mu)
	}
	return out, rows.Err()
}

// UserMatches возвращает последние матчи пользователя.
type UserMatch struct {
	MatchID   int64
	StartTime int64
	HeroName  string
	Win       bool
	Role      dota.Role
}

func (d *DB) UserMatches(accountID int64, limit int) ([]UserMatch, error) {
	rows, err := d.sql.Query(`
		SELECT m.match_id, COALESCE(m.start_time,0), COALESCE(p.hero_name,''),
		       COALESCE(m.radiant_win,0), COALESCE(mu.role,0), COALESCE(p.player_slot,0)
		FROM match_users mu
		JOIN matches m ON m.match_id = mu.match_id
		LEFT JOIN players p ON p.match_id = mu.match_id AND p.account_id = mu.account_id
		WHERE mu.account_id = ?
		ORDER BY m.start_time DESC LIMIT ?`, accountID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []UserMatch
	for rows.Next() {
		var um UserMatch
		var radiantWin, role, slot int
		if err := rows.Scan(&um.MatchID, &um.StartTime, &um.HeroName, &radiantWin, &role, &slot); err != nil {
			return nil, err
		}
		um.Role = dota.Role(role)
		um.Win = (slot < 128) == (radiantWin == 1)
		out = append(out, um)
	}
	return out, rows.Err()
}

// UnparsedMatches возвращает матчи за последние days дней, по которым нет
// разбора. Реплеи Valve живут около двух недель, поэтому просить разбор для
// более старых бессмысленно.
func (d *DB) UnparsedMatches(days int) ([]int64, error) {
	since := time.Now().AddDate(0, 0, -days).Unix()
	rows, err := d.sql.Query(
		`SELECT match_id FROM matches WHERE detail = 0 AND start_time >= ? ORDER BY start_time DESC`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// HistoryRow — строка истории для проверки качества показателей.
type HistoryRow struct {
	MatchID int64
	Role    int
	Win     bool
	Metrics string
}

// HistoryRows возвращает все матчи игрока с посчитанными показателями.
func (d *DB) HistoryRows(accountID int64) ([]HistoryRow, error) {
	rows, err := d.sql.Query(`
		SELECT mu.match_id, COALESCE(mu.role,0), COALESCE(mu.metrics,'{}'),
		       COALESCE(m.radiant_win,0), COALESCE(p.player_slot,-1)
		FROM match_users mu
		JOIN matches m ON m.match_id = mu.match_id
		LEFT JOIN players p ON p.match_id = mu.match_id AND p.account_id = mu.account_id
		WHERE mu.account_id = ?`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []HistoryRow
	for rows.Next() {
		var h HistoryRow
		var radiantWin, slot int
		if err := rows.Scan(&h.MatchID, &h.Role, &h.Metrics, &radiantWin, &slot); err != nil {
			return nil, err
		}
		if slot < 0 {
			continue
		}
		h.Win = (slot < 128) == (radiantWin == 1)
		out = append(out, h)
	}
	return out, rows.Err()
}

// MatchCount — сколько матчей пользователя в базе.
func (d *DB) MatchCount(accountID int64) int {
	var n int
	_ = d.sql.QueryRow(`SELECT count(*) FROM match_users WHERE account_id=?`, accountID).Scan(&n)
	return n
}

// ---------------------------------------------------------------- role hints

// SaveRoleHint запоминает поправку роли.
func (d *DB) SaveRoleHint(accountID int64, heroID, lane int, role dota.Role) error {
	_, err := d.sql.Exec(`
		INSERT INTO role_hints(account_id,hero_id,lane,role) VALUES(?,?,?,?)
		ON CONFLICT(account_id,hero_id,lane) DO UPDATE SET role=excluded.role`,
		accountID, heroID, lane, int(role))
	return err
}

// RoleHint ищет поправку.
func (d *DB) RoleHint(accountID int64, heroID, lane int) (dota.Role, bool) {
	var r int
	err := d.sql.QueryRow(`SELECT role FROM role_hints WHERE account_id=? AND hero_id=? AND lane=?`,
		accountID, heroID, lane).Scan(&r)
	if err != nil {
		return dota.RoleUnknown, false
	}
	return dota.Role(r), true
}

// ---------------------------------------------------------------- benchmarks

// SaveBenchmarks кладёт снимок кривых перцентилей по герою.
func (d *DB) SaveBenchmarks(heroID int, curves map[string][]struct {
	Percentile float64
	Value      float64
}) error {
	tx, err := d.sql.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	now := time.Now().Unix()
	for metric, points := range curves {
		for _, pt := range points {
			if _, err := tx.Exec(`
				INSERT INTO benchmarks(hero_id,metric,percentile,value,fetched_at) VALUES(?,?,?,?,?)
				ON CONFLICT(hero_id,metric,percentile) DO UPDATE SET value=excluded.value, fetched_at=excluded.fetched_at`,
				heroID, metric, pt.Percentile, pt.Value, now); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

// Curve возвращает кривую перцентилей по герою и метрике.
func (d *DB) Curve(heroID int, metric string) ([][2]float64, error) {
	rows, err := d.sql.Query(
		`SELECT percentile,value FROM benchmarks WHERE hero_id=? AND metric=? ORDER BY percentile`,
		heroID, metric)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out [][2]float64
	for rows.Next() {
		var p, v float64
		if err := rows.Scan(&p, &v); err != nil {
			return nil, err
		}
		out = append(out, [2]float64{p, v})
	}
	return out, rows.Err()
}

// BenchmarksHeroCount — по скольким героям есть данные в снимке.
func (d *DB) BenchmarksHeroCount() int {
	var n int
	_ = d.sql.QueryRow(`SELECT count(DISTINCT hero_id) FROM benchmarks`).Scan(&n)
	return n
}

// BenchmarksFetchedAt — когда снимок обновлялся последний раз.
func (d *DB) BenchmarksFetchedAt() int64 {
	var t sql.NullInt64
	_ = d.sql.QueryRow(`SELECT max(fetched_at) FROM benchmarks`).Scan(&t)
	return t.Int64
}

// ---------------------------------------------------------------- weights

// SaveWeights сохраняет веса модели для пользователя.
func (d *DB) SaveWeights(accountID int64, w []float64) error {
	b, _ := json.Marshal(w)
	_, err := d.sql.Exec(`
		INSERT INTO weights(account_id,vector,fitted_at) VALUES(?,?,?)
		ON CONFLICT(account_id) DO UPDATE SET vector=excluded.vector, fitted_at=excluded.fitted_at`,
		accountID, string(b), time.Now().Unix())
	return err
}

// Weights возвращает веса пользователя.
func (d *DB) Weights(accountID int64) ([]float64, bool) {
	var s string
	if d.sql.QueryRow(`SELECT COALESCE(vector,'') FROM weights WHERE account_id=?`, accountID).Scan(&s) != nil || s == "" {
		return nil, false
	}
	var out []float64
	if json.Unmarshal([]byte(s), &out) != nil {
		return nil, false
	}
	return out, true
}

// ---------------------------------------------------------------- обучение

// LabelledMatch — размеченный матч для обучения модели.
type LabelledMatch struct {
	MatchID int64
	Actual  []int
	Slots   []int
	Vectors [][]float64
}

// Labelled возвращает размеченные матчи пользователя.
func (d *DB) Labelled(accountID int64, featureKeys []string) ([]LabelledMatch, error) {
	rows, err := d.sql.Query(
		`SELECT match_id, actual FROM match_users WHERE account_id=? AND actual IS NOT NULL AND actual != '[]'`,
		accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type pair struct {
		id     int64
		actual []int
	}
	var pairs []pair
	for rows.Next() {
		var id int64
		var s string
		if err := rows.Scan(&id, &s); err != nil {
			return nil, err
		}
		var act []int
		_ = json.Unmarshal([]byte(s), &act)
		if len(act) > 0 {
			pairs = append(pairs, pair{id, act})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var out []LabelledMatch
	for _, p := range pairs {
		prows, err := d.sql.Query(
			`SELECT player_slot, COALESCE(features,'{}') FROM players WHERE match_id=? ORDER BY player_slot`, p.id)
		if err != nil {
			return nil, err
		}
		lm := LabelledMatch{MatchID: p.id, Actual: p.actual}
		for prows.Next() {
			var slot int
			var feat string
			if err := prows.Scan(&slot, &feat); err != nil {
				_ = prows.Close()
				return nil, err
			}
			bench := map[string]float64{}
			_ = json.Unmarshal([]byte(feat), &bench)
			vec := make([]float64, len(featureKeys))
			for i, k := range featureKeys {
				v, ok := bench[k]
				if !ok {
					v = 0.5
				}
				vec[i] = v
			}
			lm.Slots = append(lm.Slots, slot)
			lm.Vectors = append(lm.Vectors, vec)
		}
		_ = prows.Close()
		if len(lm.Slots) > 1 {
			out = append(out, lm)
		}
	}
	return out, nil
}

// AverageOnHero — среднее по прошлым матчам на этом герое, независимо от роли.
// Нужно для показателей вроде точности стрелы: она зависит от героя, а не от
// позиции, и разбивать выборку по ролям значит потерять её.
func (d *DB) AverageOnHero(accountID int64, heroID int, key string) (float64, int, bool) {
	rows, err := d.sql.Query(`
		SELECT COALESCE(mu.metrics,'{}') FROM match_users mu
		JOIN players p ON p.match_id = mu.match_id AND p.account_id = mu.account_id
		WHERE mu.account_id = ? AND p.hero_id = ?`, accountID, heroID)
	if err != nil {
		return 0, 0, false
	}
	defer rows.Close()
	return averageOf(rows, key)
}

// AverageOnHeroRole — среднее по матчам на этом герое и в этой роли.
// Нужно там, где показатель задан и героем, и позицией: лечение Дазла на
// пятёрке и на миде — это разные величины.
func (d *DB) AverageOnHeroRole(accountID int64, heroID int, role dota.Role, key string) (float64, int, bool) {
	rows, err := d.sql.Query(`
		SELECT COALESCE(mu.metrics,'{}') FROM match_users mu
		JOIN players p ON p.match_id = mu.match_id AND p.account_id = mu.account_id
		WHERE mu.account_id = ? AND p.hero_id = ? AND mu.role = ?`, accountID, heroID, int(role))
	if err != nil {
		return 0, 0, false
	}
	defer rows.Close()
	return averageOf(rows, key)
}

// Average реализует analysis.History: среднее значение показателя по прошлым
// матчам игрока на той же роли.
func (d *DB) Average(accountID int64, role dota.Role, key string) (float64, int, bool) {
	rows, err := d.sql.Query(
		`SELECT COALESCE(metrics,'{}') FROM match_users WHERE account_id=? AND role=?`,
		accountID, int(role))
	if err != nil {
		return 0, 0, false
	}
	defer rows.Close()
	return averageOf(rows, key)
}

func averageOf(rows *sql.Rows, key string) (float64, int, bool) {
	var sum float64
	var n int
	for rows.Next() {
		var s string
		if rows.Scan(&s) != nil {
			continue
		}
		m := map[string]float64{}
		if json.Unmarshal([]byte(s), &m) != nil {
			continue
		}
		if v, ok := m[key]; ok {
			sum += v
			n++
		}
	}
	if n == 0 {
		return 0, 0, false
	}
	return sum / float64(n), n, true
}

// ---------------------------------------------------------------- бюджет GC

// TakeGCBudget пытается занять одну заявку из суточного лимита.
func (d *DB) TakeGCBudget(limit int) bool {
	day := time.Now().UTC().Format("2006-01-02")
	var used int
	_ = d.sql.QueryRow(`SELECT COALESCE(requests,0) FROM gc_budget WHERE day=?`, day).Scan(&used)
	if used >= limit {
		return false
	}
	_, err := d.sql.Exec(`
		INSERT INTO gc_budget(day,requests) VALUES(?,1)
		ON CONFLICT(day) DO UPDATE SET requests=requests+1`, day)
	return err == nil
}

// GCBudgetUsed — сколько заявок израсходовано сегодня.
func (d *DB) GCBudgetUsed() int {
	day := time.Now().UTC().Format("2006-01-02")
	var used int
	_ = d.sql.QueryRow(`SELECT COALESCE(requests,0) FROM gc_budget WHERE day=?`, day).Scan(&used)
	return used
}

// ------------------------------------------------------------------- корпус

// LoadCorpus поднимает все выборки: значения и счётчики всего виденного.
func (d *DB) LoadCorpus() (map[int]map[string][]float32, map[int]map[string]int64, error) {
	rows, err := d.sql.Query(`SELECT hero_id,metric,seen,vals FROM corpus`)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	values := map[int]map[string][]float32{}
	seen := map[int]map[string]int64{}
	for rows.Next() {
		var hero int
		var metric string
		var n int64
		var blob []byte
		if err := rows.Scan(&hero, &metric, &n, &blob); err != nil {
			return nil, nil, err
		}
		if values[hero] == nil {
			values[hero] = map[string][]float32{}
			seen[hero] = map[string]int64{}
		}
		values[hero][metric] = corpus.DecodeValues(blob)
		seen[hero][metric] = n
	}
	return values, seen, rows.Err()
}

// SaveCorpusRing сохраняет одну выборку целиком.
func (d *DB) SaveCorpusRing(heroID int, metric string, seen int64, values []float32) error {
	_, err := d.sql.Exec(`
		INSERT INTO corpus(hero_id,metric,seen,vals) VALUES(?,?,?,?)
		ON CONFLICT(hero_id,metric) DO UPDATE SET seen=excluded.seen, vals=excluded.vals`,
		heroID, metric, seen, corpus.EncodeValues(values))
	return err
}

// CorpusHasMatch — учтён ли матч в выборках.
func (d *DB) CorpusHasMatch(matchID int64) bool {
	var one int
	err := d.sql.QueryRow(`SELECT 1 FROM corpus_matches WHERE match_id=?`, matchID).Scan(&one)
	return err == nil
}

// CorpusAddMatch помечает матч учтённым.
func (d *DB) CorpusAddMatch(matchID int64) error {
	_, err := d.sql.Exec(`INSERT OR IGNORE INTO corpus_matches(match_id) VALUES(?)`, matchID)
	return err
}

// CorpusSize — сколько матчей учтено и сколько пар «герой+метрика» заполнено.
func (d *DB) CorpusSize() (matches, series int) {
	_ = d.sql.QueryRow(`SELECT count(*) FROM corpus_matches`).Scan(&matches)
	_ = d.sql.QueryRow(`SELECT count(*) FROM corpus`).Scan(&series)
	return matches, series
}

// NewestMatchID — самый свежий известный матч. Нужен как точка отсчёта для
// обхода публичного потока: номер в последовательности берётся от него.
func (d *DB) NewestMatchID() (int64, error) {
	var id int64
	err := d.sql.QueryRow(`SELECT match_id FROM matches ORDER BY start_time DESC LIMIT 1`).Scan(&id)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return id, err
}

// MatchIDsNotInCorpus — матчи, ещё не учтённые в выборках перцентилей.
// Свои матчи уже скачаны, и не использовать их было бы расточительством.
func (d *DB) MatchIDsNotInCorpus() ([]int64, error) {
	rows, err := d.sql.Query(`
		SELECT m.match_id FROM matches m
		LEFT JOIN corpus_matches c ON c.match_id = m.match_id
		WHERE c.match_id IS NULL AND m.scoreboard IS NOT NULL
		ORDER BY m.start_time`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
