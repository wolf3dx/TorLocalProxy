package ptrun

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSupportedСодержитObfs4(t *testing.T) {
	if !slices.Contains(Supported(), "obfs4") {
		t.Errorf("obfs4 нет в списке поддерживаемых: %v", Supported())
	}
}

func TestНеизвестныйТранспортОтклоняется(t *testing.T) {
	бегун := New(Options{})
	t.Cleanup(func() { _ = бегун.Stop() })

	_, err := бегун.Start([]string{"quantumfoo"}, t.TempDir())
	if err == nil {
		t.Fatal("ожидался отказ на неизвестном транспорте")
	}
	// В тексте должно быть видно, что вообще доступно, — иначе
	// пользователь не поймёт, что не так с его мостами.
	if !strings.Contains(err.Error(), "obfs4") {
		t.Errorf("ошибка не перечисляет доступные транспорты: %v", err)
	}
}

func TestБезКаталогаСостоянияОтказ(t *testing.T) {
	бегун := New(Options{})
	if _, err := бегун.Start([]string{"obfs4"}, ""); err == nil {
		t.Error("obfs4 хранит состояние на диске, без каталога запускаться нельзя")
	}
}

func TestПустойСписокНичегоНеПоднимает(t *testing.T) {
	бегун := New(Options{})
	плагины, err := бегун.Start(nil, t.TempDir())
	if err != nil {
		t.Fatalf("Start без транспортов: %v", err)
	}
	if len(плагины) != 0 {
		t.Errorf("поднято %d плагинов, ожидалось ноль", len(плагины))
	}
}

func TestObfs4ПоднимаетсяИСлушает(t *testing.T) {
	var мьютекс sync.Mutex
	var журнал []string
	бегун := New(Options{Log: func(с string) {
		мьютекс.Lock()
		defer мьютекс.Unlock()
		журнал = append(журнал, с)
	}})
	t.Cleanup(func() { _ = бегун.Stop() })

	плагины, err := бегун.Start([]string{"obfs4"}, t.TempDir())
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if len(плагины) != 1 {
		t.Fatalf("поднято %d плагинов, ожидался один", len(плагины))
	}
	плагин := плагины[0]
	if !slices.Equal(плагин.Transports, []string{"obfs4"}) {
		t.Errorf("транспорты плагина = %v", плагин.Transports)
	}
	// Адрес уйдёт в torrc строкой ClientTransportPlugin ... socks5 <адрес>.
	if !strings.HasPrefix(плагин.Address, "127.0.0.1:") {
		t.Errorf("адрес = %q, ожидался локальный", плагин.Address)
	}

	соединение, err := net.DialTimeout("tcp", плагин.Address, 3*time.Second)
	if err != nil {
		t.Fatalf("транспорт не слушает объявленный адрес: %v", err)
	}
	_ = соединение.Close()

	мьютекс.Lock()
	записано := strings.Join(журнал, "\n")
	мьютекс.Unlock()
	if !strings.Contains(записано, "obfs4") {
		t.Errorf("в журнал не попало ничего про транспорт:\n%s", записано)
	}
}

func TestStopЗакрываетПортИПовторяем(t *testing.T) {
	бегун := New(Options{})
	плагины, err := бегун.Start([]string{"obfs4"}, t.TempDir())
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	адрес := плагины[0].Address

	if err := бегун.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if соединение, err := net.DialTimeout("tcp", адрес, time.Second); err == nil {
		_ = соединение.Close()
		t.Errorf("после Stop порт %s всё ещё принимает соединения", адрес)
	}

	// Stop зовут и из обработчиков ошибок, где состояние неизвестно.
	if err := бегун.Stop(); err != nil {
		t.Errorf("повторный Stop: %v", err)
	}

	// И после остановки должно быть можно подняться заново: пользователь
	// нажимает «Подключиться» второй раз.
	if _, err := бегун.Start([]string{"obfs4"}, t.TempDir()); err != nil {
		t.Errorf("повторный Start: %v", err)
	}
	_ = бегун.Stop()
}

