// Встроенные мосты — те же, что несёт в себе Tor Browser.
//
// Зачем они здесь. Обычные мосты (obfs4, webtunnel) выдаются по
// запросу и у каждого свои: адрес, отпечаток, ключ. А у snowflake и
// meek_lite строка одна на всех — договариваться о встрече с
// добровольцами помогает брокер, адрес которого и вписан в строку.
// Значит, ничего заказывать не нужно: приложение может предложить их
// сразу, одной кнопкой.
//
// Это не наша выдумка и не чей-то приватный мост: точно эти строки
// лежат внутри каждого Tor Browser (chrome/toolkit/content/global/
// pt_config.json), оттуда они и взяты. Со временем Tor Project их
// меняет — если встроенные мосты перестанут работать, строки стоит
// обновить оттуда же.
//
// Почему snowflake идёт первым: у него нет постоянного адреса, который
// можно заблокировать, поэтому там, где узнают obfs4, он остаётся
// последней рабочей возможностью. meek_lite медленнее и держится на
// одном большом облаке — он запасной.
package bridges

import "strings"

// Builtin возвращает встроенные строки мостов, готовые для поля ввода.
func Builtin() string {
	return strings.Join([]string{
		"snowflake 192.0.2.3:80 2B280B23E1107BB62ABFC40DDCC8824814F80A72 fingerprint=2B280B23E1107BB62ABFC40DDCC8824814F80A72 url=https://1098762253.rsc.cdn77.org/ fronts=app.datapacket.com,www.datapacket.com ice=stun:stun.epygi.com:3478,stun:stun.uls.co.za:3478,stun:stun.voipgate.com:3478,stun:stun.mixvoip.com:3478,stun:stun.telnyx.com:3478,stun:stun.hot-chilli.net:3478,stun:stun.fitauto.ru:3478,stun:stun.m-online.net:3478 utls-imitate=hellorandomizedalpn",
		"snowflake 192.0.2.4:80 8838024498816A039FCBBAB14E6F40A0843051FA fingerprint=8838024498816A039FCBBAB14E6F40A0843051FA url=https://1098762253.rsc.cdn77.org/ fronts=app.datapacket.com,www.datapacket.com ice=stun:stun.epygi.com:3478,stun:stun.uls.co.za:3478,stun:stun.voipgate.com:3478,stun:stun.mixvoip.com:3478,stun:stun.telnyx.com:3478,stun:stun.hot-chilli.net:3478,stun:stun.fitauto.ru:3478,stun:stun.m-online.net:3478 utls-imitate=hellorandomizedalpn",
		"meek_lite 192.0.2.20:80 url=https://1603026938.rsc.cdn77.org front=www.phpmyadmin.net utls=HelloRandomizedALPN",
	}, "\n") + "\n"
}
