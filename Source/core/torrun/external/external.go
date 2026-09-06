// Package external поднимает tor отдельным процессом — так работает
// десктопная поставка.
//
// Мобильная реализация того же интерфейса появится на этапе 8: на iOS
// дочерние процессы запрещены, и там tor линкуется библиотекой
// (ЛОГИКА.md, раздел 2.1). Поэтому весь код, который знает про exec.Cmd,
// заперт в этом пакете и наружу не торчит.
package external

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"gitlab.com/vkandreevich/torlocalproxy/core/torconf"
	"gitlab.com/vkandreevich/torlocalproxy/core/torrun"
)

// СрокControlПорта — сколько ждать, пока tor напишет файл с адресом
// control-порта. Файл появляется в самом конце инициализации.
const СрокControlПорта = 30 * time.Second

// строкЖурнала — сколько последних строк stderr держать, чтобы приложить
// их к ошибке. Без них «процесс завершился с кодом 1» не говорит ничего.
const строкЖурнала = 20

// Options настраивает запуск.
type Options struct {
	// Binary — путь к tor. Пусто — искать по известным местам (FindTor).
	Binary string
	// Args — аргументы перед «-f torrc». Нужны, когда tor запускается не
	// напрямую, а через обёртку: так интеграционные тесты подставляют
	// заглушку, запуская «python fake_tor.py -f torrc».
	Args []string
}

// Runtime — реализация torrun.Runtime на отдельном процессе.
type Runtime struct {
	настройки Options

	мьютекс sync.Mutex
	процесс *exec.Cmd
	готов   chan struct{} // закрывается, когда процесс завершился
	журнал  кольцо
}

// New создаёт среду выполнения. Ничего не запускает и не ищет: поиск tor
// происходит в Start, чтобы ошибка «tor не найден» приходила тогда, когда
// пользователь нажал «Подключиться», а не при старте приложения.
func New(opts Options) *Runtime {
	return &Runtime{настройки: opts}
}

// Убедимся на этапе компиляции, что интерфейс реализован.
var _ torrun.Runtime = (*Runtime)(nil)

