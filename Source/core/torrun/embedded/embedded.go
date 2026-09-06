//go:build cgo && (android || linux || darwin)

// Package embedded поднимает tor библиотекой внутри нашего процесса.
//
// Так работает мобильная поставка. На Android нельзя положить рядом с
// приложением посторонний исполняемый файл и запустить его, а на iOS
// дочерние процессы запрещены совсем (ЛОГИКА.md, раздел 2.1). Тот же
// интерфейс torrun.Runtime, что и у отдельного процесса, — вызывающий
// разницы не видит.
//
// Внутри — berty.tech/go-libtor: настоящий tor, собранный статически
// вместе с OpenSSL, libevent и zlib. Отсюда требования к сборке:
//
//	CGO_ENABLED=1
//	-tags "staticOpenssl,staticZlib,staticLibevent"
//
// Без тегов исходники OpenSSL остаются за бортом, и сборка падает на
// «openssl/opensslv.h file not found». Под Windows этой реализации нет
// вовсе — там работает external.
package embedded

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"berty.tech/go-libtor"
	"github.com/cretz/bine/process"

	"gitlab.com/vkandreevich/torlocalproxy/core/torconf"
	"gitlab.com/vkandreevich/torlocalproxy/core/torrun"
)

// Available говорит, собрана ли в это приложение встроенная реализация.
const Available = true

// строкЖурнала — сколько последних строк вывода tor держать для ошибки.
const строкЖурнала = 20

// Options настраивает запуск.
type Options struct {
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
	процесс, err := libtor.Creator.New(жизнь, "-f", файлКонфига)
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

// Stop останавливает tor. Безопасен при повторном вызове и до Start.
func (r *Runtime) Stop() error {
	r.мьютекс.Lock()
	отмена, готов := r.отмена, r.готов
	r.процесс, r.отмена, r.готов = nil, nil, nil
	r.мьютекс.Unlock()

	if отмена == nil {
		return nil
	}
	отмена()
	<-готов
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
