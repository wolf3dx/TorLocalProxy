//go:build integration

// Живая проверка на настоящей сети: настоящий tor, настоящие мосты
// obfs4, настоящий выход в интернет через поднятый прокси.
//
// За тегом сборки и вдобавок требует переменную окружения с путём к
// файлу мостов — иначе тест пропускается. Строки мостов в репозиторий
// не попадают и в вывод теста не печатаются: файл лежит вне проекта.
//
//	TORPROXY_BRIDGES_FILE="...\мосты.txt" go test -tags integration -run Живая ./core/service/
package service

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"gitlab.com/vkandreevich/torlocalproxy/core/torrun/external"
)

// шумныйНаблюдатель показывает ход подключения в выводе теста — ровно
// то, что увидит пользователь в плашке статуса и в журнале.
type шумныйНаблюдатель struct{ t *testing.T }

func (н шумныйНаблюдатель) OnBootstrap(percent int, phase string) {
	н.t.Logf("  %3d%%  %s", percent, phase)
}
func (н шумныйНаблюдатель) OnLog(line string) { н.t.Logf("  %s", line) }
func (н шумныйНаблюдатель) OnState(state string) {
	н.t.Logf("состояние: %s", state)
}

func TestЖиваяСвязьЧерезМосты(t *testing.T) {
	файл := os.Getenv("TORPROXY_BRIDGES_FILE")
	if файл == "" {
		t.Skip("не задан TORPROXY_BRIDGES_FILE — живая проверка пропущена")
	}
	данные, err := os.ReadFile(файл)
	if err != nil {
		t.Fatalf("файл мостов не прочитался: %v", err)
	}

	// Каталог состояния можно задать снаружи: при разборе отказа нужно
	// заглянуть в torrc и tor.log, а временный каталог теста исчезает.
	//
	// Обычный t.TempDir() тут не годится: он берёт имя из имени теста, а
	// оно по-русски — tor такой путь под Windows не откроет вовсе.
	// Короткое имя 8.3 в torrc помогает, но самого каталога с кириллицей
	// в живой проверке лучше не создавать.
	каталог := os.Getenv("TORPROXY_STATE_DIR")
	if каталог == "" {
		свой, err := os.MkdirTemp("", "torproxy-live-")
		if err != nil {
			t.Fatalf("каталог состояния: %v", err)
		}
		t.Cleanup(func() { _ = os.RemoveAll(свой) })
		каталог = свой
	}
	s, err := New(Options{
		StateDir: каталог,
		Runtime:  external.New(external.Options{}),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = s.Disconnect() })

	ctx, отменить := context.WithTimeout(context.Background(), 4*time.Minute)
	defer отменить()

	начало := time.Now()
	if err := s.Connect(ctx, string(данные), шумныйНаблюдатель{t}); err != nil {
		t.Fatalf("подключиться не удалось: %v", err)
	}
	t.Logf("подключение заняло %s", time.Since(начало).Round(time.Second))

	адрес := s.SocksAddress()
	if адрес == "" {
		t.Fatal("прокси не сообщил адрес")
	}
	t.Logf("прокси SOCKS5: %s — это и вписывается в чужие приложения", адрес)

	// Главная проверка: через прокси действительно ходит трафик, и
	// выходит он из сети Tor.
	ip, черезTor, err := проверитьВыход(ctx, адрес)
	if err != nil {
		t.Fatalf("через прокси не удалось выйти в сеть: %v", err)
	}
	t.Logf("внешний адрес: %s, сеть Tor: %v", ip, черезTor)
	if !черезTor {
		t.Errorf("трафик пошёл мимо Tor")
	}

	// Смена цепочки — кнопка «новая цепочка» в приложении.
	if err := s.NewIdentity(ctx); err != nil {
		t.Errorf("NewIdentity: %v", err)
	}

	if err := s.Disconnect(); err != nil {
		t.Errorf("Disconnect: %v", err)
	}
	if s.State() != StateIdle {
		t.Errorf("после отключения состояние %q", s.State())
	}
}

