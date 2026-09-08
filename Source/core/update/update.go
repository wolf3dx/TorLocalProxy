// Package update проверяет, вышла ли новая версия приложения, спрашивая
// об этом хостинг выпусков. Само обновление (скачивание, замена, запуск)
// пакет не делает: это платформозависимо и живёт в приложении. Здесь —
// только «есть ли что новее и где это взять».
//
// Источник зависит от платформы сборки, а не задаётся здесь: Windows и
// Android спрашивают GitLab (основной репозиторий), iOS и macOS — GitHub
// (туда CI кладёт сборки под Apple). Приложение передаёт нужный Source.
//
// Пакет платформо-независим и ходит по HTTP через переданный клиент —
// это позволяет и проверять его без сети (httptest), и пускать проверку
// через SOCKS тора, когда прокси поднят.
package update

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// Kind — вид хостинга. От него зависит формат API.
type Kind string

const (
	GitHub Kind = "github"
	GitLab Kind = "gitlab"
)

// Source описывает, где и что искать.
type Source struct {
	Kind Kind
	// Owner/Repo — владелец и имя репозитория. Для GitLab Owner — это
	// группа/пользователь, Repo — проект; вместе они кодируются в путь.
	Owner string
	Repo  string
	// TagPrefix отбирает выпуски нужной платформы: у проекта в одном
	// репозитории соседствуют выпуски под разные системы, и различаются
	// они префиксом тега — "windows-v", "android-v", "ios-v", "macos-v".
	TagPrefix string
	// AssetSuffix — по какому окончанию имени узнаётся нужный файл:
	// ".zip", ".apk", ".ipa", ".dmg". Файл контрольной суммы ищется как
	// тот же плюс ".sha256".
	AssetSuffix string
	// BaseURL переопределяет адрес API. Пусто — берётся стандартный для
	// вида хостинга. Существует ради тестов и self-hosted установок.
	BaseURL string
}

// Available — найденная версия, которая новее текущей.
type Available struct {
	Version   string // "0.0.6"
	Notes     string // описание выпуска, как показать человеку
	AssetURL  string // откуда качать пакет
	SHA256URL string // откуда взять его контрольную сумму (пусто, если нет)
	Page      string // страница выпуска для платформ, где ставят вручную
}

// выпуск — общий вид выпуска после разбора ответа любого хостинга.
type выпуск struct {
	tag    string
	notes  string
	page   string
	assets map[string]string // имя файла -> ссылка на скачивание
}

// Check спрашивает хостинг о выпусках нужной платформы и возвращает
// самый свежий, если он новее current. Если новее ничего нет — (nil,
// nil). Ошибка — только если спросить не удалось (нет сети, отказ API).
func Check(ctx context.Context, клиент *http.Client, ист Source, current string) (*Available, error) {
	выпуски, err := получитьВыпуски(ctx, клиент, ист)
	if err != nil {
		return nil, err
	}

	var лучший *выпуск
	var лучшаяВерсия string
	for i := range выпуски {
		в := &выпуски[i]
		if !strings.HasPrefix(в.tag, ист.TagPrefix) {
			continue
		}
		версия := strings.TrimPrefix(в.tag, ист.TagPrefix)
		if !Новее(current, версия) {
			continue
		}
		if лучший == nil || Новее(лучшаяВерсия, версия) {
			лучший = в
			лучшаяВерсия = версия
		}
	}
	if лучший == nil {
		return nil, nil
	}

	доступно := &Available{
		Version: лучшаяВерсия,
		Notes:   лучший.notes,
		Page:    лучший.page,
	}
	for имя, ссылка := range лучший.assets {
		switch {
		case strings.HasSuffix(имя, ист.AssetSuffix+".sha256"):
			доступно.SHA256URL = ссылка
		case strings.HasSuffix(имя, ист.AssetSuffix):
			доступно.AssetURL = ссылка
		}
	}
	return доступно, nil
}

// Новее сообщает, строго ли kandidat новее, чем current. Версии — набор
// чисел через точку ("0.0.6"). Необязательный ведущий "v" и суффиксы
// после дефиса ("0.0.6-rc1") отбрасываются: сравниваются только числа.
func Новее(current, kandidat string) bool {
	return сравнить(разобрать(kandidat), разобрать(current)) > 0
}

