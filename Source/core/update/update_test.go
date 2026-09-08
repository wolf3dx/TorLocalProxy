package update

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestНовее(t *testing.T) {
	случаи := []struct {
		current, kandidat string
		хотим             bool
	}{
		{"0.0.5", "0.0.6", true},
		{"0.0.5", "0.0.5", false},
		{"0.0.6", "0.0.5", false},
		{"0.0.5", "0.1.0", true},
		{"0.9.9", "1.0.0", true},
		{"1.0.0", "0.9.9", false},
		{"0.1", "0.1.0", false},       // равны, недостающий разряд — ноль
		{"0.1.0", "0.1.1", true},      //
		{"v0.0.5", "v0.0.6", true},    // ведущий v не мешает
		{"0.0.6", "0.0.6-rc1", false}, // суффикс отбрасывается, версии равны
		{"0.0.5", "0.0.6-rc1", true},
	}
	for _, с := range случаи {
		if got := Новее(с.current, с.kandidat); got != с.хотим {
			t.Errorf("Новее(%q, %q) = %v, хотели %v", с.current, с.kandidat, got, с.хотим)
		}
	}
}

const образецGitHub = `[
  {"tag_name":"ios-v0.0.7","body":"iOS","html_url":"https://h/ios7",
   "assets":[{"name":"TorLocalProxy-0.0.7-ios.ipa","browser_download_url":"https://h/ios7.ipa"}]},
  {"tag_name":"macos-v0.0.6","body":"Мак 0.0.6","html_url":"https://h/mac6",
   "assets":[
     {"name":"TorLocalProxy-0.0.6-macos.dmg","browser_download_url":"https://h/mac6.dmg"},
     {"name":"TorLocalProxy-0.0.6-macos.dmg.sha256","browser_download_url":"https://h/mac6.sha256"}]},
  {"tag_name":"macos-v0.0.5","body":"Мак 0.0.5","html_url":"https://h/mac5",
   "assets":[{"name":"TorLocalProxy-0.0.5-macos.dmg","browser_download_url":"https://h/mac5.dmg"}]}
]`

func TestCheckGitHubБерётСвежийПоПлатформе(t *testing.T) {
	сервер := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/wolf3dx/TorLocalProxy/releases" {
			t.Errorf("неожиданный путь: %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(образецGitHub))
	}))
	defer сервер.Close()

	ист := Source{
		Kind: GitHub, Owner: "wolf3dx", Repo: "TorLocalProxy",
		TagPrefix: "macos-v", AssetSuffix: ".dmg", BaseURL: сервер.URL,
	}
	got, err := Check(context.Background(), сервер.Client(), ист, "0.0.5")
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if got == nil {
		t.Fatal("ожидали обновление 0.0.6, получили nil")
	}
	if got.Version != "0.0.6" {
		t.Errorf("версия = %q, хотели 0.0.6 (не должен взять ios-v0.0.7)", got.Version)
	}
	if got.AssetURL != "https://h/mac6.dmg" {
		t.Errorf("ассет = %q", got.AssetURL)
	}
	if got.SHA256URL != "https://h/mac6.sha256" {
		t.Errorf("sha256 = %q", got.SHA256URL)
	}
}

func TestCheckНетОбновленияКогдаТекущаяСвежая(t *testing.T) {
	сервер := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(образецGitHub))
	}))
	defer сервер.Close()

	ист := Source{
		Kind: GitHub, Owner: "wolf3dx", Repo: "TorLocalProxy",
		TagPrefix: "macos-v", AssetSuffix: ".dmg", BaseURL: сервер.URL,
	}
	got, err := Check(context.Background(), сервер.Client(), ист, "0.0.6")
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if got != nil {
		t.Errorf("текущая версия свежая, а Check вернул %+v", got)
	}
}

const образецGitLab = `[
  {"tag_name":"windows-v0.0.6","description":"Окна 0.0.6","_links":{"self":"https://g/win6"},
   "assets":{"links":[
     {"name":"TorLocalProxy-0.0.6-windows-x86_64.zip","url":"https://g/win6.zip"},
     {"name":"TorLocalProxy-0.0.6-windows-x86_64.zip.sha256","url":"https://g/win6.sha256"}]}},
  {"tag_name":"android-v0.0.9","description":"Дроид","_links":{"self":"https://g/a9"},
   "assets":{"links":[{"name":"TorLocalProxy-0.0.9-arm64-v8a.apk","url":"https://g/a9.apk"}]}}
]`

func TestCheckGitLabWindows(t *testing.T) {
	сервер := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.EscapedPath() != "/api/v4/projects/vkandreevich%2FTorLocalProxy/releases" {
			t.Errorf("неожиданный путь: %s", r.URL.EscapedPath())
		}
		_, _ = w.Write([]byte(образецGitLab))
	}))
	defer сервер.Close()

	ист := Source{
		Kind: GitLab, Owner: "vkandreevich", Repo: "TorLocalProxy",
		TagPrefix: "windows-v", AssetSuffix: ".zip", BaseURL: сервер.URL,
	}
	got, err := Check(context.Background(), сервер.Client(), ист, "0.0.5")
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if got == nil || got.Version != "0.0.6" {
		t.Fatalf("хотели windows 0.0.6, получили %+v", got)
	}
	if got.AssetURL != "https://g/win6.zip" || got.SHA256URL != "https://g/win6.sha256" {
		t.Errorf("ассеты: url=%q sha=%q", got.AssetURL, got.SHA256URL)
	}
}
