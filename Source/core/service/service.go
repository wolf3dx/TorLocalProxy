// Package service связывает ядро в одно целое: разбор мостов, запуск tor,
// control-протокол и состояние подключения.
//
// Это единственный пакет, с которым разговаривает интерфейс. Ни окно на
// десктопе, ни экран на Android не знают ни про torrc, ни про
// control-порт: они вызывают Connect, слушают Observer и показывают то,
// что пришло.
//
// Observer намеренно «бедный» — только знаковые целые, строки и никаких
// структур. Так он переедет через границу gomobile на Android без единой
// правки: там проходят не всякие типы.
package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"gitlab.com/vkandreevich/torlocalproxy/core/bridges"
	"gitlab.com/vkandreevich/torlocalproxy/core/control"
	"gitlab.com/vkandreevich/torlocalproxy/core/torrun"
)

// Состояния подключения. Строками, а не числами, — их показывает
// интерфейс, и в журнале они читаются без расшифровки.
const (
	StateIdle       = "idle"       // не подключены
	StateConnecting = "connecting" // идёт подключение, есть процент
	StateConnected  = "connected"  // работает, прокси слушает
	StateFailed     = "failed"     // сорвалось, причина в журнале
)

// Observer получает всё, что интерфейсу нужно показывать.
//
// Вызывается из служебной горутины, а не из потока интерфейса: на Fyne и
// на Android обновлять виджеты надо в их собственном потоке, и это забота
// того, кто реализует Observer.
type Observer interface {
	// OnBootstrap — ход подключения: процент и фаза по-русски.
	OnBootstrap(percent int, phase string)
	// OnLog — строка журнала. Та же строка уже сохранена внутри, см. Log.
	OnLog(line string)
	// OnState — смена состояния, одна из констант State*.
	OnState(state string)
}

// СтрокЖурнала — сколько строк держать для кнопки «журнал». Хватает,
// чтобы увидеть весь ход подключения и причину отказа.
const СтрокЖурнала = 500

// СрокПодключения — сколько всего ждать подключения.
const СрокПодключения = 3 * time.Minute

// Options — настройки службы.
type Options struct {
	// StateDir — каталог для состояния tor, журнала и сохранённых мостов.
	StateDir string
	// SocksPort — 0 означает «пусть tor выберет свободный сам».
	SocksPort int
	// Runtime — как поднимать tor. Пусто недопустимо: выбор реализации
	// (отдельный процесс или встроенная библиотека) делает вызывающий,
	// потому что он зависит от платформы сборки.
	Runtime torrun.Runtime
	// Dial — как открывать control-соединение. Пусто — control.Dial.
	// Поле существует ради тестов: с ним служба проверяется без tor.
	Dial func(ctx context.Context, address string, opts control.Options) (control.Client, error)
}

// Service — фасад ядра.
type Service struct {
	настройки Options

	мьютекс   sync.Mutex
	состояние string
	процент   int
	фаза      string
	socks     string
	журнал    []string
	клиент    control.Client
	отмена    context.CancelFunc
	наблюдате Observer
}

// New создаёт службу. Ничего не запускает.
func New(opts Options) (*Service, error) {
	if opts.StateDir == "" {
		return nil, errors.New("не задан каталог состояния")
	}
	if opts.Runtime == nil {
		return nil, errors.New("не задана реализация запуска tor")
	}
	if opts.Dial == nil {
		opts.Dial = control.Dial
	}
	return &Service{настройки: opts, состояние: StateIdle}, nil
}

// State возвращает текущее состояние.
func (s *Service) State() string {
	s.мьютекс.Lock()
	defer s.мьютекс.Unlock()
	return s.состояние
}

// Progress возвращает процент и фазу последнего шага подключения.
func (s *Service) Progress() (int, string) {
	s.мьютекс.Lock()
	defer s.мьютекс.Unlock()
	return s.процент, s.фаза
}

// SocksAddress возвращает адрес, который вписывают в чужие приложения.
// Пусто, пока подключение не завершилось.
func (s *Service) SocksAddress() string {
	s.мьютекс.Lock()
	defer s.мьютекс.Unlock()
	return s.socks
}

// Log возвращает накопленный журнал — то, что показывает кнопка
// «посмотреть журнал».
func (s *Service) Log() []string {
	s.мьютекс.Lock()
	defer s.мьютекс.Unlock()
	return append([]string(nil), s.журнал...)
}

