// Package embedded поднимает tor библиотекой внутри нашего процесса.
//
// Так работает поставка для iPhone: на iOS запуск дочерних процессов
// запрещён совсем, и другого пути там нет. На Windows, Android и Linux
// tor запускается отдельным процессом (пакет external) — это надёжнее:
// что бы с ним ни случилось, окно останется живым и покажет причину.
//
// Саму библиотеку этот пакет не содержит и не выбирает: её приносит
// вызывающий в Options.Creator. Реализация для iOS лежит в torrun/torlib
// и требует cgo вместе со статическим архивом tor; здесь же только
// обвязка вокруг неё — torrc, ожидание control-порта, чтение журнала.
// Поэтому пакет собирается везде и ничего лишнего в сборку не тянет.
//
// Одно свойство встроенного tor протекает наружу, и его надо знать.
// Остановить его снаружи нечем: сигнала не пошлёшь, он внутри нас.
// Поэтому Stop не убивает процесс, а просит tor завершиться через
// control-порт — как это сделал бы человек командой SIGNAL HALT. И
// запустить tor в одном процессе дважды нельзя: tor_api.h прямо
// предупреждает, что повторный вызов может кончиться падением
// (ошибка 23847 в их учёте). После Stop на iOS приложение придётся
// перезапустить — об этом пишется в журнал.
package embedded

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/cretz/bine/process"

	"gitlab.com/vkandreevich/torlocalproxy/core/torconf"
	"gitlab.com/vkandreevich/torlocalproxy/core/torrun"
)

// строкЖурнала — сколько последних строк вывода tor держать для ошибки.
const строкЖурнала = 20

// Options настраивает запуск.
type Options struct {
	// Creator — чем поднимать tor. Обязателен: сам пакет библиотеки в
	// себе не несёт, её приносит платформенная сборка (torrun/torlib).
	Creator process.Creator
	// Log — куда отдавать строки вывода tor по мере поступления. Пусто —
	// только копить для сообщения об ошибке.
	Log func(string)
}

// Runtime — реализация torrun.Runtime на встроенной библиотеке.
type Runtime struct {
	настройки Options

	мьютекс sync.Mutex
	процесс process.Process
	отмена  context.CancelFunc
	готов   chan struct{}
	журнал  torrun.Кольцо

	// Пути нужны для остановки: чтобы попросить tor завершиться, надо
	// заново открыть control-порт и предъявить cookie.
	файлПорта  string
	файлCookie string
}

var _ torrun.Runtime = (*Runtime)(nil)

// New создаёт среду выполнения. Ничего не запускает.
func New(opts Options) *Runtime {
	return &Runtime{настройки: opts, журнал: torrun.Кольцо{Предел: строкЖурнала}}
}

func (r *Runtime) Start(ctx context.Context, cfg torrun.Config) (torrun.Endpoint, error) {
	r.мьютекс.Lock()
	if r.процесс != nil {
		r.мьютекс.Unlock()
		return torrun.Endpoint{}, errors.New("tor уже запущен")
	}
	r.журнал.Очистить()
	r.мьютекс.Unlock()

	if r.настройки.Creator == nil {
		return torrun.Endpoint{}, errors.New(
			"встроенный tor в эту сборку не включён: не задан Options.Creator")
	}
	if cfg.DataDir == "" {
		return torrun.Endpoint{}, errors.New("не задан каталог состояния tor")
	}
	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return torrun.Endpoint{}, fmt.Errorf("не создался каталог состояния: %w", err)
	}
	// tor отказывается работать, если права на DataDirectory шире 0700.
	if err := os.Chmod(cfg.DataDir, 0o700); err != nil {
		return torrun.Endpoint{}, fmt.Errorf("не выставились права 0700: %w", err)
	}

	файлПорта := filepath.Join(cfg.DataDir, "control_port")
	файлЖурнала := filepath.Join(cfg.DataDir, "tor.log")
	файлCookie := filepath.Join(cfg.DataDir, "control_auth_cookie")
	файлКонфига := filepath.Join(cfg.DataDir, "torrc")

	// Старый файл увёл бы нас на мёртвый порт от прошлого запуска.
	for _, путь := range []string{файлПорта, файлЖурнала} {
		if err := os.Remove(путь); err != nil && !os.IsNotExist(err) {
			return torrun.Endpoint{}, fmt.Errorf("не удалось убрать %s: %w", путь, err)
		}
	}

	конфиг := torconf.Build(torconf.Config{
		DataDir:     cfg.DataDir,
		LogFile:     файлЖурнала,
		ControlFile: файлПорта,
		SocksPort:   cfg.SocksPort,
		Bridges:     cfg.Bridges,
		Plugins:     cfg.PTPlugins,
		// Владельца нет: отдельного процесса, за смертью которого tor
		// мог бы следить, здесь не существует — он живёт внутри нас.
		Extra: cfg.ExtraTorrc,
	})
	if err := os.WriteFile(файлКонфига, []byte(конфиг), 0o600); err != nil {
		return torrun.Endpoint{}, fmt.Errorf("не записался torrc: %w", err)
	}

	// Свой ctx: жизнь tor кончается вместе с ним, а не со сроком запуска.
	жизнь, отмена := context.WithCancel(context.Background())
	процесс, err := r.настройки.Creator.New(жизнь, "-f", файлКонфига)
	if err != nil {
		отмена()
		return torrun.Endpoint{}, fmt.Errorf("не создался встроенный tor: %w", err)
	}
	if err := процесс.Start(); err != nil {
		отмена()
		return torrun.Endpoint{}, fmt.Errorf("встроенный tor не запустился: %w", err)
	}

	готов := make(chan struct{})
	r.мьютекс.Lock()
	r.процесс, r.отмена, r.готов = процесс, отмена, готов
	r.файлПорта, r.файлCookie = файлПорта, файлCookie
	r.мьютекс.Unlock()

	go func() {
		if err := процесс.Wait(); err != nil {
			r.записать("tor завершился: " + err.Error())
		}
		close(готов)
	}()

	// Встроенный tor пишет только в свой файл журнала, стандартных
	// потоков у него нет. Читаем файл — иначе кнопка «журнал» в
	// приложении осталась бы пустой ровно тогда, когда нужна.
	go r.читатьЖурнал(жизнь, файлЖурнала)

	адресControl, err := torrun.WaitForControlPort(ctx, файлПорта, готов, r.LastLog)
	if err != nil {
		_ = r.Stop()
		return torrun.Endpoint{}, err
	}

	конец := torrun.Endpoint{ControlAddress: адресControl, CookiePath: файлCookie}
	if cfg.SocksPort != 0 {
		конец.SocksAddress = fmt.Sprintf("127.0.0.1:%d", cfg.SocksPort)
	}
	return конец, nil
}

