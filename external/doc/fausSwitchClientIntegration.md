# Faus Switch online — istemci devir notu

Tarih: 7 Kasım 2026 · Hazırlayan: backend · Hedef: istemci (mobil) agent'ı · Sözleşme:
`fausSwitch.v1`, protocol `2`, `courseVersion` `7`

Backend tamamlandı (F0–F6, ayrıntılar `fausSwitchBackendPlan.md`). Sözleşmeyi istemci yazdı
(`fausSwitchBackendBriefing.md`, `fausSwitchProtocol.json`); sunucu ona uyar. Bu not neyin
hazır olduğunu, sözleşmenin boşluklarında backend'in verdiği kararları ve istemciden istenenleri
anlatır. İstemciden istenenler §8'de, görev metni §9'da.

## 0. Sınırlar

- Wire sözleşmesi, ürün kuralları ve fizik sabitleri tek taraflı değiştirilmez. Belge, kod ve
  fixture arasında uyuşmazlık görülürse somut fark (dosya, alan, senaryo, beklenen/gerçek değer)
  backend'e bildirilir.
- Kanonik kaynaklar: bu not, `fausSwitchBackendPlan.md`, `architecture-decisions.md`
  (ADR-030 lobi niyetleri, ADR-031 geri sarma ve yarış sonu penceresi) ve §2'deki dosyalar.
- Zarf, lobi, hazır olma, heartbeat, sonuç ve rövanş House Rockets / FauseTanks ile aynıdır.
  Ortak transport katmanı yeniden kullanılır; Faus Switch'e özgü olan yalnız bu nottaki
  farklardır.
- Sunucu bot doldurmaz. Online yarış yalnız gerçek oyuncularla oynanır.

## 1. Teslim durumu

| İş | Durum |
|---|---|
| Motor (parkur, kamera, başlangıç ızgarası, fizik) | Swift motorundan port; 48 senaryoluk Swift iz karşılaştırması bit düzeyinde aynı. |
| Simülasyon (geri sarma, yarış sonu penceresi) | Tamam (ADR-031). |
| Lobi renkleri (`fausSwitch.selectColor`, `lobbyState`) | Tamam (ADR-030). |
| Runtime, gateway, iki instance | Gerçek Mongo + Redis ile 2 ve 8 istemcili uçtan uca test geçti. |
| Kalıcı sonuç, rövanş | Socket/HTTP sonuç eşitliği, fixture'daki `result` ile değer değer eşitlik. |
| Kurtarma, sahip çökmesi | Yarış ortasında sahip süreci öldürülüp aynı soketlerle kurtarma testi geçti. |
| Yük | 8 robot, robot başına 20 flip/sn ölçüldü (§5). |
| Katalog | Migration 0061 oyunu deploy'la **tüm evlerde açık** getirir. |
| Canlı ortam | **Açık.** Sürüm henüz deploy edilmedi (§7). |

## 2. Kaynaklar

| Dosya | sha256 | Not |
|---|---|---|
| `fausSwitchProtocol.json` (21.340 B) | `ba32a5be803b289337c9a57ab804255e02a69366d657b54fd685fd7fd0029c6d` | Backend kopyası (`internal/application/game/games/fausSwitch/fixtures/`) istemcideki asılla byte byte aynı. Değiştirilmedi. |
| `testdata/swiftParity/digests.txt` | `9c497bee0cdc84032d348731b57c79e400b98e9e75ab3dedc4a8d793932eb25e` | Swift motorunun 48 tohumlu yarış izinin özetleri. |
| `testdata/swiftParity/main.swift` | `38a747b482a2a84b3a2a95eee02c1babf0e274eaf7752f6226e148327818ad59` | Özetleri üreten Swift programı (motor kaynaklarının bir kopyasına karşı derlenir). |

İz karşılaştırması rastgele flip'li 1–8 robotluk yarışları iki motorda da çalıştırır ve her
float'ı bit deseniyle yazar; Go izinin özetleri Swift'inkilerle aynıdır. Bu, parkurların ve
fiziğin istemciyle bire bir aynı olduğunun kanıtıdır.

## 3. Kullanıcı kararları ve istemciye etkisi

| # | Karar | İstemcinin yapacağı |
|---|---|---|
| K1 | Lobide renkler gösterilir. | `sessionSnapshot.lobbyState.colors` okunup lobide çizilir (§4.1). |
| K2 | Yarış sonu 250 ms beklenebilir. | Son robot durduktan sonra sonuç 250 ms içinde gelir; arada "bekleniyor" gösterilir (§4.4). |
| K3 | Oyun katalogda tüm evlere açık. | Oyunun açık olduğu her zaman `GET /game/catalog`'dan öğrenilir, sabit kabul edilmez. |

