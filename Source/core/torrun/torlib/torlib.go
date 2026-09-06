//go:build ios && cgo

// Package torlib поднимает tor внутри нашего процесса, вызывая его
// собственный C-интерфейс.
//
// Так сделано ради iOS и только ради неё. На iPhone запуск дочерних
// процессов запрещён совсем: положить рядом с приложением tor и
// запустить его, как мы делаем на Windows и Android, там невозможно.
// Остаётся один путь — прилинковать tor библиотекой и позвать
// tor_run_main из отдельного потока. Именно так работают Onion Browser
// и Orbot для iOS.
//
// Откуда берётся сама библиотека. Собирать tor под Apple нам нечем и
// незачем: проект iCepa выпускает готовый tor.xcframework, внутри
// которого статический архив со всем нужным — tor 0.4.9.11 (та же
// версия, что мы кладём в APK), OpenSSL 3, libevent, liblzma и zlib.
// Скрипт fetch-tor-ios.sh складывает срез для нужной архитектуры в
// lib/libtor.a, а заголовок — в include/. В репозиторий эти файлы не
// попадают: они весят десятки мегабайт и качаются при сборке.
//
// Что важно знать про встроенный tor, прежде чем читать код ниже.
//
// Первое: в одном процессе tor можно запустить только один раз. Это не
// наша осторожность, это написано в tor_api.h: повторный вызов
// tor_run_main после возврата «может привести к падению или странному
// поведению» (ошибка 23847 в их учёте). Поэтому Stop здесь по
// умолчанию tor не убивает — гасить и поднимать заново мы будем не
// процессом, а через control-порт.
//
// Второе: остановить встроенный tor снаружи нечем — сигнала ему не
// пошлёшь, он внутри нас. Единственный опрятный способ, на который
// указывает сам tor_api.h, — отдать ему control-сокет: когда наша
// сторона закрывается, tor завершает работу сам.
package torlib

/*
#cgo CFLAGS: -I${SRCDIR}/include
#cgo LDFLAGS: ${SRCDIR}/lib/libtor.a -lz -lm -framework Security -framework CoreFoundation

#include <stdlib.h>
#include <tor_api.h>

// Массив argv для tor_run_main. Через cgo его не собрать: нужен именно
// char**, живущий до конца работы tor.
static char** новыйМассив(int размер) {
	return calloc(sizeof(char*), размер);
}

static void вписать(char **массив, char *строка, int место) {
	массив[место] = строка;
}

static void освободить(char **массив, int размер) {
	for (int i = 0; i < размер; i++) {
		free(массив[i]);
	}
	free(массив);
}
*/
import "C"

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"sync"

	"github.com/cretz/bine/process"
)

// Available говорит, что встроенный tor в этой сборке есть.
const Available = true

// Version — версия tor, зашитая в библиотеку. Идёт в журнал: без неё
// непонятно, что именно собрано в приложение.
func Version() string {
	return C.GoString(C.tor_api_get_provider_version())
}

// Creator отдаёт tor как process.Creator — тот же интерфейс, которым
// пользуется отдельный процесс. Выше по стеку разницы не видно.
var Creator process.Creator = создатель{}

type создатель struct{}

func (создатель) New(ctx context.Context, args ...string) (process.Process, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	настройки := C.tor_main_configuration_new()
	if настройки == nil {
		return nil, errors.New("tor не отдал настройки: tor_main_configuration_new вернул пусто")
	}
	return &встроенный{ctx: ctx, настройки: настройки, аргументы: args}, nil
}

// встроенный — один запуск tor внутри нашего процесса.
type встроенный struct {
	ctx       context.Context
	настройки *C.tor_main_configuration_t
	аргументы []string

	мьютекс  sync.Mutex
	конец    chan int
	массив   **C.char
	количест C.int
}

var _ process.Process = (*встроенный)(nil)

// Start запускает tor в отдельной горутине и сразу возвращается.
func (в *встроенный) Start() error {
	в.мьютекс.Lock()
	defer в.мьютекс.Unlock()

	if в.конец != nil {
		return errors.New("этот tor уже запущен")
	}

	// Первым аргументом идёт имя программы: tor разбирает командную
	// строку так же, как если бы его позвали из оболочки.
	аргументы := append([]string{"tor"}, в.аргументы...)
	количество := C.int(len(аргументы))
	массив := C.новыйМассив(количество)
	for место, значение := range аргументы {
		C.вписать(массив, C.CString(значение), C.int(место))
	}

	if код := C.tor_main_configuration_set_command_line(
		в.настройки, количество, массив); код != 0 {
		C.освободить(массив, количество)
		C.tor_main_configuration_free(в.настройки)
		return fmt.Errorf("tor не принял аргументы, код %d", int(код))
	}

	в.массив, в.количест = массив, количество
	в.конец = make(chan int, 1)
	конец := в.конец

	go func() {
		// tor_run_main держит поток до самого конца работы tor. Память
		// освобождаем только после возврата: tor_api.h требует, чтобы и
		// argv, и настройки жили всё это время.
		код := int(C.tor_run_main(в.настройки))
		C.освободить(массив, количество)
		C.tor_main_configuration_free(в.настройки)
		конец <- код
	}()
	return nil
}

// Wait ждёт завершения tor.
//
// Отмена ctx возвращает управление, но tor при этом продолжает
// работать: остановить его снаружи нечем, он внутри нашего процесса.
// Это не недоделка, а свойство встроенного tor — см. заглавный
// комментарий пакета.
func (в *встроенный) Wait() error {
	в.мьютекс.Lock()
	конец := в.конец
	в.мьютекс.Unlock()

	if конец == nil {
		return errors.New("tor не запускался")
	}
	select {
	case <-в.ctx.Done():
		return в.ctx.Err()
	case код := <-конец:
		if код == 0 {
			return nil
		}
		return fmt.Errorf("встроенный tor завершился с кодом %d", код)
	}
}

// EmbeddedControlConn отдаёт готовое control-соединение к встроенному
// tor.
//
// Через него же tor узнаёт, когда пора завершаться: закрытие нашей
// стороны для него означает «владелец ушёл». Вызывать до Start и один
// раз — так требует tor_api.h.
func (в *встроенный) EmbeddedControlConn() (net.Conn, error) {
	сокет := C.tor_main_configuration_setup_control_socket(в.настройки)
	if сокет == C.INVALID_TOR_CONTROL_SOCKET {
		return nil, errors.New("tor не отдал control-сокет")
	}
	файл := os.NewFile(uintptr(сокет), "tor-control")
	соединение, err := net.FileConn(файл)
	if err != nil {
		return nil, fmt.Errorf("не вышло сделать соединение из control-сокета: %w", err)
	}
	// Дальше соединением владеет net.Conn; наш файловый дубликат больше
	// не нужен, и держать его — значит держать лишний дескриптор.
	if err := файл.Close(); err != nil {
		return nil, fmt.Errorf("не закрылся дубликат control-сокета: %w", err)
	}
	return соединение, nil
}