// разобрать превращает "v0.0.6-rc1" в [0 0 6]. Всё после первого дефиса
// и любой ведущий не-цифровой мусор игнорируются.
func разобрать(s string) []int {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '-'); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimPrefix(s, "v")
	var числа []int
	for _, часть := range strings.Split(s, ".") {
		n, err := strconv.Atoi(strings.TrimSpace(часть))
		if err != nil {
			break
		}
		числа = append(числа, n)
	}
	return числа
}

// сравнить возвращает 1, 0 или -1. Недостающие разряды считаются нулями:
// "0.1" и "0.1.0" равны.
func сравнить(a, b []int) int {
	n := len(a)
	if len(b) > n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		var x, y int
		if i < len(a) {
			x = a[i]
		}
		if i < len(b) {
			y = b[i]
		}
		switch {
		case x > y:
			return 1
		case x < y:
			return -1
		}
	}
	return 0
}

func получитьВыпуски(ctx context.Context, клиент *http.Client, ист Source) ([]выпуск, error) {
	switch ист.Kind {
	case GitHub:
		return получитьGitHub(ctx, клиент, ист)
	case GitLab:
		return получитьGitLab(ctx, клиент, ист)
	default:
		return nil, fmt.Errorf("неизвестный вид хостинга: %q", ист.Kind)
	}
}

func получитьGitHub(ctx context.Context, клиент *http.Client, ист Source) ([]выпуск, error) {
	база := ист.BaseURL
	if база == "" {
		база = "https://api.github.com"
	}
	адрес := fmt.Sprintf("%s/repos/%s/%s/releases?per_page=100", база, ист.Owner, ист.Repo)

	var сырые []struct {
		TagName string `json:"tag_name"`
		Body    string `json:"body"`
		HTMLURL string `json:"html_url"`
		Assets  []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := получитьJSON(ctx, клиент, адрес, &сырые); err != nil {
		return nil, err
	}

	выпуски := make([]выпуск, 0, len(сырые))
	for _, r := range сырые {
		в := выпуск{tag: r.TagName, notes: r.Body, page: r.HTMLURL, assets: map[string]string{}}
		for _, a := range r.Assets {
			в.assets[a.Name] = a.URL
		}
		выпуски = append(выпуски, в)
	}
	return выпуски, nil
}

func получитьGitLab(ctx context.Context, клиент *http.Client, ист Source) ([]выпуск, error) {
	база := ист.BaseURL
	if база == "" {
		база = "https://gitlab.com"
	}
	// GitLab кодирует "владелец/проект" в один сегмент пути.
	проект := url.PathEscape(ист.Owner + "/" + ист.Repo)
	адрес := fmt.Sprintf("%s/api/v4/projects/%s/releases?per_page=100", база, проект)

	var сырые []struct {
		TagName     string `json:"tag_name"`
		Description string `json:"description"`
		Links       struct {
			Self string `json:"self"`
		} `json:"_links"`
		Assets struct {
			Links []struct {
				Name string `json:"name"`
				URL  string `json:"url"`
			} `json:"links"`
		} `json:"assets"`
	}
	if err := получитьJSON(ctx, клиент, адрес, &сырые); err != nil {
		return nil, err
	}

	выпуски := make([]выпуск, 0, len(сырые))
	for _, r := range сырые {
		в := выпуск{tag: r.TagName, notes: r.Description, page: r.Links.Self, assets: map[string]string{}}
		for _, a := range r.Assets.Links {
			в.assets[a.Name] = a.URL
		}
		выпуски = append(выпуски, в)
	}
	return выпуски, nil
}

func получитьJSON(ctx context.Context, клиент *http.Client, адрес string, куда any) error {
	if клиент == nil {
		клиент = http.DefaultClient
	}
	запрос, err := http.NewRequestWithContext(ctx, http.MethodGet, адрес, nil)
	if err != nil {
		return err
	}
	запрос.Header.Set("Accept", "application/json")
	ответ, err := клиент.Do(запрос)
	if err != nil {
		return err
	}
	defer func() { _ = ответ.Body.Close() }()

	if ответ.StatusCode != http.StatusOK {
		return fmt.Errorf("хостинг ответил %s", ответ.Status)
	}
	тело, err := io.ReadAll(io.LimitReader(ответ.Body, 1<<20))
	if err != nil {
		return err
	}
	return json.Unmarshal(тело, куда)
}