func TestПовторныйStartБезStopОтклоняется(t *testing.T) {
	бегун := New(Options{})
	t.Cleanup(func() { _ = бегун.Stop() })

	if _, err := бегун.Start([]string{"obfs4"}, t.TempDir()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := бегун.Start([]string{"obfs4"}, t.TempDir()); err == nil {
		t.Error("второй Start без Stop должен отказать")
	}
}

// ------------------------------------------------- разговор по SOCKS --

// Дальше проверяется то, ради чего пакет и существует: tor приходит по
// SOCKS5, передаёт параметры моста в полях аутентификации, а мы должны
// разобрать их и внятно отказать, если мост не отвечает.

func TestНегодныеПараметрыМостаОтклоняются(t *testing.T) {
	бегун := New(Options{})
	t.Cleanup(func() { _ = бегун.Stop() })
	плагины, err := бегун.Start([]string{"obfs4"}, t.TempDir())
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Без cert= obfs4 не может даже начать: это должно кончиться отказом,
	// а не зависанием и не падением слушателя.
	ответ := попроситьЧерезSocks(t, плагины[0].Address, "iat-mode=0", "192.0.2.1:9443")
	if ответ == 0 {
		t.Error("мост без cert= не должен приниматься")
	}
}

func TestМолчащийМостДаётОтказАНеЗависание(t *testing.T) {
	бегун := New(Options{})
	t.Cleanup(func() { _ = бегун.Stop() })
	плагины, err := бегун.Start([]string{"obfs4"}, t.TempDir())
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Порт заведомо закрыт: соединение отвергается сразу, без ожидания.
	слушатель, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	мёртвый := слушатель.Addr().String()
	_ = слушатель.Close()

	ответ := попроситьЧерезSocks(t, плагины[0].Address, "cert="+правдоподобныйCert(t)+";iat-mode=0", мёртвый)
	if ответ == 0 {
		t.Error("недоступный мост не должен подтверждаться успехом")
	}

	// И слушатель обязан остаться живым: один плохой мост не должен
	// ронять транспорт целиком.
	соединение, err := net.DialTimeout("tcp", плагины[0].Address, 3*time.Second)
	if err != nil {
		t.Fatalf("после неудачи транспорт перестал слушать: %v", err)
	}
	_ = соединение.Close()
}

// правдоподобныйCert собирает cert правильной формы: obfs4 ждёт base64
// от 52 байт — 20 байт идентификатора узла и 32 байта открытого ключа.
// Ключ ненастоящий, но до проверки ключа дело и не доходит.
func правдоподобныйCert(t *testing.T) string {
	t.Helper()
	сырые := make([]byte, 52)
	if _, err := rand.Read(сырые); err != nil {
		t.Fatal(err)
	}
	return base64.RawStdEncoding.EncodeToString(сырые)
}

// попроситьЧерезSocks изображает tor: здоровается по SOCKS5, передаёт
// параметры моста в имени пользователя и просит соединение с целью.
// Возвращает код ответа, ноль — успех.
func попроситьЧерезSocks(t *testing.T, адресТранспорта, параметры, цель string) byte {
	t.Helper()

	соединение, err := net.DialTimeout("tcp", адресТранспорта, 5*time.Second)
	if err != nil {
		t.Fatalf("транспорт не отвечает: %v", err)
	}
	defer func() { _ = соединение.Close() }()
	_ = соединение.SetDeadline(time.Now().Add(20 * time.Second))

	// Приветствие: версия 5, один способ — имя с паролем (0x02).
	записать(t, соединение, []byte{0x05, 0x01, 0x02})
	прочитать(t, соединение, 2)

	// Параметры моста едут в имени пользователя (RFC 1929).
	имя := []byte(параметры)
	if len(имя) > 255 {
		t.Fatalf("параметры не влезают в одно поле: %d байт", len(имя))
	}
	запрос := append([]byte{0x01, byte(len(имя))}, имя...)
	запрос = append(запрос, 0x01, 0x00) // пароль из одного нулевого байта
	записать(t, соединение, запрос)
	прочитать(t, соединение, 2)

	// CONNECT к цели, адрес доменным именем (0x03) — так же его передаёт tor.
	хост, порт := разделить(t, цель)
	команда := []byte{0x05, 0x01, 0x00, 0x03, byte(len(хост))}
	команда = append(команда, хост...)
	команда = binary.BigEndian.AppendUint16(команда, порт)
	записать(t, соединение, команда)

	ответ := прочитать(t, соединение, 10)
	return ответ[1]
}

func записать(t *testing.T, соединение net.Conn, данные []byte) {
	t.Helper()
	if _, err := соединение.Write(данные); err != nil {
		t.Fatalf("запись в транспорт: %v", err)
	}
}

func прочитать(t *testing.T, соединение net.Conn, сколько int) []byte {
	t.Helper()
	буфер := make([]byte, сколько)
	if _, err := io.ReadFull(соединение, буфер); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			t.Fatalf("транспорт закрыл соединение, не ответив: %v", err)
		}
		t.Fatalf("чтение ответа: %v", err)
	}
	return буфер
}

func разделить(t *testing.T, адрес string) ([]byte, uint16) {
	t.Helper()
	хост, порт, err := net.SplitHostPort(адрес)
	if err != nil {
		t.Fatalf("адрес %q: %v", адрес, err)
	}
	номер, err := net.LookupPort("tcp", порт)
	if err != nil {
		t.Fatalf("порт %q: %v", порт, err)
	}
	return []byte(хост), uint16(номер)
}