func (r *Runtime) Start(ctx context.Context, cfg torrun.Config) (torrun.Endpoint, error) {
	r.мьютекс.Lock()
	if r.процесс != nil {
		r.мьютекс.Unlock()
		return torrun.Endpoint{}, errors.New("tor уже запущен")
	}
	r.журнал.очистить()
	r.мьютекс.Unlock()

	бинарник, err := FindTor(r.настройки.Binary)
	if err != nil {
		return torrun.Endpoint{}, err
	}

	if cfg.DataDir == "" {
		return torrun.Endpoint{}, errors.New("не задан каталог состояния tor")
	}
	if err := подготовитьКаталог(cfg.DataDir); err != nil {
		return torrun.Endpoint{}, err
	}

	файлPID := filepath.Join(cfg.DataDir, "tor.pid")
	файлПорта := filepath.Join(cfg.DataDir, "control_port")
	файлЖурнала := filepath.Join(cfg.DataDir, "tor.log")
	файлCookie := filepath.Join(cfg.DataDir, "control_auth_cookie")
	файлКонфига := filepath.Join(cfg.DataDir, "torrc")

	// Сначала убираем за собой прошлый запуск. Приложение могли снять
	// из списка задач или убить по нехватке памяти — тогда наш tor
	// остаётся жить сиротой и держит порт. Пользователь этого не видит
	// и не понимает, почему «порт занят».
	убратьПрошлыйЗапуск(файлPID)

	// Если порт занят кем-то ещё, лучше сказать сразу и внятно, чем дать
	// tor упасть с невнятной строкой в своём журнале. Прототип в этом
	// месте молча переключался на автоматический выбор — но тогда
	// пользователь, настроивший 9050, не понимает, почему прокси
	// оказался на другом порту.
	if cfg.SocksPort != 0 {
		if err := портСвободен("127.0.0.1", cfg.SocksPort); err != nil {
			return torrun.Endpoint{}, err
		}
	}

	// Файл с адресом control-порта обязан появиться заново: старый
	// остался бы от прошлого запуска и увёл бы нас на мёртвый порт.
	for _, путь := range []string{файлПорта, файлЖурнала} {
		if err := os.Remove(путь); err != nil && !os.IsNotExist(err) {
			return torrun.Endpoint{}, fmt.Errorf("не удалось убрать %s: %w", путь, err)
		}
	}

	// В torrc уходят пути в форме, которую переживёт сам tor: под Windows
	// это короткие имена 8.3 (см. путьДляTor). Себе оставляем исходные —
	// файл control_port мы читаем своими средствами, и им всё равно.
	конфиг := torconf.Build(torconf.Config{
		DataDir:     путьДляTor(cfg.DataDir),
		LogFile:     путьДляTor(файлЖурнала),
		ControlFile: путьДляTor(файлПорта),
		SocksPort:   cfg.SocksPort,
		Bridges:     cfg.Bridges,
		Plugins:     cfg.PTPlugins,
		// Отдельный процесс — единственный случай, когда владелец
		// вообще существует: tor завершится, если мы исчезнем.
		OwningPID: os.Getpid(),
		Extra:     cfg.ExtraTorrc,
	})
	if err := os.WriteFile(файлКонфига, []byte(конфиг), 0o600); err != nil {
		return torrun.Endpoint{}, fmt.Errorf("не записался torrc: %w", err)
	}

	аргументы := append(append([]string{}, r.настройки.Args...), "-f", файлКонфига)
	команда := exec.Command(бинарник, аргументы...)
	команда.Dir = filepath.Dir(бинарник)
	// Читаются оба потока: раннюю жалобу на конфиг tor пишет в stdout,
	// ещё до того как узнает про Log-файл. Без stdout отказ выглядит как
	// «процесс завершился» без единой подсказки.
	ошибки, err := команда.StderrPipe()
	if err != nil {
		return torrun.Endpoint{}, fmt.Errorf("не открылся поток ошибок tor: %w", err)
	}
	вывод, err := команда.StdoutPipe()
	if err != nil {
		return torrun.Endpoint{}, fmt.Errorf("не открылся поток вывода tor: %w", err)
	}
	скрытьОкно(команда)

	if err := команда.Start(); err != nil {
		return torrun.Endpoint{}, fmt.Errorf("не запустился %s: %w", бинарник, err)
	}

	готов := make(chan struct{})
	r.мьютекс.Lock()
	r.процесс = команда
	r.готов = готов
	r.мьютекс.Unlock()

	// Запоминаем номер процесса: по нему следующий запуск уберёт за
	// собой, если этот не переживёт закрытия приложения.
	if команда.Process != nil {
		_ = os.WriteFile(файлPID, []byte(strconv.Itoa(команда.Process.Pid)), 0o600)
	}

	go r.читатьВывод(ошибки)
	go r.читатьВывод(вывод)
	go func() {
		_ = команда.Wait()
		close(готов)
	}()

	адресControl, err := r.ждатьControlПорт(ctx, файлПорта, готов)
	if err != nil {
		_ = r.Stop()
		return torrun.Endpoint{}, err
	}

	конец := torrun.Endpoint{
		ControlAddress: адресControl,
		CookiePath:     файлCookie,
	}
	if cfg.SocksPort != 0 {
		конец.SocksAddress = fmt.Sprintf("127.0.0.1:%d", cfg.SocksPort)
	}
	return конец, nil
}

// Stop останавливает tor. Безопасен при повторном вызове и до Start:
// его зовут и из обработчика ошибок, где состояние неизвестно.
//
// Возвращается только после того, как процесс действительно завершился,
// — иначе следующий Start наткнулся бы на ещё занятые порты.
func (r *Runtime) Stop() error {
	r.мьютекс.Lock()
	команда, готов := r.процесс, r.готов
	r.процесс, r.готов = nil, nil
	r.мьютекс.Unlock()

	if команда == nil || команда.Process == nil {
		return nil
	}

	// Сначала вежливо: tor успевает закрыть слушающие сокеты сам.
	// На Windows os.Interrupt не поддерживается, там сразу Kill.
	if runtime.GOOS != "windows" {
		_ = команда.Process.Signal(os.Interrupt)
		select {
		case <-готов:
			return nil
		case <-time.After(10 * time.Second):
		}
	}

	if err := команда.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return fmt.Errorf("не удалось остановить tor: %w", err)
	}
	select {
	case <-готов:
	case <-time.After(10 * time.Second):
		return errors.New("процесс tor не завершился за 10 с")
	}
	return nil
}

// LastLog возвращает последние строки, которые tor написал в stderr.
// Нужны для диагностики: без них отказ выглядит как код возврата.
func (r *Runtime) LastLog() []string {
	r.мьютекс.Lock()
	defer r.мьютекс.Unlock()
	return r.журнал.строки()
}

