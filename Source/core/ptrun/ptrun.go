// Package ptrun поднимает pluggable transports внутри нашего процесса.
//
// Обычно obfs4 работает так: tor запускает отдельный исполняемый файл
// lyrebird и разговаривает с ним по SOCKS. Здесь этого файла нет — код
// lyrebird подключён библиотекой, а SOCKS-слушатель поднимаем мы сами.
// Причины две, и обе весомые:
//
//   - На iOS дочерние процессы запрещены совсем, и другого пути там нет
//     (ЛОГИКА.md, раздел 2.1). Один путь для всех платформ дешевле в
//     поддержке, чем два, из которых мобильный проверяется вдвое реже.
//   - Рядом с приложением не нужно класть посторонних исполняемых файлов,
//     а в torrc не остаётся ни одного exec. На Android это вообще
//     единственный вариант: положить исполняемый файл и запустить его из
//     каталога приложения там нельзя.
//
// Что делает слушатель: принимает соединение от tor, забирает из полей
// аутентификации SOCKS параметры моста (cert=, iat-mode=), заворачивает
// исходящее соединение в транспорт и перекачивает байты. Ровно то же
// делает main самого lyrebird — здесь оно вызвано напрямую.
package ptrun

import (
	"errors"
	"fmt"
	"io"
	"net"
	"sort"
	"strings"
	"sync"

	pt "gitlab.torproject.org/tpo/anti-censorship/pluggable-transports/goptlib"
	"gitlab.torproject.org/tpo/anti-censorship/pluggable-transports/lyrebird/transports/base"
	"gitlab.torproject.org/tpo/anti-censorship/pluggable-transports/lyrebird/transports/obfs4"

	"gitlab.com/vkandreevich/torlocalproxy/core/torconf"
)

// Plugin — поднятый транспорт: что он обслуживает и на каком адресе
// слушает. Тип общий с torconf, чтобы адрес попадал в torrc без
// перекладывания.
type Plugin = torconf.Plugin

// Runner поднимает и останавливает транспорты.
type Runner interface {
	// Start поднимает слушателей для перечисленных транспортов и
	// возвращает их адреса. stateDir нужен самим транспортам: obfs4
	// хранит там своё состояние.
	Start(transports []string, stateDir string) ([]Plugin, error)
	Stop() error
}

// Options настраивает Runner.
type Options struct {
	// Log — куда писать о происходящем. Пусто — молчать. Через него
	// строки попадают в журнал приложения: без них отказ транспорта
	// выглядит как «tor застрял на 10 %».
	Log func(string)
}

// Local — реализация на библиотеке lyrebird, в этом же процессе.
type Local struct {
	настройки Options

	мьютекс    sync.Mutex
	слушатели  []net.Listener
	ожидание   sync.WaitGroup
	остановлен bool
}

var _ Runner = (*Local)(nil)

// таблица — транспорты, которые собраны в приложение.
//
// Собирается своя, а не берётся transports.Init() из lyrebird: тот
// пакет импортирует сразу все транспорты, включая snowflake и webtunnel,
// и притаскивает за ними pion/webrtc с aws-sdk. Для APK это лишние
// мегабайты за то, чем мы не пользуемся. Новый транспорт добавляется
// одной строкой здесь.
var таблица = map[string]base.Transport{
	"obfs4": &obfs4.Transport{},
}

// New создаёт Runner. Ничего не поднимает.
func New(opts Options) *Local {
	return &Local{настройки: opts}
}

// Supported перечисляет транспорты, которые умеет поднять эта сборка.
// Порядок устойчивый: список попадает в текст ошибки.
func Supported() []string {
	имена := make([]string, 0, len(таблица))
	for имя := range таблица {
		имена = append(имена, имя)
	}
	sort.Strings(имена)
	return имена
}

func (л *Local) Start(имена []string, stateDir string) ([]Plugin, error) {
	if len(имена) == 0 {
		return nil, nil
	}
	if stateDir == "" {
		return nil, errors.New("транспортам нужен каталог состояния")
	}
	л.мьютекс.Lock()
	if len(л.слушатели) > 0 {
		л.мьютекс.Unlock()
		return nil, errors.New("транспорты уже подняты")
	}
	л.остановлен = false
	л.мьютекс.Unlock()

	var плагины []Plugin
	for _, имя := range имена {
		плагин, err := л.поднять(имя, stateDir)
		if err != nil {
			// Поднялось не всё — не оставляем половину висеть.
			_ = л.Stop()
			return nil, err
		}
		плагины = append(плагины, плагин)
	}
	return плагины, nil
}