## 4. Sözleşmede netleşen davranışlar

### 4.1. Lobi ve renkler

- Seçim `fausSwitch.selectColor` (`{"color": "plum"}`) ile yapılır. Kontrol yetkisi istemez;
  `realtime.ready` gibi işlenir ve başarıda `gameSession.commandAccepted` döner.
- Seçimler oturumun `lobbyState` alanındadır: `{"colors": {"<playerId>": "<color>"}}`. Alan
  `sessionSnapshot`'ta, `PUT/GET .../session` cevabında **yalnız doluyken** yazılır; diğer
  oyunların wire formatı değişmedi.
- Başkasının tuttuğu renge talep **kabul edilir ama durumu değiştirmez.** İstemci kendi
  seçimini her zaman `lobbyState`'ten okumalı, gönderdiği talepten değil.
- Ayrılan oyuncunun kaydı bir sonraki seçime kadar alanda kalabilir; istemci yalnız katılmış
  oyuncuların rengini çizmeli.
- Geri sayım başlayınca seçimler donar; sonraki talep `fausSwitch.error.not_in_lobby` alır.
  Rengi olmayan oyuncuya katalog sırasındaki ilk boş renk verilir (`plum`, `forest`, `ocean`,
  `lavender`, `olive`, `bronze`, `graphite`, `berry`).
- Geri sayım snapshot'ı (`phase: countdown`, tick 0) yarışı nihai renklerle ve nihai
  `runnerScale` ile gösterir; fixture'daki `countdown` / `crowdedCountdown` ile değer değer
  aynıdır.

### 4.2. Kontrol, bağlantı ve kurtarma

- Yarış başlayınca her bağlantı `fausSwitch.controlGranted` alır; `controlGeneration` yalnız
  kendi robotunun snapshot satırında ve yalnız kontrol o bağlantıdayken doludur.
- Bağlantı kopması eleme değildir: robot girdisiz koşmaya devam eder, oyuncu dönünce yeni
  generation ile kontrolü geri alır. Yeniden bind'da en yüksek ve işlenmiş sıra 0'a döner,
  bekleyen flip'ler atılır; istemci sırayı 1'den başlatmalı.
- Elenen (duvar, geride kalma) robotun oyuncusu **yarış boyunca kontrolü tutar**: geç bir flip
  robotu kurtarabilir (§4.3). Kontrolü yalnız oturumdan ayrılan (`forfeit`) ya da evden çıkan
  (`membershipRevoked`) oyuncu kaybeder.
- Sahip değişince (süreç çökmesi, deploy) yarış son checkpoint'ten yeni bir `runtimeEpoch` ile
  sürer. Kesinti için tick harcanmaz; robotlar kaldıkları yerden devam eder. Eski generation'la
  gelen flip `fausSwitch.error.stale_control` alır; istemci yeni grant'i bekleyip onunla
  göndermeli. Test: yarış ortasında sahip öldürüldü, aynı soketler eski sahibin kirası dolunca
  yeni grant aldı, renkler ve uygulanmış flip korundu, yarış tek sonuçla bitti.

### 4.3. Flip ve gecikme telafisi

- Flip `{"controlGeneration", "inputSequence", "clientTick"}` taşır; `clientTick` oyuncunun
  ekranındaki dünyanın tick'idir. Sunucu flip'i o tick'te uygular: `now − 30`'dan eskiyse
  `now − 30`'a, gelecekteyse `now`'a kırpar.
- Geçerlilik flip'in uygulandığı tick'teki dünyaya göre belirlenir. O tick'te desteksiz flip
  hata değildir, no-op'tur. `fausSwitch.error.runner_finished` yalnız robot o tick'te zaten
  bitmiş ya da elenmişse döner. İki durumda da `lastProcessedInputSequence` sırayı onaylar.
- Yinelenen `inputSequence` hatasız kabul edilir ve uygulanmaz (yeniden gönderim güvenli).
- Hız sınırı saniyede 20 flip ve oyuncu başına en çok 8 bekleyen flip; fazlası
  `realtime.error.rate_limited`. Yarış bittiyse `fausSwitch.error.not_running`.

### 4.4. Yarış sonu ve sonuç

- Koşan robot kalmayınca sonuç hemen değil, 30 tick (250 ms) sonra kesinleşir; bu sürede gelen
  geç bir flip son robotu kurtarabilir ve yarış sürer. Sonucun tick'i son robotun durduğu
  tick'tir. İstemci son elenmeyi kesin sonuç saymamalı.
