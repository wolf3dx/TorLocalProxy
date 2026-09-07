package service

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"gitlab.com/vkandreevich/torlocalproxy/core/control"
	"gitlab.com/vkandreevich/torlocalproxy/core/torrun"
)

const отпечаток = "0123456789ABCDEF0123456789ABCDEF01234567"

// ------------------------------------------------------------- подставки --

// поддельныйЗапуск изображает torrun.Runtime, ничего не запуская.
type поддельныйЗапуск struct {
	конец    torrun.Endpoint
	ошибка   error
	запущен  int
	остановл int
	мосты    int
	плагины  []torrun.PTPlugin
}

func (п *поддельныйЗапуск) Start(_ context.Context, cfg torrun.Config) (torrun.Endpoint, error) {
	п.запущен++
	п.мосты = len(cfg.Bridges)
	п.плагины = cfg.PTPlugins
	if п.ошибка != nil {
		return torrun.Endpoint{}, п.ошибка
	}
	return п.конец, nil
}

func (п *поддельныйЗапуск) Stop() error {
	п.остановл++
	return nil
}

// поддельныеТранспорты изображают ptrun.Runner, ничего не поднимая.
type поддельныеТранспорты struct {
	плагины   []torrun.PTPlugin
	ошибка    error
	поднято   []string
	остановки int
}

func (т *поддельныеТранспорты) Start(имена []string, _ string) ([]torrun.PTPlugin, error) {
	т.поднято = append([]string(nil), имена...)
	if т.ошибка != nil {
		return nil, т.ошибка
	}
	return т.плагины, nil
}

func (т *поддельныеТранспорты) Stop() error {
	т.остановки++
	return nil
}

// поддельныйКлиент изображает control.Client: отдаёт заранее заданный
// ход подключения и запоминает вызовы.
type поддельныйКлиент struct {
	шаги         []control.Bootstrap
	адресSocks   string
	ошибкаАвторз error
	новаяЦепочка int
	закрыт       int
}

func (к *поддельныйКлиент) Authenticate(context.Context) error {
	return к.ошибкаАвторз
}
func (к *поддельныйКлиент) TakeOwnership(context.Context) error { return nil }
func (к *поддельныйКлиент) NewIdentity(context.Context) error {
	к.новаяЦепочка++
	return nil
}
func (к *поддельныйКлиент) Close() error { к.закрыт++; return nil }

func (к *поддельныйКлиент) SocksAddress(context.Context) (string, error) {
	if к.адресSocks == "" {
		return "", errors.New("адрес неизвестен")
	}
	return к.адресSocks, nil
}

func (к *поддельныйКлиент) WatchBootstrap(context.Context) (<-chan control.Bootstrap, error) {
	канал := make(chan control.Bootstrap, len(к.шаги))
	for _, шаг := range к.шаги {
		канал <- шаг
	}
	close(канал)
	return канал, nil
}

// записнойНаблюдатель запоминает всё, что показал бы интерфейс.
type записнойНаблюдатель struct {
	мьютекс   sync.Mutex
	состояния []string
	проценты  []int
	строки    []string
}

func (н *записнойНаблюдатель) OnBootstrap(percent int, _ string) {
	н.мьютекс.Lock()
	defer н.мьютекс.Unlock()
	н.проценты = append(н.проценты, percent)
}

func (н *записнойНаблюдатель) OnLog(line string) {
	н.мьютекс.Lock()
	defer н.мьютекс.Unlock()
	н.строки = append(н.строки, line)
}

func (н *записнойНаблюдатель) OnState(state string) {
	н.мьютекс.Lock()
	defer н.мьютекс.Unlock()
	н.состояния = append(н.состояния, state)
}

func (н *записнойНаблюдатель) снимок() ([]string, []int, []string) {
	н.мьютекс.Lock()
	defer н.мьютекс.Unlock()
	return append([]string(nil), н.состояния...),
		append([]int(nil), н.проценты...),
		append([]string(nil), н.строки...)
}

// служба собирает Service на подставках.
func служба(t *testing.T, запуск *поддельныйЗапуск, клиент *поддельныйКлиент) *Service {
	t.Helper()
	return службаСТранспортами(t, запуск, клиент, &поддельныеТранспорты{})
}