// поднять запускает одного слушателя под один транспорт. По слушателю на
// транспорт, а не один общий: у каждого транспорта своя фабрика, и
// разделять их проще, чем угадывать по параметрам, кто пришёл.
func (л *Local) поднять(имя, stateDir string) (Plugin, error) {
	транспорт, известен := таблица[имя]
	if !известен {
		return Plugin{}, fmt.Errorf(
			"транспорт %q не поддерживается; доступны: %s",
			имя, strings.Join(Supported(), ", "))
	}

	фабрика, err := транспорт.ClientFactory(stateDir)
	if err != nil {
		return Plugin{}, fmt.Errorf("транспорт %s не готов: %w", имя, err)
	}

	// Порт нулевой: свободный выберет система, и он же уйдёт в torrc.
	// Фиксировать порт нельзя — на телефоне он может быть занят чем угодно.
	слушатель, err := pt.ListenSocks("tcp", "127.0.0.1:0")
	if err != nil {
		return Plugin{}, fmt.Errorf("транспорт %s не занял порт: %w", имя, err)
	}

	л.мьютекс.Lock()
	л.слушатели = append(л.слушатели, слушатель)
	л.мьютекс.Unlock()

	л.ожидание.Add(1)
	go func() {
		defer л.ожидание.Done()
		л.принимать(имя, слушатель, фабрика)
	}()

	л.записать(fmt.Sprintf("Транспорт %s слушает %s", имя, слушатель.Addr()))
	return Plugin{Transports: []string{имя}, Address: слушатель.Addr().String()}, nil
}

func (л *Local) принимать(имя string, слушатель *pt.SocksListener, фабрика base.ClientFactory) {
	for {
		соединение, err := слушатель.AcceptSocks()
		if err != nil {
			л.мьютекс.Lock()
			остановлен := л.остановлен
			л.мьютекс.Unlock()
			if остановлен {
				return
			}
			var сетевая net.Error
			if errors.As(err, &сетевая) {
				// Временная неурядица — соединение отбросили, слушаем дальше.
				continue
			}
			л.записать(fmt.Sprintf("Транспорт %s перестал принимать соединения: %v", имя, err))
			return
		}

		л.ожидание.Add(1)
		go func() {
			defer л.ожидание.Done()
			л.обслужить(имя, соединение, фабрика)
		}()
	}
}

// обслужить проводит одно соединение через транспорт.
func (л *Local) обслужить(имя string, соединение *pt.SocksConn, фабрика base.ClientFactory) {
	defer func() { _ = соединение.Close() }()

	// Параметры моста tor передаёт в полях аутентификации SOCKS — там же
	// живут cert= и iat-mode=. Разбирает их сам транспорт.
	аргументы, err := фабрика.ParseArgs(&соединение.Req.Args)
	if err != nil {
		л.записать(fmt.Sprintf("Транспорт %s: негодные параметры моста: %v", имя, err))
		_ = соединение.Reject()
		return
	}

	// Наружу идём обычным net.Dial: прокси перед транспортом мы не
	// поддерживаем, и городить его ради этого не за чем.
	удалённое, err := фабрика.Dial("tcp", соединение.Req.Target, base.DialFunc(net.Dial), аргументы)
	if err != nil {
		л.записать(fmt.Sprintf("Транспорт %s: мост %s не ответил: %v",
			имя, соединение.Req.Target, err))
		_ = соединение.Reject()
		return
	}
	defer func() { _ = удалённое.Close() }()

	if err := соединение.Grant(&net.TCPAddr{IP: net.IPv4zero, Port: 0}); err != nil {
		л.записать(fmt.Sprintf("Транспорт %s: не удалось подтвердить SOCKS: %v", имя, err))
		return
	}

	перекачать(соединение, удалённое)
}

// перекачать гоняет байты в обе стороны и возвращается, когда любая из
// сторон закрылась.
func перекачать(первое, второе net.Conn) {
	готово := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(первое, второе)
		готово <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(второе, первое)
		готово <- struct{}{}
	}()
	<-готово
}

// Stop закрывает слушателей и дожидается, пока разойдутся обработчики.
// Безопасен при повторном вызове и до Start.
func (л *Local) Stop() error {
	л.мьютекс.Lock()
	слушатели := л.слушатели
	л.слушатели = nil
	л.остановлен = true
	л.мьютекс.Unlock()

	var первая error
	for _, слушатель := range слушатели {
		if err := слушатель.Close(); err != nil && первая == nil {
			первая = err
		}
	}
	л.ожидание.Wait()

	if len(слушатели) > 0 {
		л.записать("Транспорты остановлены")
	}
	return первая
}

func (л *Local) записать(строка string) {
	if л.настройки.Log != nil {
		л.настройки.Log(строка)
	}
}