- `timeLimit` 14.400. tick'te kesindir. O anda koşan robotlar mesafeye göre sıralanır; tek başına
  1. olan ama bitirmeyen robot **kazanan değildir** (`winnerId: null`, §8.6).
- Sonuç bir kez yazılır; `fausSwitch.result` mesajı ve `GET /game/{sessionId}/result` cevabı
  byte byte aynıdır. Önce `phase: ended` snapshot'ı (`winnerId` dolu ya da `null`), sonra sonuç
  mesajı gelir.
- Sunucu `startedAt: null` olan bir sonuç hiç göndermez: sonuç yalnız başlamış yarıştan doğar,
  lobide iptal sonuç yazmaz (sonuç uç noktası `result_not_found`). Fixture'daki `cancelledResult`
  yalnız istemcinin çözebildiği bir şekil olarak kalır.
- Yarış ortasında oyuncu sayısı 2'nin altına düşse de yarış iptal edilmez; son koşan robot
  durunca pencereyle biter.

### 4.5. Sahip aşırı yükü

Bir sahip süreci 250 ms'den uzun duraklarsa (o aralığın flip'leri geri sarma penceresinden
düşer) yarış `cancelled` / `runtimeOverloaded` ile biter. Hesap maliyeti bunun çok altındadır
(§5); bu yalnız süreç duraklamasında olur. İstemci bu sonucu diğer iptaller gibi gösterir.

## 5. Ölçümler (`multiplayerBaseline.md` §11)

8 robot, robot başına 20 flip/sn, her flip 0–30 tick geç (neredeyse her adım geri sarar):

| | Değer |
|---|---|
| Sunucu adımı (60 Hz, 2 tick) | 30–34 µs; oda başına bir çekirdeğin ~%0,2'si |
| En kötü adım (tam pencere geri sarma) | ~80 µs |
| Snapshot, 8 robot | ~3,3 KB ham, ~0,7 KB permessage-deflate ile |
| Bağlantı başına bant, 20 Hz | 64 KB/s ham, **14 KB/s sıkıştırılmış** |

İstemci WebSocket bağlantısında permessage-deflate'i açmalı; snapshot %78 küçülür.

## 6. Backend'in yerelde doğruladıkları

- `fausSwitch` paketi: Swift iz eşitliği, parkur ve kamera tabloları, simülasyon (20 tick geç
  flip = zamanında flip, kırpma, birleştirilmiş geri sarma = tek tek, yarış sonu penceresi ve
  kurtarma, süre sınırı), runtime (kontrol, hız, yinelenen sıra, çıkanlar, sonuç, checkpoint →
  restore → aynı dünya), frame'siz adımda 0 ayırma.
- `tests/fausSwitchContract_test.go`: fixture'daki her istemci mesajının çözümü, önizleme =
  fixture, `playing` şekli, hata kodları.
- `tests/fausSwitchResult_test.go`: fixture `result` eşitliği, 23 bozuk sonuç reddi, migration'lar.
- `tests/lobbyIntent_test.go`: renk kuralları, `lobbyState` yazımı, gateway üzerinden seçim.
- `tests/fausSwitchIntegration_test.go`: iki instance'ta 2 ve 8 istemcili tam akış (renk seçimi
  ve çakışma → `lobbyState` → hazır → geri sayım önizlemesi → grant → her oyuncunun flip'i ve
  onayı → yarışın kendiliğinden bitmesi → socket/HTTP sonuç eşitliği; 8 kişide son hazırla
  hemen geri sayım, 0,75 ölçek); yarış ortasında sahip sürecinin öldürülüp kurtarılması.

## 7. Canlı test ortamı

Deploy henüz yapılmadı. Deploy'dan sonra:

1. `GET /game/catalog` cevabında `fausSwitch` ve `settings` (fixture'daki `settings` ile aynı
   değerler) görünmeli.
2. İki cihazla lobi: renk seçimi karşı cihazda görünmeli; aynı renge talep sessizce yok sayılmalı.
3. Yarış: flip'ler kendi robotunda anında, karşı cihazda en geç bir snapshot sonra görünmeli.
4. Bir cihazın ağı kesilip açılmalı: robot koşmaya devam etmeli, dönüşte kontrol geri gelmeli.
5. Sonuç paneli ile `GET /game/{sessionId}/result` aynı olmalı; rövanş yeni oturum açmalı.

## 8. İstemciden istenenler

