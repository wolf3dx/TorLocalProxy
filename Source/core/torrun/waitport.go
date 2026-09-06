package torrun

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"
)

// СрокControlПорта — сколько ждать, пока tor напишет файл с адресом
// control-порта. Файл появляется в самом конце инициализации, так что
// это же и общий срок запуска.
const СрокControlПорта = 30 * time.Second

// WaitForControlPort ждёт, пока tor напишет файл с адресом
// control-порта, и возвращает этот адрес.
//
// Помощник общий для обеих реализаций: и отдельный процесс, и
// встроенная библиотека сообщают о готовности одинаково — через файл.
// Канал «готов» закрывается, когда tor завершился; журнал отдаёт
// последние строки его вывода, чтобы приложить их к ошибке.
func WaitForControlPort(
	ctx context.Context,
	файлПорта string,
	готов <-chan struct{},
	журнал func() []string,
) (string, error) {
	срок := time.NewTimer(СрокControlПорта)
	defer срок.Stop()
	опрос := time.NewTicker(100 * time.Millisecond)
	defer опрос.Stop()

	for {
		if адрес, err := ЧитатьАдресControl(файлПорта); err == nil {
			return адрес, nil
		}

		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-готов:
			// Последний шанс: файл мог появиться прямо перед завершением.
			if адрес, err := ЧитатьАдресControl(файлПорта); err == nil {
				return адрес, nil
			}
			return "", fmt.Errorf("tor завершился, не открыв control-порт%s", ХвостЖурнала(журнал))
		case <-срок.C:
			return "", fmt.Errorf("tor не открыл control-порт за %s%s",
				СрокControlПорта, ХвостЖурнала(журнал))
		case <-опрос.C:
		}
	}
}

// ЧитатьАдресControl разбирает файл, который tor пишет в конце запуска.
func ЧитатьАдресControl(файлПорта string) (string, error) {
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

// ХвостЖурнала оформляет последние строки вывода tor для сообщения об
// ошибке. Без них «tor завершился» не объясняет ничего.
func ХвостЖурнала(журнал func() []string) string {
	if журнал == nil {
		return ""
	}
	строки := журнал()
	if len(строки) == 0 {
		return ""
	}
	return ".\nЧто сказал tor:\n  " + strings.Join(строки, "\n  ")
}

// Кольцо хранит последние строки вывода tor. Обе реализации ведут такой
// журнал: по коду возврата причина отказа не видна, а по последним
// строкам — почти всегда.
type Кольцо struct {
	Предел int
	данные []string
}

func (к *Кольцо) Добавить(строка string) {
	предел := к.Предел
	if предел <= 0 {
		предел = 20
	}
	к.данные = append(к.данные, строка)
	if len(к.данные) > предел {
		к.данные = к.данные[len(к.данные)-предел:]
	}
}

func (к *Кольцо) Строки() []string {
	return append([]string(nil), к.данные...)
}

func (к *Кольцо) Очистить() {
	к.данные = nil
}