// Connect подключается к сети Tor и возвращается, когда подключение
// установлено или сорвалось. Ход дела приходит в Observer.
//
// Блокирующий вызов: интерфейс зовёт его из отдельной горутины. Так
// проще и честнее, чем прятать внутри ещё один поток и заставлять
// вызывающего угадывать, когда всё закончилось.
func (s *Service) Connect(ctx context.Context, bridgesText string, obs Observer) error {
	s.мьютекс.Lock()
	if s.состояние == StateConnecting || s.состояние == StateConnected {
		s.мьютекс.Unlock()
		return errors.New("подключение уже идёт или установлено")
	}
	s.наблюдате = obs
	s.журнал = nil
	s.процент, s.фаза, s.socks = 0, "", ""
	s.мьютекс.Unlock()

	s.сменитьСостояние(StateConnecting)

	мосты, err := s.подготовитьМосты(bridgesText)
	if err != nil {
		s.провал(err)
		return err
	}

	ctx, отмена := context.WithTimeout(ctx, СрокПодключения)
	s.мьютекс.Lock()
	s.отмена = отмена
	s.мьютекс.Unlock()
	defer отмена()

	s.записать("Запуск tor")
	конец, err := s.настройки.Runtime.Start(ctx, torrun.Config{
		DataDir:   s.настройки.StateDir,
		Bridges:   мосты,
		SocksPort: s.настройки.SocksPort,
	})
	if err != nil {
		s.провал(fmt.Errorf("не удалось запустить tor: %w", err))
		return err
	}
	s.записать("control-порт: " + конец.ControlAddress)

	клиент, err := s.настройки.Dial(ctx, конец.ControlAddress, control.Options{})
	if err != nil {
		s.свернуть()
		s.провал(err)
		return err
	}
	s.мьютекс.Lock()
	s.клиент = клиент
	s.мьютекс.Unlock()

	if err := клиент.Authenticate(ctx); err != nil {
		s.свернуть()
		s.провал(err)
		return err
	}
	// Жизнь tor привязывается к этому соединению: оборвётся оно —
	// tor завершится сам, а не останется висеть в памяти.
	if err := клиент.TakeOwnership(ctx); err != nil {
		s.записать("предупреждение: " + err.Error())
	}

	события, err := клиент.WatchBootstrap(ctx)
	if err != nil {
		s.свернуть()
		s.провал(err)
		return err
	}

	// Читаем события сами, а не отдаём канал в control.Wait: интерфейсу
	// нужен каждый шаг, а не только итог.
	итог, err := s.следитьЗаПодключением(ctx, события)
	if err != nil {
		s.свернуть()
		s.провал(err)
		return err
	}

	адрес := конец.SocksAddress
	if адрес == "" {
		// Порт выбирал сам tor — спросить можно только у него.
		адрес, err = клиент.SocksAddress(ctx)
		if err != nil {
			s.свернуть()
			s.провал(err)
			return err
		}
	}

	s.мьютекс.Lock()
	s.socks = адрес
	s.мьютекс.Unlock()
	s.записать(fmt.Sprintf("Подключено на %d%%. Прокси SOCKS5: %s", итог.Percent, адрес))
	s.сменитьСостояние(StateConnected)
	return nil
}

// следитьЗаПодключением переводит события в вызовы Observer и следит за
// застреванием — этим занимается control.Wait, но ему нужен свой канал,
// поэтому события раздваиваются здесь.
func (s *Service) следитьЗаПодключением(ctx context.Context, события <-chan control.Bootstrap) (control.Bootstrap, error) {
	дляОжидания := make(chan control.Bootstrap, 16)
	go func() {
		defer close(дляОжидания)
		for шаг := range события {
			s.мьютекс.Lock()
			изменилось := шаг.Percent != s.процент || шаг.Phase != s.фаза
			s.процент, s.фаза = шаг.Percent, шаг.Phase
			наблюдатель := s.наблюдате
			s.мьютекс.Unlock()

			if изменилось {
				if наблюдатель != nil {
					наблюдатель.OnBootstrap(шаг.Percent, шаг.Phase)
				}
				s.записать(fmt.Sprintf("%d%% — %s", шаг.Percent, шаг.Phase))
			}

			select {
			case дляОжидания <- шаг:
			case <-ctx.Done():
				return
			}
		}
	}()

	return control.Wait(ctx, дляОжидания, control.WaitOptions{
		Timeout: СрокПодключения,
	})
}

