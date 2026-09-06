package httpproxy_test

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"gitlab.com/vkandreevich/torlocalproxy/core/httpproxy"
)

// Проверяем главное: прокси разговаривает по HTTP снаружи, а наружу
// ходит только через SOCKS5, и имя узла разрешает не сам, а отдаёт
// строкой посреднику. Последнее важнее всего: разреши мы имя сами —
// запрос к DNS ушёл бы мимо тора и выдал, куда идёт пользователь.

func TestТуннельCONNECT(t *testing.T) {
	узел, адресУзла := поднятьУзел(t, "привет из-за туннеля")
	defer func() { _ = узел.Close() }()

	socks := поднятьSocks(t)
	прокси := поднятьПрокси(t, socks.Адрес())

	клиент := черезПрокси(прокси)
	ответ, err := клиент.Get("https://" + адресУзла + "/")
	// Настоящего TLS у поддельного узла нет, поэтому сюда мы приходим с
	// ошибкой рукопожатия — и это нормально: проверяем не TLS, а то,
	// что туннель открылся именно через SOCKS5 и к нужному узлу.
	if err == nil {
		_ = ответ.Body.Close()
	}

	цели := socks.Цели()
	if len(цели) != 1 {
		t.Fatalf("через SOCKS5 прошло целей: %d, ожидалась одна: %v", len(цели), цели)
	}
	if цели[0] != адресУзла {
		t.Errorf("SOCKS5 попросили открыть %q, а ожидалось %q", цели[0], адресУзла)
	}
}

func TestОбычныйHTTP(t *testing.T) {
	узел, адресУзла := поднятьУзел(t, "страница через прокси")
	defer func() { _ = узел.Close() }()

	socks := поднятьSocks(t)
	прокси := поднятьПрокси(t, socks.Адрес())

	клиент := черезПрокси(прокси)
	ответ, err := клиент.Get("http://" + адресУзла + "/страница")
	if err != nil {
		t.Fatalf("запрос через прокси не прошёл: %v", err)
	}
	defer func() { _ = ответ.Body.Close() }()

	тело, err := io.ReadAll(ответ.Body)
	if err != nil {
		t.Fatalf("тело ответа не прочиталось: %v", err)
	}
	if !strings.Contains(string(тело), "страница через прокси") {
		t.Errorf("ответ узла не дошёл до клиента, получено: %q", тело)
	}
	if цели := socks.Цели(); len(цели) != 1 || цели[0] != адресУзла {
		t.Errorf("через SOCKS5 прошли цели %v, ожидалась одна: %q", цели, адресУзла)
	}
}

func TestИмяУзлаНеРазрешаетсяЛокально(t *testing.T) {
	socks := поднятьSocks(t)
	прокси := поднятьПрокси(t, socks.Адрес())

	клиент := черезПрокси(прокси)
	// Имя, которого не существует. Если прокси попытается разрешить его
	// сам, запрос до SOCKS5 не дойдёт — и это будет утечка DNS.
	//
	// Имя нарочно из латиницы: русские буквы в имени узла клиент сам
	// переводит в punycode (xn--...), и проверка превратилась бы в
	// проверку этого перевода, а не нашего поведения.
	ответ, err := клиент.Get("http://no-such-host.onion.invalid/")
	if err == nil {
		_ = ответ.Body.Close()
	}

	цели := socks.Цели()
	if len(цели) != 1 {
		t.Fatalf("через SOCKS5 прошло целей: %d, ожидалась одна: %v", len(цели), цели)
	}
	if цели[0] != "no-such-host.onion.invalid:80" {
		t.Errorf("SOCKS5 получил %q — имя должно уходить строкой, без разрешения на месте",
			цели[0])
	}
}

func TestАдресВписалиВАдреснуюСтроку(t *testing.T) {
	socks := поднятьSocks(t)
	прокси := поднятьПрокси(t, socks.Адрес())

	// Обращение к прокси как к обычному сайту: так бывает, когда адрес
	// вписали не в поле прокси, а в адресную строку браузера.
	ответ, err := http.Get("http://" + прокси.Address() + "/")
	if err != nil {
		t.Fatalf("прокси не ответил: %v", err)
	}
	defer func() { _ = ответ.Body.Close() }()

	if ответ.StatusCode != http.StatusBadRequest {
		t.Errorf("код ответа %d, ожидался %d", ответ.StatusCode, http.StatusBadRequest)
	}
	тело, _ := io.ReadAll(ответ.Body)
	if !strings.Contains(string(тело), "не сайт") {
		t.Errorf("ответ должен объяснять ошибку по-человечески, получено: %q", тело)
	}
	if цели := socks.Цели(); len(цели) != 0 {
		t.Errorf("наружу ничего идти не должно было, а пошло: %v", цели)
	}
}

func TestStopОсвобождаетПорт(t *testing.T) {
	socks := поднятьSocks(t)
	прокси := поднятьПрокси(t, socks.Адрес())
	адрес := прокси.Address()

	if err := прокси.Stop(); err != nil {
		t.Fatalf("остановка не удалась: %v", err)
	}
	// Порт должен освободиться: иначе следующий запуск не поднимется.
	слушатель, err := net.Listen("tcp", адрес)
	if err != nil {
		t.Fatalf("порт %s не освободился: %v", адрес, err)
	}
	_ = слушатель.Close()
}