// Stop просит встроенный tor завершиться. Безопасен при повторном
// вызове и до Start.
//
// Отмена контекста здесь ничего не решает: она вернёт управление
// нашему ожиданию, но сам tor продолжит работать — он выполняется
// внутри нашего процесса. Единственный опрятный способ — сказать ему
// об этом через control-порт, что мы и делаем.
func (r *Runtime) Stop() error {
	r.мьютекс.Lock()
	отмена, готов := r.отмена, r.готов
	порт, cookie := r.файлПорта, r.файлCookie
	r.процесс, r.отмена, r.готов = nil, nil, nil
	r.мьютекс.Unlock()

	if отмена == nil {
		return nil
	}

	if err := попроситьЗавершиться(порт, cookie); err != nil {
		r.записать("не удалось попросить tor завершиться: " + err.Error())
	}

	// Ждём недолго: если tor не ушёл сам, дальше ждать бессмысленно —
	// убить его всё равно нечем.
	select {
	case <-готов:
		r.записать("Встроенный tor завершился. Для нового подключения " +
			"приложение придётся перезапустить: дважды в одном процессе " +
			"tor запускать нельзя.")
	case <-time.After(СрокОстанова):
		r.записать("tor не завершился за " + СрокОстанова.String() +
			"; перезапустите приложение")
	}
	отмена()
	return nil
}

// СрокОстанова — сколько ждать, пока tor уйдёт по просьбе.
const СрокОстанова = 10 * time.Second

// попроситьЗавершиться открывает control-порт и посылает SIGNAL HALT —
// ту же команду, которой останавливают обычный tor.
func попроситьЗавершиться(файлПорта, файлCookie string) error {
	if файлПорта == "" || файлCookie == "" {
		return errors.New("неизвестны пути control-порта")
	}
	адрес, err := torrun.ЧитатьАдресControl(файлПорта)
	if err != nil {
		return err
	}
	ключ, err := os.ReadFile(файлCookie)
	if err != nil {
		return fmt.Errorf("не прочитался cookie: %w", err)
	}

	соединение, err := net.DialTimeout("tcp", адрес, СрокОстанова)
	if err != nil {
		return fmt.Errorf("не открылся control-порт: %w", err)
	}
	defer func() { _ = соединение.Close() }()
	_ = соединение.SetDeadline(time.Now().Add(СрокОстанова))

	// Проверка подлинности по cookie: она передаётся шестнадцатеричной
	// строкой, ровно как это делает tor-контроллер.
	команды := fmt.Sprintf("AUTHENTICATE %x\r\nSIGNAL HALT\r\n", ключ)
	if _, err := соединение.Write([]byte(команды)); err != nil {
		return fmt.Errorf("не отправилась команда: %w", err)
	}
	// Ответ читаем, но не разбираем: tor уходит и может оборвать связь
	// прямо посреди ответа — это не ошибка, а ровно то, чего мы просили.
	_, _ = io.ReadAll(соединение)
	return nil
}

// LastLog возвращает последние строки журнала tor.
func (r *Runtime) LastLog() []string {
	r.мьютекс.Lock()
	defer r.мьютекс.Unlock()
	return r.журнал.Строки()
}

// читатьЖурнал следит за файлом журнала tor и тянет из него строки по
// мере появления. Файл создаётся не сразу, поэтому открытие повторяется.
func (r *Runtime) читатьЖурнал(ctx context.Context, путь string) {
	var файл *os.File
	for файл == nil {
		if ctx.Err() != nil {
			return
		}
		открытый, err := os.Open(путь)
		if err == nil {
			файл = открытый
			break
		}
		select {
		case <-ctx.Done():
			return
		case <-таймер(200):
		}
	}
	defer func() { _ = файл.Close() }()

	чтение := bufio.NewReader(файл)
	for {
		if ctx.Err() != nil {
			return
		}
		строка, err := чтение.ReadString('\n')
		if len(строка) > 0 {
			r.записать(обрезатьПеревод(строка))
			continue
		}
		if err != nil && !errors.Is(err, io.EOF) {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-таймер(200):
		}
	}
}

func (r *Runtime) записать(строка string) {
	if строка == "" {
		return
	}
	r.мьютекс.Lock()
	r.журнал.Добавить(строка)
	наблюдатель := r.настройки.Log
	r.мьютекс.Unlock()

	if наблюдатель != nil {
		наблюдатель(строка)
	}
}

// таймер — маленький помощник, чтобы не тащить time во весь файл ради
// двух пауз в цикле чтения журнала.
func таймер(миллисекунд int) <-chan time.Time {
	return time.After(time.Duration(миллисекунд) * time.Millisecond)
}

func обрезатьПеревод(строка string) string {
	return strings.TrimRight(строка, "\r\n")
}