// проверитьВыход спрашивает у check.torproject.org, кто мы снаружи.
// Ходит строго через наш прокси.
func проверитьВыход(ctx context.Context, адресSocks string) (string, bool, error) {
	клиент := &http.Client{
		Timeout: 90 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, цель string) (net.Conn, error) {
				return черезSocks5(ctx, адресSocks, цель)
			},
		},
	}

	запрос, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://check.torproject.org/api/ip", nil)
	if err != nil {
		return "", false, err
	}
	ответ, err := клиент.Do(запрос)
	if err != nil {
		return "", false, err
	}
	defer ответ.Body.Close()

	тело, err := io.ReadAll(io.LimitReader(ответ.Body, 4096))
	if err != nil {
		return "", false, err
	}
	var разобранное struct {
		IsTor bool   `json:"IsTor"`
		IP    string `json:"IP"`
	}
	if err := json.Unmarshal(тело, &разобранное); err != nil {
		return "", false, fmt.Errorf("непонятный ответ: %s", strings.TrimSpace(string(тело)))
	}
	return разобранное.IP, разобранное.IsTor, nil
}

// черезSocks5 открывает соединение до цели через SOCKS5 без
// аутентификации. Имя хоста уходит на сторону прокси нетронутым: решать
// его локально нельзя, DNS-запрос ушёл бы мимо Tor и выдал, куда мы идём.
func черезSocks5(ctx context.Context, прокси, цель string) (net.Conn, error) {
	соединение, err := (&net.Dialer{}).DialContext(ctx, "tcp", прокси)
	if err != nil {
		return nil, err
	}
	успех := false
	defer func() {
		if !успех {
			_ = соединение.Close()
		}
	}()
	if срок, есть := ctx.Deadline(); есть {
		_ = соединение.SetDeadline(срок)
	}

	// Приветствие: версия 5, один способ — без аутентификации.
	if _, err := соединение.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		return nil, err
	}
	ответ := make([]byte, 2)
	if _, err := io.ReadFull(соединение, ответ); err != nil {
		return nil, err
	}
	if ответ[0] != 0x05 || ответ[1] != 0x00 {
		return nil, fmt.Errorf("прокси не принял приветствие: %v", ответ)
	}

	хост, порт, err := net.SplitHostPort(цель)
	if err != nil {
		return nil, err
	}
	номер, err := net.LookupPort("tcp", порт)
	if err != nil {
		return nil, err
	}
	if len(хост) > 255 {
		return nil, fmt.Errorf("слишком длинное имя хоста: %q", хост)
	}

	// Адрес передаётся типом 0x03 — доменное имя.
	запрос := []byte{0x05, 0x01, 0x00, 0x03, byte(len(хост))}
	запрос = append(запрос, хост...)
	запрос = binary.BigEndian.AppendUint16(запрос, uint16(номер))
	if _, err := соединение.Write(запрос); err != nil {
		return nil, err
	}

	голова := make([]byte, 4)
	if _, err := io.ReadFull(соединение, голова); err != nil {
		return nil, err
	}
	if голова[1] != 0x00 {
		return nil, fmt.Errorf("прокси отказал, код %d", голова[1])
	}
	// Дочитываем адрес, который вернул прокси: его длина зависит от типа.
	switch голова[3] {
	case 0x01:
		_, err = io.ReadFull(соединение, make([]byte, 4+2))
	case 0x03:
		длина := make([]byte, 1)
		if _, err = io.ReadFull(соединение, длина); err == nil {
			_, err = io.ReadFull(соединение, make([]byte, int(длина[0])+2))
		}
	case 0x04:
		_, err = io.ReadFull(соединение, make([]byte, 16+2))
	default:
		err = fmt.Errorf("непонятный тип адреса в ответе: %d", голова[3])
	}
	if err != nil {
		return nil, err
	}

	// Дальше соединением распоряжается вызывающий, срок снимаем.
	_ = соединение.SetDeadline(time.Time{})
	успех = true
	return соединение, nil
}