1. **`fausSwitchSimulation.json`** (Swift motorundan): 2, 4 ve 8 kişilik (ölçek 0,75)
   senaryolar. Backend kendi iz karşılaştırmasını yaptığı için engelleyici değil; istemcinin kendi
   testlerinde aynı dosyayı kullanması için isteniyor. Biçim (`JSONEncoder` varsayılanı,
   `Double`'lar kısaltmasız):
   `{"courseVersion": 7, "scenarios": [{"name", "runnerCount", "flips": [{"tick", "seat"}],
   "checkpoints": [{"tick", "runners": [{"x", "y", "velocityY", "gravity", "status",
   "finishedAtTick", "eliminatedAtTick", "eliminationReason"}]}]}]}`. Opsiyoneller `null`
   yazılır. Senaryo `FausSwitchStartGrid.lanes(count:runnerScale:)` ile başlar; her tick'te o
   tick'in flip'leri liste sırasıyla `FausSwitchPhysics.flip(_:index:course:)` ile uygulanır,
   sonra `step`. Tick `T`'deki checkpoint, `T`'ye ulaştıran adımdan sonraki ve `T`'nin
   flip'lerinden önceki durumdur.
2. **Parkur dökümü (isteğe bağlı):** `FausSwitchCourse.race(runnerCount:)` için `n = 1…8` katı
   (kimlik, tür, kutu), portal, boşluk, boost ve yavaşlatma listeleri JSON olarak. İz
   karşılaştırması parkurları dolaylı doğruladığı için engelleyici değil.
3. **Lobide renkler (K1):** §4.1. Fixture'ın `sessionSnapshot` örneğine bir `lobbyState` örneği
   eklensin.
4. **Yarış sonu (K2):** §4.4; son elenmeden sonra 250 ms "bekleniyor".
5. **Katalog (K3):** oyun deploy'la tüm evlerde açık gelir; açıklık katalogdan okunur.
6. **Sonuç doğrulaması düzeltmesi:** `FausSwitchResultDTO.validate` (`completed` dalı)
   `winnerId == null` için "birden fazla 1. sıra" yerine "1. sıradaki tek oyuncu bitirmemiş ya da
   birden fazla 1. sıra var" kuralını kullanmalı. Örnek: süre dolduğunda en ilerideki robot tek
   başına 1. sıradadır ama bitirmediği için `winnerId` `null`dır. Bugünkü kural bu geçerli sonucu
   reddeder.
7. **Çalışmayan Swift testi:** `FausSwitchPhysicsTests.testPortalNeverDropsARobotBehindTheTrail`
   iç fonksiyon olarak `testPortalIsJustBigEnoughForARobot`'un içinde kaldığı için hiç çalışmıyor.
   Dışarı alınırsa `runner.x < before` kontrolü başarısız olur (iz robotu öne çektiğinde iniş
   noktası giriş noktasının önünde). Işınlanmayı yer çekimi değişimiyle algılamak yeterli.
8. **Bilgi:** referans motorda iki robot, sıkışma ya da portal inişinde bir tick boyunca iç içe
   olabiliyor. Sunucu bunu birebir uygular; çizimde kısa bir üst üste binme normaldir.
9. **Bilgi:** §4.2–4.4'teki netleşmeler (yinelenen sıra, `runner_finished` sayımı, elenen
   oyuncunun kontrolü tutması, `startedAt: null` sonucun hiç gelmemesi) istemcinin beklentisiyle
   çelişiyorsa backend'e bildirilmeli.

## 9. İstemci agent'ına verilecek görev

> Faus Switch online backend'i hazır (`fausSwitch.v1`, protocol 2, courseVersion 7). Bu notu
> (`fausSwitchClientIntegration.md`) baştan sona oku. Sunucu senin fixture'ına uyuyor;
> `fausSwitchProtocol.json` backend kopyasıyla byte byte aynı (sha256 `ba32a5be…9c6d`).
> Yapılacaklar: (1) §8.6'daki sonuç doğrulaması düzeltmesi ve §8.7'deki Swift testinin
> düzeltilmesi; (2) lobide `sessionSnapshot.lobbyState.colors`'u okuyup çizmek, seçimi
> `fausSwitch.selectColor` ile göndermek ve kendi rengini `lobbyState`'ten okumak (§4.1);
> fixture'ın `sessionSnapshot` örneğine `lobbyState` eklemek; (3) online yarış akışında §4.2–4.4
> davranışlarını uygulamak: yeniden bind'da sırayı 1'den başlatmak, sahip değişiminde yeni grant'i
> beklemek, son elenmeden sonra 250 ms "bekleniyor" göstermek, permessage-deflate'i açmak;
> (4) `fausSwitchSimulation.json`'ı (§8.1) üretip backend'e iletmek. Sözleşmeyle çelişen bir şey
> görürsen kendin alan ya da kural uydurma, somut farkı backend'e yaz.
