package control

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Bootstrap — один шаг подключения к сети Tor.
type Bootstrap struct {
	Percent int    // 0..100
	Tag     string // машинный тег фазы, например conn_done_pt
	Phase   string // то же по-русски, для показа человеку
}

// PhaseNames переводит теги фаз bootstrap на русский. Таблица перенесена
// из прототипа (tormgr.py, BOOTSTRAP_TAGS): tor присылает и SUMMARY, но
// он английский, а показывать пользователю надо на его языке.
var PhaseNames = map[string]string{
	"starting":               "Запуск",
	"conn_pt":                "Подключение к мосту",
	"conn_done_pt":           "Мост ответил",
	"conn_proxy":             "Подключение через прокси",
	"conn_done_proxy":        "Прокси ответил",
	"conn":                   "Подключение к сети Tor",
	"conn_done":              "Соединение установлено",
	"handshake":              "Согласование шифрования",
	"handshake_done":         "Шифрование согласовано",
	"onehop_create":          "Создание служебной цепочки",
	"requesting_status":      "Запрос состояния сети",
	"loading_status":         "Загрузка состояния сети",
	"loading_keys":           "Загрузка ключей",
	"requesting_descriptors": "Запрос описаний узлов",
	"loading_descriptors":    "Загрузка описаний узлов",
	"enough_dirinfo":         "Каталог узлов получен",
	"ap_conn_pt":             "Строим цепочку через мост",
	"ap_conn_done_pt":        "Цепочка через мост поднята",
	"ap_conn":                "Строим цепочку",
	"ap_conn_done":           "Цепочка построена",
	"ap_handshake":           "Согласование в цепочке",
	"ap_handshake_done":      "Цепочка согласована",
	"circuit_create":         "Создание рабочей цепочки",
	"done":                   "Готово — Tor подключён",
}

// ParseBootstrap разбирает строку состояния bootstrap. Одна и та же форма
// приходит и событием STATUS_CLIENT, и ответом на
// GETINFO status/bootstrap-phase:
//
//	NOTICE BOOTSTRAP PROGRESS=25 TAG=requesting_status SUMMARY="Asking for ..."
//
// Второе значение — false, если строка не про bootstrap.
//
// Разбор свой, а не из bine: тамошний ParseStatusEvent режет аргументы по
// пробелам и портит SUMMARY, у которого пробелы внутри кавычек.
func ParseBootstrap(raw string) (Bootstrap, bool) {
	if !strings.Contains(raw, "BOOTSTRAP") {
		return Bootstrap{}, false
	}

	var b Bootstrap
	процентНайден := false
	for _, слово := range strings.Fields(raw) {
		switch {
		case strings.HasPrefix(слово, "PROGRESS="):
			значение, err := strconv.Atoi(strings.TrimPrefix(слово, "PROGRESS="))
			if err != nil {
				continue
			}
			b.Percent = значение
			процентНайден = true
		case strings.HasPrefix(слово, "TAG="):
			b.Tag = strings.TrimPrefix(слово, "TAG=")
		}
	}
	if !процентНайден {
		return Bootstrap{}, false
	}

	b.Phase = PhaseName(b.Tag, вКавычкахПосле(raw, "SUMMARY="))
	return b, true
}

// PhaseName выбирает, что показать человеку: свой перевод, английский
// SUMMARY от tor или, если нет ничего, сам тег.
func PhaseName(tag, summary string) string {
	if имя, есть := PhaseNames[tag]; есть {
		return имя
	}
	if summary != "" {
		return summary
	}
	return tag
}

// вКавычкахПосле достаёт значение key="..." целиком, вместе с пробелами
// внутри кавычек.
func вКавычкахПосле(raw, key string) string {
	начало := strings.Index(raw, key+`"`)
	if начало < 0 {
		return ""
	}
	остаток := raw[начало+len(key)+1:]
	конец := strings.Index(остаток, `"`)
	if конец < 0 {
		return ""
	}
	return остаток[:конец]
}

