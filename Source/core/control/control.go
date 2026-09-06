// Package control разговаривает с control-портом Tor.
//
// Низкий уровень протокола закрывает github.com/cretz/bine: соединение,
// SAFECOOKIE-аутентификация, разбор ответов. Своего здесь три вещи, ради
// которых пакет и существует: события bootstrap, перевод фаз на русский
// (bootstrap.go) и обнаружение застревания.
//
// Пакет ничего не запускает. Кто поднял tor — отдельный процесс или
// слинкованная библиотека — ему безразлично: на входе адрес
// control-порта, и всё (ЛОГИКА.md, раздел 3).
package control

import (
	"context"
	"fmt"
	"net"
	"net/textproto"
	"strings"
	"sync"
	"time"

	"github.com/cretz/bine/control"
)

// Client — то, что нужно от control-порта остальному ядру.
type Client interface {
	// Authenticate проходит аутентификацию: SAFECOOKIE, cookie или
	// пароль — что предложит tor.
	Authenticate(ctx context.Context) error
	// TakeOwnership привязывает жизнь tor к этому соединению: оборвётся
	// оно — tor завершится сам, а не останется висеть.
	TakeOwnership(ctx context.Context) error
	// WatchBootstrap отдаёт поток событий подключения. Канал закрывается,
	// когда ctx отменён или соединение потеряно.
	WatchBootstrap(ctx context.Context) (<-chan Bootstrap, error)
	// NewIdentity просит новую цепочку (SIGNAL NEWNYM).
	NewIdentity(ctx context.Context) error
	// SocksAddress возвращает адрес SOCKS5, который слушает tor.
	SocksAddress(ctx context.Context) (string, error)
	Close() error
}

// Options — необязательные настройки соединения.
type Options struct {
	// Password для HASHEDPASSWORD. Обычно пусто: мы поднимаем tor сами и
	// пользуемся cookie.
	Password string
	// DialTimeout ограничивает установку соединения. 0 — 15 секунд.
	DialTimeout time.Duration
}

type клиент struct {
	адрес     string
	настройки Options

	мьютекс sync.Mutex
	сокет   net.Conn
	conn    *control.Conn

	закрытие sync.Once
}

// Dial открывает соединение с control-портом. Аутентификация — отдельным
// вызовом: адрес и cookie появляются в разное время, и ошибку связи
// полезно отличать от ошибки доступа.
func Dial(ctx context.Context, address string, opts Options) (Client, error) {
	сокет, conn, err := соединиться(ctx, address, opts)
	if err != nil {
		return nil, err
	}
	return &клиент{адрес: address, настройки: opts, сокет: сокет, conn: conn}, nil
}

func соединиться(ctx context.Context, address string, opts Options) (net.Conn, *control.Conn, error) {
	срок := opts.DialTimeout
	if срок <= 0 {
		срок = 15 * time.Second
	}
	dialer := net.Dialer{Timeout: срок}
	сокет, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, nil, fmt.Errorf("control-порт %s недоступен: %w", address, err)
	}
	return сокет, control.NewConn(textproto.NewConn(сокет)), nil
}

func (к *клиент) Authenticate(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	к.мьютекс.Lock()
	defer к.мьютекс.Unlock()
	if err := к.conn.Authenticate(к.настройки.Password); err != nil {
		return fmt.Errorf("аутентификация на control-порту: %w", err)
	}
	return nil
}

func (к *клиент) TakeOwnership(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	к.мьютекс.Lock()
	defer к.мьютекс.Unlock()
	if err := к.conn.TakeOwnership(); err != nil {
		return fmt.Errorf("TAKEOWNERSHIP: %w", err)
	}
	// Сбрасываем слежение за процессом-владельцем: теперь жизнью tor
	// управляет это соединение, и две проверки сразу только мешают —
	// у встроенной сборки процесса-владельца нет вовсе. Отказ терпим:
	// параметра могло и не быть в torrc.
	_ = к.conn.ResetConf(control.NewKeyVal("__OwningControllerProcess", ""))
	return nil
}

func (к *клиент) NewIdentity(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	к.мьютекс.Lock()
	defer к.мьютекс.Unlock()
	if err := к.conn.Signal("NEWNYM"); err != nil {
		return fmt.Errorf("SIGNAL NEWNYM: %w", err)
	}
	return nil
}