// ждатьControlПорт ждёт файл, который tor пишет в самом конце запуска:
// раньше него control-порт слушать некому.
func (r *Runtime) ждатьControlПорт(ctx context.Context, файлПорта string, готов <-chan struct{}) (string, error) {
	срок := time.NewTimer(СрокControlПорта)
	defer срок.Stop()
	опрос := time.NewTicker(100 * time.Millisecond)
	defer опрос.Stop()

	for {
		if адрес, err := прочитатьАдрес(файлПорта); err == nil {
			return адрес, nil
		}

		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-готов:
			// Дадим последний шанс: файл мог появиться прямо перед
			// завершением, а завершение — законным (заглушка так себя
			// и ведёт при FAKE_TOR_FAIL).
			if адрес, err := прочитатьАдрес(файлПорта); err == nil {
				return адрес, nil
			}
			return "", fmt.Errorf("процесс tor завершился, не открыв control-порт%s",
				хвостЖурнала(r.LastLog()))
		case <-срок.C:
			return "", fmt.Errorf("tor не открыл control-порт за %s%s",
				СрокControlПорта, хвостЖурнала(r.LastLog()))
		case <-опрос.C:
		}
	}
}

func прочитатьАдрес(файлПорта string) (string, error) {
	данные, err := os.ReadFile(файлПорта)
	if err != nil {
		return "", err
	}
	строка := strings.TrimSpace(string(данные))
	адрес, найдено := strings.CutPrefix(строка, "PORT=")
	if !найдено || адрес == "" {
		return "", fmt.Errorf("в %s нет строки PORT=", файлПорта)
	}
	return адрес, nil
}

func хвостЖурнала(строки []string) string {
	if len(строки) == 0 {
		return ""
	}
	return ".\nЧто сказал tor:\n  " + strings.Join(строки, "\n  ")
}

func (r *Runtime) читатьВывод(поток io.ReadCloser) {
	сканер := bufio.NewScanner(поток)
	for сканер.Scan() {
		строка := strings.TrimSpace(сканер.Text())
		if строка == "" {
			continue
		}
		r.мьютекс.Lock()
		r.журнал.добавить(строка)
		r.мьютекс.Unlock()
	}
}

// убратьПрошлыйЗапуск снимает tor, оставшийся от прошлого раза.
//
// Ошибки здесь намеренно молчаливые: файла может не быть, номер может
// принадлежать уже кому-то другому, прав может не хватить. Это уборка
// на всякий случай, а не операция, от которой что-то зависит.
func убратьПрошлыйЗапуск(файлPID string) {
	данные, err := os.ReadFile(файлPID)
	if err != nil {
		return
	}
	номер, err := strconv.Atoi(strings.TrimSpace(string(данные)))
	if err != nil || номер <= 1 {
		_ = os.Remove(файлPID)
		return
	}

	процесс, err := os.FindProcess(номер)
	if err == nil {
		_ = процесс.Kill()
		// Даём ядру освободить порты, прежде чем занимать их снова.
		time.Sleep(300 * time.Millisecond)
	}
	_ = os.Remove(файлPID)
}

// подготовитьКаталог создаёт каталог состояния. tor отказывается
// работать, если права на DataDirectory шире 0700.
func подготовитьКаталог(путь string) error {
	if err := os.MkdirAll(путь, 0o700); err != nil {
		return fmt.Errorf("не создался каталог состояния %s: %w", путь, err)
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(путь, 0o700); err != nil {
			return fmt.Errorf("не выставились права 0700 на %s: %w", путь, err)
		}
	}
	return nil
}

func портСвободен(хост string, порт int) error {
	слушатель, err := net.Listen("tcp", fmt.Sprintf("%s:%d", хост, порт))
	if err != nil {
		return fmt.Errorf("порт %d занят: %w. Укажите другой или 0, "+
			"чтобы tor выбрал свободный сам", порт, err)
	}
	return слушатель.Close()
}

// кольцо хранит последние строки журнала.
type кольцо struct {
	данные []string
}

func (к *кольцо) добавить(строка string) {
	к.данные = append(к.данные, строка)
	if len(к.данные) > строкЖурнала {
		к.данные = к.данные[len(к.данные)-строкЖурнала:]
	}
}

func (к *кольцо) строки() []string {
	return append([]string(nil), к.данные...)
}

func (к *кольцо) очистить() {
	к.данные = nil
}
