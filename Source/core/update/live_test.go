//go:build integration

// Живая проверка канала обновлений: настоящий GitLab, настоящий выпуск,
// настоящее скачивание и сверка контрольной суммы. За тегом сборки,
// потому что ходит в сеть.
//
//	go test -tags integration -run Живой ./core/update/
package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestЖивойКаналWindows(t *testing.T) {
	ист := Source{
		Kind: GitLab, Owner: "vkandreevich", Repo: "TorLocalProxy",
		TagPrefix: "windows-v", AssetSuffix: ".zip",
	}

	ctx, отмена := context.WithTimeout(context.Background(), 60*time.Second)
	defer отмена()

	// Прикидываемся заведомо старой версией — какое-то обновление обязано
	// найтись, пока на GitLab есть хоть один выпуск windows-v*.
	доступно, err := Check(ctx, http.DefaultClient, ист, "0.0.5")
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if доступно == nil {
		t.Fatal("ни одного выпуска windows-v* не найдено — проверь релизы на GitLab")
	}
	t.Logf("нашлось обновление: версия %s", доступно.Version)
	t.Logf("  пакет: %s", доступно.AssetURL)
	t.Logf("  сумма: %s", доступно.SHA256URL)

	// Точную версию не пиним: канал двигается с каждым выпуском, и тест не
	// должен краснеть после релиза. Проверяем суть — что найденное новее
	// той версии, которой мы прикинулись.
	if !Новее("0.0.5", доступно.Version) {
		t.Errorf("версия %q не новее 0.0.5 — канал отдал не то", доступно.Version)
	}
	if доступно.AssetURL == "" || доступно.SHA256URL == "" {
		t.Fatal("у выпуска нет ссылок на пакет или контрольную сумму")
	}

	// Скачиваем пакет и сверяем sha256 — ровно то, что делает приложение
	// перед заменой.
	факт := скачатьИПосчитать(ctx, t, доступно.AssetURL)
	ожид := первоеСловоЖ(скачатьТекстЖ(ctx, t, доступно.SHA256URL))
	if !strings.EqualFold(факт, ожид) {
		t.Fatalf("контрольная сумма не совпала:\n  посчитали %s\n  в выпуске %s", факт, ожид)
	}
	t.Logf("sha256 совпал: %s", факт)
}

func скачатьИПосчитать(ctx context.Context, t *testing.T, адрес string) string {
	t.Helper()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, адрес, nil)
	ответ, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("скачать пакет: %v", err)
	}
	defer func() { _ = ответ.Body.Close() }()
	if ответ.StatusCode != http.StatusOK {
		t.Fatalf("пакет отдал %s", ответ.Status)
	}
	h := sha256.New()
	if _, err := io.Copy(h, ответ.Body); err != nil {
		t.Fatalf("чтение пакета: %v", err)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func скачатьТекстЖ(ctx context.Context, t *testing.T, адрес string) string {
	t.Helper()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, адрес, nil)
	ответ, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("скачать сумму: %v", err)
	}
	defer func() { _ = ответ.Body.Close() }()
	тело, _ := io.ReadAll(io.LimitReader(ответ.Body, 4096))
	return string(тело)
}

func первоеСловоЖ(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, " \t\r\n"); i >= 0 {
		return s[:i]
	}
	return s
}