func службаСТранспортами(t *testing.T, запуск *поддельныйЗапуск, клиент *поддельныйКлиент,
	транспорты *поддельныеТранспорты) *Service {
	t.Helper()
	s, err := New(Options{
		StateDir:   t.TempDir(),
		Runtime:    запуск,
		Transports: транспорты,
		Dial: func(context.Context, string, control.Options) (control.Client, error) {
			return клиент, nil
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

func полныйХод() []control.Bootstrap {
	return []control.Bootstrap{
		{Percent: 0, Phase: "Запуск"},
		{Percent: 25, Phase: "Запрос состояния сети"},
		{Percent: 100, Phase: "Готово — Tor подключён"},
	}
}

// ---------------------------------------------------------------- New --

func TestNewТребуетКаталогИЗапуск(t *testing.T) {
	if _, err := New(Options{Runtime: &поддельныйЗапуск{}}); err == nil {
		t.Error("без каталога состояния служба не должна создаваться")
	}
	if _, err := New(Options{StateDir: t.TempDir()}); err == nil {
		t.Error("без реализации запуска служба не должна создаваться")
	}
}

// ------------------------------------------------------------ Connect --

func TestУспешноеПодключение(t *testing.T) {
	запуск := &поддельныйЗапуск{конец: torrun.Endpoint{ControlAddress: "127.0.0.1:9051"}}
	клиент := &поддельныйКлиент{шаги: полныйХод(), адресSocks: "127.0.0.1:9050"}
	s := служба(t, запуск, клиент)
	наблюдатель := &записнойНаблюдатель{}

	if err := s.Connect(context.Background(), "", наблюдатель); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	if s.State() != StateConnected {
		t.Errorf("состояние = %q, ожидалось %q", s.State(), StateConnected)
	}
	if s.SocksAddress() != "127.0.0.1:9050" {
		t.Errorf("адрес прокси = %q", s.SocksAddress())
	}
	if процент, _ := s.Progress(); процент != 100 {
		t.Errorf("процент = %d, ожидалось 100", процент)
	}

	состояния, проценты, _ := наблюдатель.снимок()
	if len(состояния) < 2 || состояния[0] != StateConnecting || состояния[len(состояния)-1] != StateConnected {
		t.Errorf("состояния = %v, ожидался переход connecting → connected", состояния)
	}
	// Интерфейсу нужен каждый шаг, а не только итог: плашка со статусом
	// показывает процент.
	if len(проценты) != 3 {
		t.Errorf("наблюдателю пришло %d шагов, ожидалось 3: %v", len(проценты), проценты)
	}
}

// Порт, заданный явно, известен сразу — у control о нём не спрашивают.
func TestЯвныйПортНеСпрашиваетControl(t *testing.T) {
	запуск := &поддельныйЗапуск{конец: torrun.Endpoint{
		ControlAddress: "127.0.0.1:9051",
		SocksAddress:   "127.0.0.1:9055",
	}}
	клиент := &поддельныйКлиент{шаги: полныйХод()} // адрес не задан — спросить нельзя
	s := служба(t, запуск, клиент)

	if err := s.Connect(context.Background(), "", &записнойНаблюдатель{}); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if s.SocksAddress() != "127.0.0.1:9055" {
		t.Errorf("адрес прокси = %q, ожидался из Endpoint", s.SocksAddress())
	}
}

func TestПовторныйConnectОтклоняется(t *testing.T) {
	запуск := &поддельныйЗапуск{конец: torrun.Endpoint{ControlAddress: "127.0.0.1:9051"}}
	клиент := &поддельныйКлиент{шаги: полныйХод(), адресSocks: "127.0.0.1:9050"}
	s := служба(t, запуск, клиент)

	if err := s.Connect(context.Background(), "", nil); err != nil {
		t.Fatalf("первый Connect: %v", err)
	}
	if err := s.Connect(context.Background(), "", nil); err == nil {
		t.Error("второй Connect при работающем подключении должен отказать")
	}
	if запуск.запущен != 1 {
		t.Errorf("tor запускался %d раза, ожидался один", запуск.запущен)
	}
}

// ------------------------------------------------------------- отказы --

func TestОшибкаЗапускаВедётВFailed(t *testing.T) {
	запуск := &поддельныйЗапуск{ошибка: errors.New("tor не найден")}
	s := служба(t, запуск, &поддельныйКлиент{})
	наблюдатель := &записнойНаблюдатель{}

	if err := s.Connect(context.Background(), "", наблюдатель); err == nil {
		t.Fatal("ожидалась ошибка")
	}
	if s.State() != StateFailed {
		t.Errorf("состояние = %q, ожидалось %q", s.State(), StateFailed)
	}

	// Кнопка «журнал» должна объяснять, что случилось.
	_, _, строки := наблюдатель.снимок()
	вЖурнале := strings.Join(строки, "\n")
	if !strings.Contains(вЖурнале, "tor не найден") {
		t.Errorf("причина не попала в журнал:\n%s", вЖурнале)
	}
}

func TestОшибкаАвторизацииОстанавливаетTor(t *testing.T) {
	запуск := &поддельныйЗапуск{конец: torrun.Endpoint{ControlAddress: "127.0.0.1:9051"}}
	клиент := &поддельныйКлиент{ошибкаАвторз: errors.New("нет доступа")}
	s := служба(t, запуск, клиент)

	if err := s.Connect(context.Background(), "", nil); err == nil {
		t.Fatal("ожидалась ошибка")
	}
	if запуск.остановл == 0 {
		t.Error("после отказа tor должен быть остановлен, иначе останется висеть")
	}
	if клиент.закрыт == 0 {
		t.Error("control-соединение должно быть закрыто")
	}
}

// Подключение, не дошедшее до 100 %, — это отказ, а не успех.
func TestНедошедшийBootstrapЭтоОтказ(t *testing.T) {
	запуск := &поддельныйЗапуск{конец: torrun.Endpoint{ControlAddress: "127.0.0.1:9051"}}
	клиент := &поддельныйКлиент{шаги: []control.Bootstrap{
		{Percent: 10, Phase: "Мост ответил"},
		{Percent: 25, Phase: "Запрос состояния сети"},
	}}
	s := служба(t, запуск, клиент)

	if err := s.Connect(context.Background(), "", nil); err == nil {
		t.Fatal("оборвавшийся bootstrap должен быть ошибкой")
	}
	if s.State() != StateFailed {
		t.Errorf("состояние = %q", s.State())
	}
	if запуск.остановл == 0 {
		t.Error("tor должен быть остановлен")
	}
}

// -------------------------------------------------------------- мосты --

func TestМостыПопадаютВЗапуск(t *testing.T) {
	запуск := &поддельныйЗапуск{конец: torrun.Endpoint{ControlAddress: "127.0.0.1:9051"}}
	клиент := &поддельныйКлиент{шаги: полныйХод(), адресSocks: "127.0.0.1:9050"}
	s := служба(t, запуск, клиент)

	текст := "192.0.2.4:9001 " + отпечаток + "\n192.0.2.5:9001 " + отпечаток + "\n"
	if err := s.Connect(context.Background(), текст, nil); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if запуск.мосты != 2 {
		t.Errorf("до запуска дошло %d мостов, ожидалось 2", запуск.мосты)
	}
}

// Транспорт, которого нет в сборке, диагностируется ДО запуска чего
// угодно: иначе tor стартует и падает с ошибкой, по которой ничего не
// понять.
func TestНеподдерживаемыйТранспортОтклоняетсяДоЗапуска(t *testing.T) {
	запуск := &поддельныйЗапуск{конец: torrun.Endpoint{ControlAddress: "127.0.0.1:9051"}}
	транспорты := &поддельныеТранспорты{}
	s := службаСТранспортами(t, запуск, &поддельныйКлиент{}, транспорты)

	// conjure строка мостов знает по форме, но в сборку он не входит:
	// ему нужен отдельный исполняемый файл, которого у нас нет.
	//
	// Раньше здесь стоял webtunnel — теперь он собран, и проверять на
	// нём нечего. Это не подгонка теста под код: смысл проверки в том,
	// что незнакомый транспорт отсекается заранее, а не в имени.
	err := s.Connect(context.Background(),
		"conjure [2001:db8::1]:443 "+отпечаток+" url=https://a.example/x", nil)
	if err == nil {
		t.Fatal("ожидался отказ: conjure в сборку не входит")
	}
	if !strings.Contains(err.Error(), "conjure") {
		t.Errorf("ошибка не называет транспорт: %v", err)
	}
	if запуск.запущен != 0 || len(транспорты.поднято) != 0 {
		t.Error("ни tor, ни транспорты не должны запускаться")
	}
}

// obfs4 в сборке есть — мосты с ним должны проходить, а адрес поднятого
// транспорта попадать в конфиг tor.
func TestObfs4ПоднимаетсяИПопадаетВКонфиг(t *testing.T) {
	запуск := &поддельныйЗапуск{конец: torrun.Endpoint{ControlAddress: "127.0.0.1:9051"}}
	транспорты := &поддельныеТранспорты{
		плагины: []torrun.PTPlugin{{Transports: []string{"obfs4"}, Address: "127.0.0.1:41000"}},
	}
	клиент := &поддельныйКлиент{шаги: полныйХод(), адресSocks: "127.0.0.1:9050"}
	s := службаСТранспортами(t, запуск, клиент, транспорты)

	текст := "obfs4 192.0.2.1:9443 " + отпечаток + " cert=AAA iat-mode=0"
	if err := s.Connect(context.Background(), текст, nil); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if !slices.Equal(транспорты.поднято, []string{"obfs4"}) {
		t.Errorf("поднимали %v, ожидался obfs4", транспорты.поднято)
	}
	if len(запуск.плагины) != 1 || запуск.плагины[0].Address != "127.0.0.1:41000" {
		t.Errorf("адрес транспорта не дошёл до tor: %+v", запуск.плагины)
	}
}

// Транспорты поднимаются раньше tor: их адреса нужны в torrc. Значит и
// отказ транспорта обязан останавливать всё до запуска tor.
func TestОтказТранспортаНеДаётЗапуститьTor(t *testing.T) {
	запуск := &поддельныйЗапуск{конец: torrun.Endpoint{ControlAddress: "127.0.0.1:9051"}}
	транспорты := &поддельныеТранспорты{ошибка: errors.New("порт не занялся")}
	s := службаСТранспортами(t, запуск, &поддельныйКлиент{}, транспорты)

	err := s.Connect(context.Background(), "obfs4 192.0.2.1:9443 "+отпечаток+" cert=AAA", nil)
	if err == nil {
		t.Fatal("ожидался отказ")
	}
	if запуск.запущен != 0 {
		t.Error("tor не должен запускаться без транспорта")
	}
}

// При отключении останавливаются и транспорты — иначе они останутся
// висеть на портах после «Отключить».
func TestDisconnectОстанавливаетТранспорты(t *testing.T) {
	запуск := &поддельныйЗапуск{конец: torrun.Endpoint{ControlAddress: "127.0.0.1:9051"}}
	транспорты := &поддельныеТранспорты{
		плагины: []torrun.PTPlugin{{Transports: []string{"obfs4"}, Address: "127.0.0.1:41000"}},
	}
	клиент := &поддельныйКлиент{шаги: полныйХод(), адресSocks: "127.0.0.1:9050"}
	s := службаСТранспортами(t, запуск, клиент, транспорты)

	if err := s.Connect(context.Background(), "obfs4 192.0.2.1:9443 "+отпечаток+" cert=AAA", nil); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if err := s.Disconnect(); err != nil {
		t.Fatalf("Disconnect: %v", err)
	}
	if транспорты.остановки == 0 {
		t.Error("транспорты остались подняты после Disconnect")
	}
}

func TestТекстБезМостовЭтоОшибка(t *testing.T) {
	s := служба(t, &поддельныйЗапуск{}, &поддельныйКлиент{})

	err := s.Connect(context.Background(), "Здравствуйте, вот ваши мосты:", nil)
	if err == nil {
		t.Fatal("текст без мостов должен быть ошибкой")
	}
	if !strings.Contains(err.Error(), "bridges@torproject.org") {
		t.Errorf("ошибка не подсказывает, что вставлять: %v", err)
	}
}

// ------------------------------------------------- сохранение мостов --

func TestМостыСохраняютсяИЧитаются(t *testing.T) {
	s := служба(t, &поддельныйЗапуск{}, &поддельныйКлиент{})

	пусто, err := s.LoadBridges()
	if err != nil {
		t.Fatalf("LoadBridges на пустом каталоге: %v", err)
	}
	if пусто != "" {
		t.Errorf("ожидалась пустая строка, получено %q", пусто)
	}

	текст := "obfs4 192.0.2.1:9443 " + отпечаток + " cert=AAA\n"
	if err := s.SaveBridges(текст); err != nil {
		t.Fatalf("SaveBridges: %v", err)
	}
	назад, err := s.LoadBridges()
	if err != nil {
		t.Fatalf("LoadBridges: %v", err)
	}
	if назад != текст {
		t.Errorf("прочитано %q, сохранялось %q", назад, текст)
	}
}

// ------------------------------------------------------ прочие вызовы --

func TestDisconnectВозвращаетВIdle(t *testing.T) {
	запуск := &поддельныйЗапуск{конец: torrun.Endpoint{ControlAddress: "127.0.0.1:9051"}}
	клиент := &поддельныйКлиент{шаги: полныйХод(), адресSocks: "127.0.0.1:9050"}
	s := служба(t, запуск, клиент)

	if err := s.Connect(context.Background(), "", nil); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if err := s.Disconnect(); err != nil {
		t.Fatalf("Disconnect: %v", err)
	}

	if s.State() != StateIdle {
		t.Errorf("состояние = %q, ожидалось %q", s.State(), StateIdle)
	}
	if s.SocksAddress() != "" {
		t.Errorf("после отключения адрес прокси должен опустеть, а он %q", s.SocksAddress())
	}
	if запуск.остановл == 0 {
		t.Error("tor должен быть остановлен")
	}

	// Повторный вызов и вызов на пустом месте — не ошибка: Disconnect
	// зовут в том числе при закрытии окна.
	if err := s.Disconnect(); err != nil {
		t.Errorf("повторный Disconnect: %v", err)
	}
}

func TestNewIdentityТребуетПодключения(t *testing.T) {
	запуск := &поддельныйЗапуск{конец: torrun.Endpoint{ControlAddress: "127.0.0.1:9051"}}
	клиент := &поддельныйКлиент{шаги: полныйХод(), адресSocks: "127.0.0.1:9050"}
	s := служба(t, запуск, клиент)

	if err := s.NewIdentity(context.Background()); err == nil {
		t.Error("без подключения NewIdentity должен отказать")
	}

	if err := s.Connect(context.Background(), "", nil); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if err := s.NewIdentity(context.Background()); err != nil {
		t.Fatalf("NewIdentity: %v", err)
	}
	if клиент.новаяЦепочка != 1 {
		t.Errorf("NEWNYM отправлен %d раз", клиент.новаяЦепочка)
	}
}

func TestЖурналРастётИОбрезается(t *testing.T) {
	s := служба(t, &поддельныйЗапуск{}, &поддельныйКлиент{})

	for range СтрокЖурнала + 50 {
		s.записать("строка")
	}
	if len(s.Log()) != СтрокЖурнала {
		t.Errorf("в журнале %d строк, ожидалось %d", len(s.Log()), СтрокЖурнала)
	}
}

func TestВЖурналеЕстьВремя(t *testing.T) {
	s := служба(t, &поддельныйЗапуск{}, &поддельныйКлиент{})
	s.записать("проверка")

	строки := s.Log()
	if len(строки) != 1 {
		t.Fatalf("строк %d", len(строки))
	}
	// Формат «ЧЧ:ММ:СС  текст» — по журналу должно быть видно, когда
	// подключение встало.
	if _, err := time.Parse("15:04:05", strings.Fields(строки[0])[0]); err != nil {
		t.Errorf("в начале строки нет времени: %q", строки[0])
	}
}

// Сроки зависят от того, чем подключаемся, и это не тонкость: meek_lite
// со snowflake тянут первые данные минутами, а obfs4 либо идёт сразу,
// либо мёртв. Один срок на всех обрывал медленные на середине — так на
// эмуляторе погиб meek_lite, дойдя до 25 %.
func TestСрокиЗависятОтТранспорта(t *testing.T) {
	быстрыйОбщий, быстрыйЗастой := сроки([]string{"obfs4"})
	if быстрыйОбщий != СрокПодключения {
		t.Errorf("для obfs4 общий срок %v, ожидался %v", быстрыйОбщий, СрокПодключения)
	}
	if быстрыйЗастой != 0 {
		t.Errorf("для obfs4 срок застоя должен остаться обычным (0 = по умолчанию), получено %v",
			быстрыйЗастой)
	}

	for _, имя := range []string{"meek_lite", "snowflake"} {
		общий, застой := сроки([]string{имя})
		if общий <= СрокПодключения {
			t.Errorf("для %s общий срок %v не больше обычного %v", имя, общий, СрокПодключения)
		}
		if застой <= 0 {
			t.Errorf("для %s срок застоя не задан", имя)
		}
	}

	// Смесь: если хоть один медленный, сроки берутся длинные — иначе он
	// умрёт, не успев начать.
	общий, застой := сроки([]string{"obfs4", "snowflake"})
	if общий != СрокПодключенияМедленных || застой != СрокЗастояМедленных {
		t.Errorf("смесь с медленным транспортом должна получать длинные сроки, получено %v/%v",
			общий, застой)
	}
}
