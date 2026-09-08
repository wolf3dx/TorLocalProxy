package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"golang.org/x/net/proxy"
)

// АдресПроверки — сайт, который сообщает, кто мы снаружи и вышли ли мы из
// сети Tor. Тот же, что показывает Tor Browser на стартовой странице.
const АдресПроверки = "https://check.torproject.org/api/ip"

// СрокПроверки — сколько ждать ответа. Через Tor запрос идёт не мгновенно:
// это три-четыре узла и выходной релей, десяток секунд — норма.
const СрокПроверки = 90 * time.Second

// CheckExitIP ходит через поднятый SOCKS5 на check.torproject.org и
// возвращает внешний адрес и признак того, что трафик вышел из сети Tor.
//
// Это кнопка «Проверить IP»: доказательство, что прокси не просто
// поднялся, а действительно уводит трафик через Tor. Запрос идёт строго
// через наш же прокси — иначе проверка бессмысленна.
//
// Ошибка, если прокси ещё не поднят: проверять нечего.
func (s *Service) CheckExitIP(ctx context.Context) (ip string, isTor bool, err error) {
	socks := s.SocksAddress()
	if socks == "" {
		return "", false, errors.New("прокси не поднят — сначала подключитесь")
	}

	// Имя узла в SOCKS5 уходит нетронутым: разрешать его локально нельзя,
	// DNS-запрос ушёл бы мимо Tor и выдал, куда мы идём. proxy.SOCKS5
	// именно так и делает — передаёт домен на сторону прокси.
	посредник, err := proxy.SOCKS5("tcp", socks, nil, &net.Dialer{Timeout: 30 * time.Second})
	if err != nil {
		return "", false, err
	}
	контекстный, _ := посредник.(proxy.ContextDialer)

	клиент := &http.Client{
		Timeout: СрокПроверки,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, цель string) (net.Conn, error) {
				if контекстный != nil {
					return контекстный.DialContext(ctx, "tcp", цель)
				}
				return посредник.Dial("tcp", цель)
			},
		},
	}

	запрос, err := http.NewRequestWithContext(ctx, http.MethodGet, АдресПроверки, nil)
	if err != nil {
		return "", false, err
	}
	ответ, err := клиент.Do(запрос)
	if err != nil {
		return "", false, err
	}
	defer func() { _ = ответ.Body.Close() }()

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
