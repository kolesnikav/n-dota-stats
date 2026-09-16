package gc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	dota2 "github.com/paralin/go-dota2"
	devents "github.com/paralin/go-dota2/events"
	"github.com/paralin/go-steam"
	unified "github.com/paralin/go-steam/protocol/protobuf/unified"
	"github.com/sirupsen/logrus"
)

// Steam — живая сессия Steam с подключением к Game Coordinator.
//
// Это единственный способ получить replay_salt: Web API его не отдаёт. Запрос
// работает по любому match_id, поэтому одного бот-аккаунта хватает на всех
// пользователей бота.
type Steam struct {
	Username string
	Password string
	// AuthCode — код Steam Guard с почты, TwoFactorCode — из мобильного
	// аутентификатора. Нужны только если в аккаунте включена защита.
	AuthCode      string
	TwoFactorCode string

	Log func(string, ...any)

	mu       sync.Mutex
	client   *steam.Client
	dota     *dota2.Dota2
	ready    chan struct{}
	readyOne sync.Once
	fatal    error
	live     bool // сессия с Game Coordinator открыта прямо сейчас
}

// NewSteam собирает клиента. Соединение поднимается отдельно — Start.
func NewSteam(username, password, authCode, twoFactor string, log func(string, ...any)) *Steam {
	if log == nil {
		log = func(string, ...any) {}
	}
	return &Steam{
		Username: username, Password: password,
		AuthCode: authCode, TwoFactorCode: twoFactor,
		Log:   log,
		ready: make(chan struct{}),
	}
}

func (s *Steam) Name() string { return "Steam Game Coordinator" }

// Start поднимает сессию и держит её, переподключаясь при обрывах.
// Возвращает управление, как только Game Coordinator поздоровался.
func (s *Steam) Start(ctx context.Context, timeout time.Duration) error {
	if s.Username == "" || s.Password == "" {
		return fmt.Errorf("%w: не заданы STEAM_BOT_USER и STEAM_BOT_PASS", ErrNotConfigured)
	}
	logger := logrus.New()
	logger.SetOutput(io.Discard) // библиотека шумит, свой лог ведём сами

	client := steam.NewClient()
	handler := dota2.New(client, logger)

	s.mu.Lock()
	s.client, s.dota = client, handler
	s.mu.Unlock()

	go s.loop(ctx, client, handler)

	client.Connect()

	select {
	case <-s.ready:
		return s.fatal
	case <-time.After(timeout):
		return fmt.Errorf("Game Coordinator не ответил за %s", timeout)
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Steam) loop(ctx context.Context, client *steam.Client, handler *dota2.Dota2) {
	for event := range client.Events() {
		select {
		case <-ctx.Done():
			return
		default:
		}
		switch e := event.(type) {
		case *steam.ConnectedEvent:
			s.Log("Steam: соединение установлено, вхожу как %s", s.Username)
			err := client.Auth.LogOn(ctx, &steam.LogOnDetails{
				Username:               s.Username,
				Password:               s.Password,
				AuthCode:               s.AuthCode,
				TwoFactorCode:          s.TwoFactorCode,
				DeviceFriendlyName:     "n-dota-stats",
				ShouldRememberPassword: true,
			})
			if err != nil {
				s.finish(fmt.Errorf("вход в Steam: %w%s", err, guardHint(err)))
				return
			}

		case *steam.LoggedOnEvent:
			s.Log("Steam: вход выполнен, запускаю Dota 2")
			handler.SetPlaying(true)
			// Здороваться приходится настойчиво: после переподключения
			// Game Coordinator отвечает не с первой попытки, а без его
			// приветствия любой запрос падает с «клиент не готов».
			go func() {
				for i := 0; i < 10; i++ {
					time.Sleep(time.Duration(2+i*3) * time.Second)
					if s.isLive() {
						return
					}
					handler.SayHello()
				}
				s.Log("Game Coordinator: не отозвался за десять попыток")
			}()

		case *steam.LogOnFailedEvent:
			s.finish(fmt.Errorf("вход отклонён Steam: %v (нужен код Steam Guard?)", e.Result))
			return

		case *steam.DisconnectedEvent:
			s.setLive(false)
			s.Log("Steam: соединение потеряно, переподключаюсь через 15 с")
			time.Sleep(15 * time.Second)
			client.Connect()

		case *steam.LoggedOffEvent:
			s.setLive(false)
			s.Log("Steam: сессия завершена")

		case *devents.ClientWelcomed:
			s.setLive(true)
			s.Log("Game Coordinator: сессия открыта")
			s.finish(nil)

		case *devents.ClientSuspended:
			s.Log("Game Coordinator: сессии временно приостановлены")

		case error:
			s.Log("Steam: %v", e)
		}
	}
}

// guardHint переводит требование Steam Guard в понятное указание: какой
// именно код нужен и куда его положить.
func guardHint(err error) string {
	var authErr *steam.AuthSessionError
	if !errors.As(err, &authErr) || len(authErr.Confirmations) == 0 {
		return ""
	}
	var parts []string
	for _, c := range authErr.Confirmations {
		switch c.Type {
		case unified.EAuthSessionGuardType_k_EAuthSessionGuardType_EmailCode:
			parts = append(parts, "код с почты — положи его в STEAM_GUARD_CODE")
		case unified.EAuthSessionGuardType_k_EAuthSessionGuardType_DeviceCode:
			parts = append(parts, "код из мобильного Steam — положи его в STEAM_TWO_FACTOR_CODE")
		case unified.EAuthSessionGuardType_k_EAuthSessionGuardType_DeviceConfirmation:
			parts = append(parts, "подтверждение в мобильном приложении")
		case unified.EAuthSessionGuardType_k_EAuthSessionGuardType_EmailConfirmation:
			parts = append(parts, "подтверждение по ссылке из письма")
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return "\nSteam просит: " + strings.Join(parts, "; ") +
		"\nКод живёт недолго, поэтому запускать бота нужно сразу после того, как положишь его в .env"
}

func (s *Steam) setLive(v bool) {
	s.mu.Lock()
	s.live = v
	s.mu.Unlock()
}

func (s *Steam) isLive() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.live
}

func (s *Steam) finish(err error) {
	s.readyOne.Do(func() {
		s.fatal = err
		close(s.ready)
	})
}

// ReplaySalt спрашивает у Game Coordinator ключ реплея и кластер.
func (s *Steam) ReplaySalt(matchID int64) (Salt, error) {
	s.mu.Lock()
	handler, live := s.dota, s.live
	s.mu.Unlock()
	if handler == nil {
		return Salt{}, ErrNotConfigured
	}
	if !live {
		return Salt{}, fmt.Errorf("сессия Game Coordinator сейчас закрыта")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	resp, err := handler.RequestMatchDetails(ctx, uint64(matchID))
	if err != nil {
		return Salt{}, fmt.Errorf("запрос матча %d: %w", matchID, err)
	}
	m := resp.GetMatch()
	if m == nil || m.GetReplaySalt() == 0 {
		return Salt{}, fmt.Errorf("матч %d: ключ реплея не выдан", matchID)
	}
	return Salt{Cluster: int(m.GetCluster()), Salt: m.GetReplaySalt()}, nil
}

// Close завершает сессию.
func (s *Steam) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dota != nil {
		s.dota.SetPlaying(false)
		s.dota.Close()
	}
	if s.client != nil {
		s.client.Disconnect()
	}
}