// подготовитьМосты разбирает текст и проверяет, что мы умеем поднять всё,
// что в нём заказано.
//
// Отсутствие транспорта диагностируется здесь, до запуска tor: иначе tor
// молча стартует, не может выполнить ClientTransportPlugin и падает с
// ошибкой, по которой ничего не понять.
func (s *Service) подготовитьМосты(текст string) ([]bridges.Bridge, error) {
	if strings.TrimSpace(текст) == "" {
		s.записать("Мосты не заданы — подключение напрямую")
		return nil, nil
	}

	разобранные, проблемы := bridges.Parse(текст)
	for _, проблема := range проблемы {
		s.записать("строка отброшена: " + проблема)
	}
	if len(разобранные) == 0 {
		return nil, errors.New("в тексте не нашлось ни одного моста; " +
			"вставьте строки целиком, как их присылает bridges@torproject.org")
	}
	s.записать(fmt.Sprintf("Мостов распознано: %d", len(разобранные)))

	if транспорты := bridges.Transports(разобранные); len(транспорты) > 0 {
		return nil, fmt.Errorf(
			"мосты требуют транспорт %s, а он пока не поднимается — "+
				"этого шага в приложении ещё нет; попробуйте обычные мосты без транспорта",
			strings.Join(транспорты, ", "))
	}
	return разобранные, nil
}

// Disconnect останавливает всё. Безопасен, когда ничего не запущено.
func (s *Service) Disconnect() error {
	s.свернуть()
	s.мьютекс.Lock()
	s.процент, s.фаза, s.socks = 0, "", ""
	s.мьютекс.Unlock()
	s.записать("Отключено")
	s.сменитьСостояние(StateIdle)
	return nil
}

// NewIdentity просит у tor новую цепочку — «сменить IP».
func (s *Service) NewIdentity(ctx context.Context) error {
	s.мьютекс.Lock()
	клиент := s.клиент
	s.мьютекс.Unlock()
	if клиент == nil {
		return errors.New("tor не подключён")
	}
	if err := клиент.NewIdentity(ctx); err != nil {
		return err
	}
	s.записать("Запрошена новая цепочка")
	return nil
}

// SaveBridges сохраняет текст мостов рядом с состоянием, чтобы при
// следующем запуске поле не пришлось заполнять заново.
func (s *Service) SaveBridges(текст string) error {
	if err := os.MkdirAll(s.настройки.StateDir, 0o700); err != nil {
		return fmt.Errorf("не создался каталог состояния: %w", err)
	}
	путь := filepath.Join(s.настройки.StateDir, "bridges.txt")
	if err := os.WriteFile(путь, []byte(текст), 0o600); err != nil {
		return fmt.Errorf("не сохранились мосты: %w", err)
	}
	return nil
}

// LoadBridges читает сохранённые мосты. Отсутствие файла — не ошибка.
func (s *Service) LoadBridges() (string, error) {
	данные, err := os.ReadFile(filepath.Join(s.настройки.StateDir, "bridges.txt"))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("не прочитались сохранённые мосты: %w", err)
	}
	return string(данные), nil
}

// свернуть закрывает control-соединение и останавливает tor. Ошибки
// глотаются намеренно: это путь отступления, и жаловаться на него
// некому — всё, что можно, уже в журнале.
func (s *Service) свернуть() {
	s.мьютекс.Lock()
	клиент, отмена := s.клиент, s.отмена
	s.клиент, s.отмена = nil, nil
	s.мьютекс.Unlock()

	if отмена != nil {
		отмена()
	}
	if клиент != nil {
		_ = клиент.Close()
	}
	_ = s.настройки.Runtime.Stop()
}

func (s *Service) сменитьСостояние(состояние string) {
	s.мьютекс.Lock()
	s.состояние = состояние
	наблюдатель := s.наблюдате
	s.мьютекс.Unlock()

	if наблюдатель != nil {
		наблюдатель.OnState(состояние)
	}
}

func (s *Service) провал(err error) {
	s.записать("Ошибка: " + err.Error())
	s.сменитьСостояние(StateFailed)
}

// записать добавляет строку в журнал и отдаёт её наблюдателю.
func (s *Service) записать(строка string) {
	метка := time.Now().Format("15:04:05") + "  " + строка

	s.мьютекс.Lock()
	s.журнал = append(s.журнал, метка)
	if len(s.журнал) > СтрокЖурнала {
		s.журнал = s.журнал[len(s.журнал)-СтрокЖурнала:]
	}
	наблюдатель := s.наблюдате
	s.мьютекс.Unlock()

	if наблюдатель != nil {
		наблюдатель.OnLog(метка)
	}
}