func (к *клиент) SocksAddress(ctx context.Context) (string, error) {
	значение, err := к.getInfo(ctx, "net/listeners/socks")
	if err != nil {
		return "", err
	}
	// tor может слушать несколько адресов и перечисляет их через пробел;
	// нам нужен первый.
	адрес := strings.Trim(strings.Fields(значение)[0], `"`)
	return адрес, nil
}

func (к *клиент) getInfo(ctx context.Context, ключ string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	к.мьютекс.Lock()
	defer к.мьютекс.Unlock()
	значения, err := к.conn.GetInfo(ключ)
	if err != nil {
		return "", fmt.Errorf("GETINFO %s: %w", ключ, err)
	}
	for _, kv := range значения {
		if kv.Key == ключ && strings.TrimSpace(kv.Val) != "" {
			return kv.Val, nil
		}
	}
	return "", fmt.Errorf("GETINFO %s: tor ответил пусто", ключ)
}

func (к *клиент) Close() error {
	var err error
	к.закрытие.Do(func() {
		к.мьютекс.Lock()
		defer к.мьютекс.Unlock()
		err = к.conn.Close()
	})
	return err
}

// WatchBootstrap подписывается на STATUS_CLIENT и переводит события в
// поток Bootstrap.
//
// Слушает вторым соединением, а не тем же. Причина в устройстве bine:
// HandleNextEvent держит блокировку чтения, пока ждёт следующего
// сообщения, и любая команда по тому же соединению встала бы в очередь
// до ближайшего события. После 100 % события приходят редко, так что
// NEWNYM мог бы ждать минутами. Два соединения tor разрешает, и это
// дешевле, чем свой цикл чтения поверх чужого.
func (к *клиент) WatchBootstrap(ctx context.Context) (<-chan Bootstrap, error) {
	событийныйСокет, событийныйConn, err := соединиться(ctx, к.адрес, к.настройки)
	if err != nil {
		return nil, fmt.Errorf("второе соединение для событий: %w", err)
	}
	if err := событийныйConn.Authenticate(к.настройки.Password); err != nil {
		_ = событийныйСокет.Close()
		return nil, fmt.Errorf("аутентификация соединения событий: %w", err)
	}

	отBine := make(chan control.Event, 16)
	if err := событийныйConn.AddEventListener(отBine, control.EventCodeStatusClient); err != nil {
		_ = событийныйConn.Close()
		return nil, fmt.Errorf("подписка на STATUS_CLIENT: %w", err)
	}

	наружу := make(chan Bootstrap, 16)

	// Насос событий. Закрытие соединения — единственный способ вывести
	// его из ожидания чтения, поэтому отмена ctx закрывает соединение.
	насосЗакончил := make(chan struct{})
	go func() {
		defer close(насосЗакончил)
		_ = событийныйConn.HandleEvents(ctx)
	}()
	go func() {
		<-ctx.Done()
		_ = событийныйConn.Close()
	}()

	go func() {
		defer close(наружу)
		// Первое состояние берём опросом: события расскажут только о том,
		// что произойдёт дальше, а tor мог уйти вперёд, пока мы шли к нему.
		if текущее, err := к.getInfo(ctx, "status/bootstrap-phase"); err == nil {
			if шаг, ок := ParseBootstrap(текущее); ок {
				select {
				case наружу <- шаг:
				case <-ctx.Done():
					return
				}
			}
		}

		for {
			select {
			case <-ctx.Done():
				return
			case <-насосЗакончил:
				// Соединение закрыто; добираем то, что уже пришло.
				for {
					select {
					case событие := <-отBine:
						if шаг, ок := вBootstrap(событие); ок {
							select {
							case наружу <- шаг:
							default:
							}
						}
					default:
						return
					}
				}
			case событие := <-отBine:
				шаг, ок := вBootstrap(событие)
				if !ок {
					continue
				}
				select {
				case наружу <- шаг:
				case <-ctx.Done():
					return
				}
			}
		}
	}()

	return наружу, nil
}

// вBootstrap достаёт состояние из события STATUS_CLIENT. Разбирается
// сырая строка, а не разобранные bine аргументы: тот режет их по пробелам
// и портит SUMMARY (см. ParseBootstrap).
func вBootstrap(событие control.Event) (Bootstrap, bool) {
	статус, ок := событие.(*control.StatusEvent)
	if !ок {
		return Bootstrap{}, false
	}
	return ParseBootstrap(статус.Raw)
}