// StallTimeoutDefault — сколько ждать движения процента, прежде чем счесть
// подключение зависшим. Значение из прототипа.
const StallTimeoutDefault = 90 * time.Second

// StallError — прогресс перестал двигаться. Отдельный тип, потому что по
// нему принимается решение в интерфейсе: предложить сменить мосты.
type StallError struct {
	Percent int
	Phase   string
	After   time.Duration
}

func (e *StallError) Error() string {
	return fmt.Sprintf("подключение зависло на %d%% (%s) — %s",
		e.Percent, e.описание(), StallHint(e.Percent))
}

func (e *StallError) описание() string {
	if e.Phase == "" {
		return "без описания"
	}
	return e.Phase
}

// StallHint объясняет, что означает застревание на этом проценте.
// Наблюдение из прототипа: место остановки довольно точно указывает на
// причину, и без подсказки пользователь смотрит на неподвижную полосу и
// не понимает, что делать (ЛОГИКА.md, раздел 4).
func StallHint(percent int) string {
	switch {
	case percent < 5:
		return "tor не начал подключение; проверьте, что мосты вообще заданы"
	case percent < 15:
		return "мост не отвечает: скорее всего он заблокирован или нерабочий, запросите новые"
	case percent < 80:
		return "мост отвечает, но сеть режет трафик; попробуйте другой транспорт"
	default:
		return "сеть отвечает слишком медленно; попробуйте ещё раз или смените мосты"
	}
}

// ErrBootstrapChannelClosed — источник событий закрылся раньше 100 %.
// Обычно это значит, что tor завершился.
var ErrBootstrapChannelClosed = errors.New("поток событий bootstrap оборвался до 100 %")

// WaitOptions настраивает Wait.
type WaitOptions struct {
	// Timeout — общий срок на подключение. 0 — не ограничивать.
	Timeout time.Duration
	// StallTimeout — сколько терпеть неподвижный процент.
	// 0 — StallTimeoutDefault.
	StallTimeout time.Duration
}

// Wait читает события до 100 % и возвращает последнее увиденное состояние.
//
// Ошибка приходит в трёх случаях: процент перестал двигаться (*StallError),
// вышел общий срок, или поток событий закрылся. Повторные события с тем же
// процентом таймер застревания не сбрасывают — иначе tor, бодро шлющий
// «всё ещё 25 %», выглядел бы живым сколь угодно долго.
func Wait(ctx context.Context, events <-chan Bootstrap, opts WaitOptions) (Bootstrap, error) {
	срокЗастревания := opts.StallTimeout
	if срокЗастревания <= 0 {
		срокЗастревания = StallTimeoutDefault
	}

	общийСрок := make(<-chan time.Time)
	if opts.Timeout > 0 {
		таймер := time.NewTimer(opts.Timeout)
		defer таймер.Stop()
		общийСрок = таймер.C
	}

	застревание := time.NewTimer(срокЗастревания)
	defer застревание.Stop()

	var последнее Bootstrap
	for {
		select {
		case <-ctx.Done():
			return последнее, ctx.Err()

		case <-общийСрок:
			return последнее, fmt.Errorf(
				"не удалось подключиться за %s (остановились на %d%%): %s",
				opts.Timeout, последнее.Percent, StallHint(последнее.Percent))

		case <-застревание.C:
			return последнее, &StallError{
				Percent: последнее.Percent,
				Phase:   последнее.Phase,
				After:   срокЗастревания,
			}

		case шаг, открыт := <-events:
			if !открыт {
				if последнее.Percent >= 100 {
					return последнее, nil
				}
				return последнее, ErrBootstrapChannelClosed
			}
			двинулось := шаг.Percent != последнее.Percent
			последнее = шаг
			if шаг.Percent >= 100 {
				return последнее, nil
			}
			if двинулось {
				if !застревание.Stop() {
					select {
					case <-застревание.C:
					default:
					}
				}
				застревание.Reset(срокЗастревания)
			}
		}
	}
}
