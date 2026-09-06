// Удержание приложения в фоне на iOS.
//
// Задача та же, что решает служба переднего плана на Android: прокси
// должен отвечать и когда приложение не на экране. Но на iOS обычное
// приложение система усыпляет через считаные секунды после ухода с
// экрана, и сокет замолкает вместе с ним. Штатного способа этого
// избежать всего один — расширение VPN (Packet Tunnel Provider), а оно
// требует особого разрешения от Apple и платной учётной записи
// разработчика.
//
// Обход, которым десятилетие пользовались прокси-приложения до
// появления расширений: объявить в Info.plist режим фоновой работы
// «audio» и проигрывать беззвучный звук по кругу. Пока звук играет,
// система приложение не усыпляет — а значит, и наши слушающие сокеты
// продолжают работать.
//
// Что об этом надо знать честно:
//   - в App Store с таким приёмом не пускают, приложение отклонят.
//     Нам туда и не надо: ставится оно в обход магазина;
//   - расход батареи выше, чем у спящего приложения: процесс не
//     засыпает вовсе;
//   - Apple может закрыть эту дорогу в любой версии iOS. Тогда
//     останется только расширение VPN.
//
// Звуковой файл рядом не кладём: тишина собирается здесь же, в памяти —
// сорок четыре байта заголовка WAV и немного нулей. Так в поставке не
// появляется лишнего файла, который нужно было бы искать во время
// работы.
//
// Ограничение сборки задано именем файла: суффикс _ios означает, что
// файл попадает только в сборку под iOS.

#import <AVFoundation/AVFoundation.h>
#import <Foundation/Foundation.h>

// Проигрыватель держим в статической переменной: он должен жить всё
// время работы приложения, иначе звук кончится вместе с ним.
static AVAudioPlayer *silencePlayer = nil;

// silentWavData собирает WAV с тишиной: 8 кГц, 8 бит, один канал,
// полсекунды. Заголовок описан в спецификации RIFF; поля пишутся
// младшим байтом вперёд.
static NSData *silentWavData(void) {
	const uint32_t sampleRate = 8000;
	const uint32_t samples = sampleRate / 2;
	const uint32_t dataSize = samples;
	const uint32_t riffSize = 36 + dataSize;

	NSMutableData *wav = [NSMutableData dataWithCapacity:44 + dataSize];

	void (^append32)(uint32_t) = ^(uint32_t value) {
		uint8_t bytes[4] = {
			(uint8_t)(value & 0xff), (uint8_t)((value >> 8) & 0xff),
			(uint8_t)((value >> 16) & 0xff), (uint8_t)((value >> 24) & 0xff)};
		[wav appendBytes:bytes length:4];
	};
	void (^append16)(uint16_t) = ^(uint16_t value) {
		uint8_t bytes[2] = {(uint8_t)(value & 0xff), (uint8_t)((value >> 8) & 0xff)};
		[wav appendBytes:bytes length:2];
	};

	[wav appendBytes:"RIFF" length:4];
	append32(riffSize);
	[wav appendBytes:"WAVE" length:4];

	[wav appendBytes:"fmt " length:4];
	append32(16);            // длина этого раздела
	append16(1);             // формат: несжатый PCM
	append16(1);             // каналов: один
	append32(sampleRate);    // частота
	append32(sampleRate);    // байт в секунду
	append16(1);             // байт на кадр
	append16(8);             // бит на отсчёт

	[wav appendBytes:"data" length:4];
	append32(dataSize);
	// Тишина: в восьмибитном PCM без знака нулевому уровню отвечает 128.
	uint8_t *silence = calloc(dataSize, 1);
	memset(silence, 128, dataSize);
	[wav appendBytes:silence length:dataSize];
	free(silence);

	return wav;
}

// TorProxyStartKeepAlive включает удержание. Возвращает 1, если звук
// пошёл, и 0, если нет: причина уходит в журнал приложения, а решение,
// что показывать пользователю, принимается на стороне Go.
int TorProxyStartKeepAlive(void) {
	if (silencePlayer != nil && silencePlayer.isPlaying) {
		return 1;
	}

	AVAudioSession *session = [AVAudioSession sharedInstance];
	NSError *error = nil;

	// Playback — та категория, которая продолжает звучать в фоне.
	// MixWithOthers обязателен: без него мы бы обрывали чужую музыку,
	// и приложение из полезного превратилось бы во вредное.
	if (![session setCategory:AVAudioSessionCategoryPlayback
				  withOptions:AVAudioSessionCategoryOptionMixWithOthers
						error:&error]) {
		NSLog(@"TorLocalProxy: не выставилась звуковая категория: %@", error);
		return 0;
	}
	if (![session setActive:YES error:&error]) {
		NSLog(@"TorLocalProxy: не включилась звуковая сессия: %@", error);
		return 0;
	}

	silencePlayer = [[AVAudioPlayer alloc] initWithData:silentWavData() error:&error];
	if (silencePlayer == nil) {
		NSLog(@"TorLocalProxy: не создался проигрыватель тишины: %@", error);
		return 0;
	}
	silencePlayer.numberOfLoops = -1; // по кругу без конца
	silencePlayer.volume = 0;
	if (![silencePlayer play]) {
		NSLog(@"TorLocalProxy: тишина не заиграла");
		silencePlayer = nil;
		return 0;
	}
	return 1;
}

// TorProxyStopKeepAlive выключает удержание. Нужен, когда пользователь
// отключил прокси: держать процесс живым больше незачем, и батарею
// тратить не на что.
void TorProxyStopKeepAlive(void) {
	if (silencePlayer != nil) {
		[silencePlayer stop];
		silencePlayer = nil;
	}
	[[AVAudioSession sharedInstance] setActive:NO error:nil];
}