// ---- обвязка ----------------------------------------------------------

func поднятьПрокси(t *testing.T, socks string) *httpproxy.Server {
	t.Helper()

	прокси := httpproxy.New(httpproxy.Options{
		Socks: socks,
		Log:   func(строка string) { t.Log("прокси: " + строка) },
	})
	if err := прокси.Start(); err != nil {
		t.Fatalf("прокси не поднялся: %v", err)
	}
	t.Cleanup(func() { _ = прокси.Stop() })
	return прокси
}

func черезПрокси(прокси *httpproxy.Server) *http.Client {
	адрес, _ := url.Parse("http://" + прокси.Address())
	return &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			Proxy: http.ProxyURL(адрес),
		},
	}
}

// поднятьУзел — сайт, до которого мы якобы добираемся через тор.
func поднятьУзел(t *testing.T, тело string) (net.Listener, string) {
	t.Helper()

	слушатель, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("узел не поднялся: %v", err)
	}
	go func() {
		_ = http.Serve(слушатель, http.HandlerFunc(
			func(ответ http.ResponseWriter, _ *http.Request) {
				_, _ = fmt.Fprint(ответ, тело)
			}))
	}()
	return слушатель, слушатель.Addr().String()
}

// поддельныйSocks — SOCKS5 ровно в той части, которой пользуется наш
// прокси: без проверки подлинности, только команда CONNECT. Помнит, что
// у него просили открыть, — по этому списку и проверяется отсутствие
// утечки DNS.
type поддельныйSocks struct {
	слушатель net.Listener

	мьютекс sync.Mutex
	цели    []string
}

func поднятьSocks(t *testing.T) *поддельныйSocks {
	t.Helper()

	слушатель, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("поддельный SOCKS5 не поднялся: %v", err)
	}
	с := &поддельныйSocks{слушатель: слушатель}
	go с.принимать()
	t.Cleanup(func() { _ = слушатель.Close() })
	return с
}

func (с *поддельныйSocks) Адрес() string { return с.слушатель.Addr().String() }

func (с *поддельныйSocks) Цели() []string {
	с.мьютекс.Lock()
	defer с.мьютекс.Unlock()
	return append([]string(nil), с.цели...)
}

func (с *поддельныйSocks) принимать() {
	for {
		соединение, err := с.слушатель.Accept()
		if err != nil {
			return
		}
		go func() {
			defer func() { _ = соединение.Close() }()
			if err := с.обслужить(соединение); err != nil &&
				!errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed) {
				return
			}
		}()
	}
}

func (с *поддельныйSocks) обслужить(клиент net.Conn) error {
	чтение := bufio.NewReader(клиент)

	// Приветствие: версия, число способов проверки подлинности, сами способы.
	голова := make([]byte, 2)
	if _, err := io.ReadFull(чтение, голова); err != nil {
		return err
	}
	if голова[0] != 5 {
		return fmt.Errorf("версия SOCKS %d, ожидалась 5", голова[0])
	}
	if _, err := io.CopyN(io.Discard, чтение, int64(голова[1])); err != nil {
		return err
	}
	// «Версия 5, проверка подлинности не нужна».
	if _, err := клиент.Write([]byte{5, 0}); err != nil {
		return err
	}

	// Запрос: версия, команда, запас, тип адреса.
	запрос := make([]byte, 4)
	if _, err := io.ReadFull(чтение, запрос); err != nil {
		return err
	}
	цель, err := прочитатьЦель(чтение, запрос[3])
	if err != nil {
		return err
	}

	с.мьютекс.Lock()
	с.цели = append(с.цели, цель)
	с.мьютекс.Unlock()

	наружу, err := net.DialTimeout("tcp", цель, 5*time.Second)
	if err != nil {
		// 4 — «узел недоступен». Так наш прокси узнаёт об отказе.
		_, _ = клиент.Write([]byte{5, 4, 0, 1, 0, 0, 0, 0, 0, 0})
		return err
	}
	defer func() { _ = наружу.Close() }()

	if _, err := клиент.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0}); err != nil {
		return err
	}

	готово := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(наружу, чтение); готово <- struct{}{} }()
	go func() { _, _ = io.Copy(клиент, наружу); готово <- struct{}{} }()
	<-готово
	return nil
}

func прочитатьЦель(чтение *bufio.Reader, тип byte) (string, error) {
	var узел string
	switch тип {
	case 1: // IPv4
		адрес := make([]byte, 4)
		if _, err := io.ReadFull(чтение, адрес); err != nil {
			return "", err
		}
		узел = net.IP(адрес).String()
	case 3: // имя
		длина, err := чтение.ReadByte()
		if err != nil {
			return "", err
		}
		имя := make([]byte, длина)
		if _, err := io.ReadFull(чтение, имя); err != nil {
			return "", err
		}
		узел = string(имя)
	case 4: // IPv6
		адрес := make([]byte, 16)
		if _, err := io.ReadFull(чтение, адрес); err != nil {
			return "", err
		}
		узел = net.IP(адрес).String()
	default:
		return "", fmt.Errorf("неизвестный тип адреса: %d", тип)
	}

	порт := make([]byte, 2)
	if _, err := io.ReadFull(чтение, порт); err != nil {
		return "", err
	}
	return net.JoinHostPort(узел, fmt.Sprint(binary.BigEndian.Uint16(порт))), nil
}
