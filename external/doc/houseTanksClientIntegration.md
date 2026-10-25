# FauseTanks online — istemci entegrasyon belgesi

Tarih: 24 Ekim 2026 · Hazırlayan: backend · Hedef: istemci (mobil) agent'ı · Sözleşme:
`houseTanks.v2`, protocol `2`, harita kataloğu `2` (25 Ekim: arena 872 × 400, §2.1)

Bu belge istemci agent'ına doğrudan verilir. Backend tamamlandı (T0–T6, ayrıntılar
`houseTanksBackendPlan.md`). Bu belgede, Go motorunun ve fixture'ların referans olduğu
bir sözleşme anlatılıyor: istemci kendi motorunu ve wire katmanını bu sözleşmeye uydurur.
Görev metni §9'da.

## 0. Okuma sırası ve sınırlar

1. §1 durum ve §2 kaynaklar: neyin hazır olduğu, hangi dosyaların kopyalanacağı.
2. §3 sözleşme ve §4 davranış: wire katmanı ve online oyun akışı.
3. §5 Faz 0: Swift motorunun Go fixture'larına uydurulması. Online işten **önce** yapılır.
4. §6 mobil fazlar ve çıkış ölçütleri, §7 canlı test ortamı.

Sınırlar:

- Wire sözleşmesi, ürün kuralları ve fizik sabitleri tek taraflı değiştirilmez. Belge, kod
  ve fixture arasında uyuşmazlık görülürse sessizce yeni alan, uç nokta ya da kural
  üretilmez; somut fark (dosya, alan, senaryo, beklenen/gerçek değer) backend'e bildirilir.
- Kanonik kaynaklar backend deposudadır: bu belge, `houseTanksBackendPlan.md`,
  `architecture-decisions.md` (ADR-028, ADR-029) ve §2'deki fixture'lar. İstemci
  deposundaki kopyalar §2'deki sha256 ile eşitlenmeden kullanılmaz.
- Lobi, zarf, heartbeat, sonuç ve rövanş mekanizmaları House Rockets'le aynıdır
  (`houseRocketsMultiplayerPlan.md` §5–7). Ortak transport katmanı yeniden yazılmaz,
  yeniden kullanılır. Tanklara özgü olan yalnız bu belgedeki farklardır.

## 1. Teslim durumu

| İş | Durum | İstemcinin işi |
|---|---|---|
| Sözleşme ve fixture'lar | Tamamlandı; Go testleri fixture'larla birebir. | Wire DTO'ları ve fixture testleri. |
| Fizik, harita kataloğu, jokerler, tur akışı | Tamamlandı; Swift kaynağından port, 17 fizik senaryosu. | Faz 0: Swift motorunu fixture'a uydur. |
| Runtime, gateway, iki instance | Gerçek Mongo + Redis ile iki instance testleri geçti. | Online oda, kontrol, render. |
| Kalıcı sonuç, rövanş | Transaction, socket/HTTP eşitliği, rövanş testleri geçti. | Sonuç paneli, HTTP fallback, rövanş. |
| Kurtarma, sahip çökmesi | Tur ortası süreç öldürme ve oy penceresinde devir testleri geçti. | Epoch/grant değişimi, resync. |
| Yük | 8 oyuncu ağır atış ölçüldü (`multiplayerBaseline.md` §10). | Sıkıştırmayı aç (§4.5). |
| Canlı ortam | **Açık.** Sürüm henüz deploy edilmedi; deploy ile oyun tüm evlerde açılır. | §7'deki kontrolden önce canlı kabul yok. |

Oyunu 0054 migration'ı tüm evlerde açık olacak şekilde kaydeder. Operatör oyunu kapatabilir
ya da belirli evlerle sınırlayabilir (§7). İstemci oyunun açık olduğunu her zaman
`GET /game/catalog` ile öğrenir, sabit kabul etmez.

## 2. Kaynaklar, sürümler ve fixture'lar

Fixture'lar backend deposunda `internal/application/game/games/houseTanks/fixtures/`
altındadır. İstemci deposunun test kaynaklarına **byte byte** kopyalanır; kopyalandıktan
sonra hash doğrulanır, dosyalar elle düzenlenmez:

| Dosya | Boyut | sha256 | İçerik |
|---|---|---|---|
| `houseTanksMaps.json` | 21.385 B | `fa8d64f37fb35c2e0c737325ac4e5c49acaf038438557a0bffe0a26a131681b9` | Harita kataloğu (5 harita) |
| `houseTanksSimulation.json` | 197.896 B | `4f7b5ce53fd81cf3ef6cf46c127d17ade68d73450699a3e0baf8c3400e19f712` | Sabitler + 17 adım adım fizik senaryosu |
| `houseTanksProtocol.json` | 46.309 B | `f0efda9feca82f4cecdce4ebad058a734d77e56e9ad05cfe87ffa4201dab4289` | Kurallar, tüm mesaj örnekleri, geçersiz mesajlar |

```bash
shasum -a 256 houseTanksMaps.json houseTanksSimulation.json houseTanksProtocol.json
```

### 2.1. Arena boyutu değişikliği (25 Ekim 2026)

İstemcinin `houseTanksArenaResizeBriefing.md` önerisi ürün sahibince onaylandı ve backend'de
uygulandı. Yeni fixture'larla Faz 0 eşitlik testi yeniden çalıştırılır.

| | Önce | Şimdi |
|---|---|---|
| Arena | 1180 × 640 | **872 × 400** |
| Duvar çerçevesi `max(16, H × 0,035)` | 22,4 | 16 |
| Joker alanı `{48, 44, W − 96, H − 124}` | 1084 × 516 | 776 × 276 |
| Joker başlangıç mesafesi `max(80, alan.H × 0,22)` | 113,52 | 80 |
| `contractRevision` / `mapCatalogVersion` | `houseTanks.v1` / 1 | `houseTanks.v2` / 2 |
| courtyard başlangıç 4 / sürüm | (0,50; 0,08) / 3 | **(0,40; 0,08) / 4** |

- Tank, mermi ve joker boyutları, hızlar, kütleler ve tick süreleri değişmedi. Formüller de
  aynı; normalize tanımlı engeller arenayla birlikte orantılı küçülür.
- Katalog doğrulaması 872 × 400'te beş haritada da sıfır sorun veriyor.
- Simülasyon senaryolarının seti ve adları aynı. Elle verilen konumlar yeni arenaya göre
  yeniden yerleştirildi:
  - Üst sıra senaryoları (`headOn`, `rocketKill`, `shieldBlocks`, `spreadProtection`,
    `stationaryTarget`, `wreckBlocks`) y = 362 şeridinde, x = 400'den başlıyor. Tanklar
    arasındaki mesafeler önceki gibi.
  - Kasa senaryoları (`cratePush`, `shellBreaksCrate`, `muzzleBlocked`) obstacle 8'in yeni
    konumuna (523,2; 160) göre yerleşti. `muzzleBlocked`'ta tank kasadan 20 nokta uzakta;
    küçülen kasada namlunun kasanın içinde kalması için gereken mesafe bu.
  - `wallBounce` x = 700'den başlıyor; sağ duvara mesafe eskisi gibi. `jokerExpiry`
    jetonu (650, 362).
  - `aimLock`'un ara checkpoint'leri 190/200'den 120/129'a alındı: mermi duvara artık 127.
    tickte çarpıyor, iki checkpoint yine çarpmanın hemen öncesi ve sonrası.
- `houseTanksProtocol.json`'daki snapshot örneklerinin konumları yeni arenaya oranlandı
  (iki ondalık). Wire şekli değişmedi.

Sürümler birbirinden bağımsızdır:

- `protocolVersion: 2` zarfın sürümüdür (çekirdek v2 zarfı, House Rockets'le aynı).
- `contractRevision: "houseTanks.v2"` oyun sözleşmesinin revizyonudur; welcome'da gelir.
- `mapCatalogVersion: 2` katalog dosyasının sürümüdür; welcome'da gelir. Her haritanın
  ayrıca kendi `version`'ı vardır (livingRoom, corridors, warehouse 3; courtyard 4; Bodrum 1); snapshot `mapId` +
  `mapVersion` taşır.
- Fixture'ların `schemaVersion: 1` alanı dosya biçimidir, socket payload'ı değildir.
- `houseTanksProtocol.json`'daki `liveGatewayAvailable: false` T0'ın tarihsel işaretidir.
  Canlı ortam kararı için kullanılmaz.

Welcome'daki `contractRevision` ya da `mapCatalogVersion` istemcinin bildiğinden farklıysa,
ya da snapshot'taki `mapId` + `mapVersion` istemci kataloğunda yoksa, istemci maçı
oynanabilir gibi göstermez; güncelleme gerektiğini bildirir.

Fixture'ların içindeki `reference` ve `referenceSourceSha256` alanları, portun yapıldığı
Swift dosyalarının hash'leridir. Swift motoru değiştikçe bu değerler eskir; bu beklenen bir
durumdur. Eşitliği bu hash'ler değil, §5'teki senaryolar belirler.

## 3. Sözleşme

### 3.1. HTTP

Tüm uçlar `/api/v1` altında, `Authorization: Bearer <token>` ile çağrılır:

| Uç | Anlamı |
|---|---|
| `GET /game/catalog?houseId=…` | Evde açık oyunlar, kuralları ve istemci ayarları. `houseTanks` yoksa oyun kapalıdır. |
| `PUT /game/houseTanks/session` `{"houseId": "…"}` | Evin aktif oturumunu bulur ya da açar. Otomatik join/ready yapmaz. |
| `GET /game/houseTanks/session?houseId=…` | Aktif oturum; yoksa 404. 404 tek başına "oyun kapalı" demek değildir. |
| `GET /game/sessions/{sessionId}/result` | Kesinleşmiş sonuç; henüz commit edilmediyse 404 `houseTanks.error.result_not_found`. |
| `GET /game/sessions/{sessionId}/realtime?protocolVersion=2` | WebSocket. Token header'da, query'de değil. |

### 3.2. Zarf ve ortak mesajlar

Zarf, lobi komutları (`gameSession.join/ready/leave/cancel`), `gameSession.snapshot`,
`gameSession.commandAccepted/commandRejected` ve `realtime.ping/pong` House Rockets v2 ile
aynıdır. Tanklara özgü olanlar:

- **`realtime.welcome`** payload'ı: `sessionId`, `gameKey`, `connectionId`,
  `protocolVersion`, `contractRevision`, `mapCatalogVersion`, `settings`. Harita ve tohum
  welcome'da **yoktur**: bağlantı lobide açıldığında harita henüz seçilmemiştir. Harita
  lobi geri sayımından itibaren her snapshot'ta gelir.
- **`settings`** (varsayılanlar; operatör değiştirebilir, oturuma dondurulur):

  | Alan | Varsayılan | Anlamı |
  |---|---|---|
  | `physicsRateHz` | 120 | Tick hızı; tüm `*Tick` alanları bu birimde |
  | `schedulingRateHz` | 60 | Sunucu adım hızı |
  | `snapshotRateHz` | 20 | Snapshot hızı (kritik değişikliklerde ek kare gelir) |
  | `maximumInputRateHz` | 20 | İstemcinin hedef girdi hızı |
  | `maximumPressMessagesPerSecond` | 30 | Sunucunun sert sınırı (üstü `realtime.error.rate_limited`) |
  | `controlHeartbeatMilliseconds` | 2000 | `realtime.ping` aralığı |
  | `controlTimeoutMilliseconds` | 6000 | Ping gelmezse kontrol düşer |
  | `reconnectGraceMilliseconds` | 10000 | Yetersiz oyuncu bekleme süresi |
  | `lagCompensationMilliseconds` | 150 | Gecikme telafisi penceresi |
  | `headingToleranceMilliradians` | 35 | Gecikme telafisi toleransı |
  | `arenaWidth` × `arenaHeight` | 872 × 400 | Arena, nokta cinsinden |
  | `roundWinsNeeded` / `armorPerRound` | 3 / 5 | |
  | `roundCountdownMilliseconds` | 3000 | Tur geri sayımı |
  | `roundReadyWindowMilliseconds` | 30000 | Oy penceresi |
  | `roundReadyIdleMilliseconds` | 120000 | Pencere hiç açılmazsa maç biter |
  | `maximumMatchMilliseconds` | 300000 | Maç üst sınırı |
  | `eventWindowMilliseconds` | 1000 | Olayların snapshot'ta kalma süresi |

- **Uygulama heartbeat'i** `realtime.ping`'dir; `controlHeartbeatMilliseconds` aralığında
  gönderilir. Transport (WebSocket) pong'u yerine geçmez. Ping'i kesen bağlantının kontrolü
  6 sn sonra düşer ve tankı hareketsiz hedef olur.

### 3.3. İstemciden

| Tip | Payload | Kural |
|---|---|---|
| `houseTanks.press` | `{controlGeneration, inputSequence, pressed, clientTick, heading}` | Tek düğmeye basma (`true`) / bırakma (`false`). Yalnız değişimde gönderilir. |
| `houseTanks.roundReady` | `{controlGeneration, ready}` | Yalnız `roundPhase = roundReady`'de. |
| `houseTanks.resync` | `{}` | Tam durum ve gerekirse yeniden bind ister. |

- `controlGeneration`: son geçerli `houseTanks.controlGranted`'daki değer. Opak metin.
- `inputSequence`: tamsayı, ≥ 1, kontrol nesli içinde kesin artan (boşluk serbest). Yeni
  grant geldiğinde sayaç sıfırdan başlar. Eski ya da tekrar eden sıra `stale_control` alır.
- `clientTick`: basıldığı anda istemcinin **ekranda gösterdiği** dünya tick'i (≥ 0,
  tamsayı). `heading`: o anda ekranda görünen namlu açısı (radyan, sonlu sayı). İkisi
  yalnız gecikme telafisi içindir (§4.3).
- Sıkı doğrulama: bilinmeyen alan, eksik alan, `null`, kesirli tamsayı, istemcinin kendi
  `playerId`'sini ya da konumunu göndermesi reddedilir. Örnekler protokol fixture'ının
  `invalidClientMessages` bölümündedir; istemci bunları hiçbir zaman üretmemelidir.

### 3.4. Sunucudan

**`houseTanks.controlGranted`**: `{sessionId, playerId, runtimeEpoch, controlGeneration}`.
Gateway, bağlantıyı kendiliğinden bind eder: faz `playing` ise ve oyuncu ayrılmamışsa
(`hasLeft = false`). İstemci bind mesajı göndermez. Grant ve snapshot'ların geliş sırası
garanti değildir; ikisi de saklanır.

**`houseTanks.snapshot`**:

| Alan | Anlamı |
|---|---|
| `sessionId`, `gameKey` | Oturum ve oyun (`houseTanks`); başka oturumun karesi atılır. |
| `runtimeEpoch`, `stateSequence` | Sıralama: `(runtimeEpoch, stateSequence)` sözlük sırasıyla artar; eski kare atılır. |
| `tick` | Dünya tick'i (120 Hz). Oturum `running` iken hiç durmaz. |
| `phase` | Çekirdek fazı: `countdown`, `playing`, `recovering`, `finalizing`, `ended`, `cancelled` |
| `mapId`, `mapVersion`, `seed` | Maçın haritası; `seed` 16 haneli hex metin (yalnız bilgi, istemci kullanmaz) |
| `roundPhase` | `roundCountdown`, `roundPlaying`, `roundReady`, `matchEnded`; yalnız lobi önizlemesinde `null` |
| `roundNumber` | 1'den başlar; önizlemede 0 |
| `countdownEndsAt` | Lobi geri sayımının sonu (yalnız `phase = countdown`) |
| `roundCountdownEndsAt` | Tur geri sayımının sonu (yalnız `roundCountdown`) |
| `roundReadyWindowEndsAt` | Oy penceresinin sonu (yalnız pencere açıkken) |
| `players[]` | Koltuk sırasında: `playerId`, `displayName`, `seat`, `color`, `armor`, `roundsWon`, `isAlive`, `connected`, `hasLeft`, `roundReady`, `powerUp`, `powerUpExpiresAtTick`, `lastProcessedInputSequence`, `controlGeneration` |
| `tanks[]` | Koltuk sırasında: `playerId`, `x`, `y`, `vx`, `vy`, `rotation`, `rotationDirection` (±1), `isDriving`, `aimLocks`, `controllable` |
| `crates[]` | Haritanın kasaları: `index` (haritadaki engel sırası), `x`, `y`, `destroyed` |
| `token` | Yerdeki joker `{kind, x, y, expiresAtTick}` ya da `null` |
| `projectiles[]` | Canlı mermiler: `id`, `ownerId`, `x`, `y`, `vx`, `vy`, `rocket`, `spawnTick` |
| `events[]` | Son 1 sn'nin olayları (§3.6) |
| `roundWinnerId` | Son biten turun kazananı; berabere ya da tur sürerken `null` |
| `championId` | Yalnız sonuç commit edildikten sonra (`phase = ended`); öncesinde her zaman `null` |

Oyuncu alanlarının anlamı:

- `isAlive`: oyuncunun **tankı bu turda canlı mı**. Turda ölen oyuncu maçta kalır.
- `connected`: oyuncunun şu an geçerli bir kontrol bağlantısı var mı.
- `hasLeft`: oyuncu oturumdan açıkça ayrıldı ya da üyeliği kaldırıldı; geri dönemez.
  `connected = false, hasLeft = false` geri bağlanabilir demektir.
- `controlGeneration`: yalnız **izleyen bağlantının kendi** oyuncusunda, faz `playing`
  iken ve grant o bağlantıya aitken dolu; diğer herkeste `null`. Turda tankı ölen oyuncu
  da kendi neslini görür, çünkü oy vermesi gerekir.
- `color`: koltuktan gelir (`plum`, `orange`, `blue`, `olive`, `mustard`, `crimson`, `sky`,
  `ivory` sırasıyla). `seat` başlangıç noktasını da belirler.
- `powerUpExpiresAtTick` / `token.expiresAtTick`: kalan süre `(expiresAtTick − tick) / 120`.

**`houseTanks.result`**: kalıcı sonuç (§4.6).

### 3.5. Faz × tur aşaması

| `phase` | `roundPhase` | İstemci gösterir | İzinli girdi |
|---|---|---|---|
| `countdown` | `null` | Lobi geri sayımı; arena ve tanklar önizleme | — |
| `playing` | `roundCountdown` | Tur geri sayımı (`roundCountdownEndsAt`) | `press` kabul edilir, etkisiz |
| `playing` | `roundPlaying` | Oyun | `press` |
| `playing` | `roundReady` | Tur sonucu + hazır paneli | `roundReady`; `press` kabul edilir, etkisiz |
| `recovering` | son aşama | Arena donuk, "yeniden bağlanıyor" | — (yeni grant bekle) |
| `finalizing` | `matchEnded` | "Sonuç kaydediliyor"; şampiyon henüz yok | — |
| `ended` | `matchEnded` | Şampiyon (`championId`) ve sonuç | Rövanş |
| `cancelled` | `matchEnded` | İptal sonucu, şampiyonsuz | Rövanş |

İlk tur da `roundCountdown` ile başlar. Lobi geri sayımı (3 sn) ve ilk tur geri sayımı
(3 sn) art arda gelir; istemci bunları tek görsel akış olarak gösterebilir. Bu süre aynı
zamanda kontrol bind'i için tampondur.

`roundCountdown` ve `roundReady` sırasında gelen `press`, tur sınırındaki yarışlar hata
üretmesin diye kabul edilir ve yok sayılır; `lastProcessedInputSequence` yine ilerler.

### 3.6. Olaylar

Her olay `sequence` (runtime epoch'u içinde birer artar), `tick` ve `kind` taşır. Bir olay
1 sn boyunca her snapshot'ta tekrar gelir, böylece yavaş bağlantıda düşen kare olayı
kaybettirmez. İstemci `(runtimeEpoch, sequence)` ile tekilleştirir ve her olayı bir kez
oynatır (ses, efekt, parçacık). Ek alanlar türe göre vardır, diğerleri JSON'da yoktur:

| `kind` | Alanlar |
|---|---|
| `fired` | `playerId`, `angle` |
| `hit` | `playerId` (hedef), `sourceId` (atan), `armor` (kalan), `lethal` (yalnız `true` iken) |
| `shielded` | `playerId`, `sourceId` |
| `crateBroken` | `crateIndex`, `x`, `y` |
| `explosion` | `x`, `y` |
| `eliminated` | `playerId`, `sourceId`, `x`, `y` |
| `jokerCollected` | `playerId`, `powerUp` |
| `roundEnded` | `roundNumber`, `winnerId` (berabere ise yok) |

Tur sonu yalnız olaydan okunmaz; `roundPhase` ve `roundWinnerId` durumu da taşır.

### 3.7. Hata kodları

`gameSession.commandRejected` içindeki `error.code`. Yerelleştirilmiş metinden kod
türetilmez.

| Kod | Ne zaman | İstemci |
|---|---|---|
| `houseTanks.error.stale_control` | Eski nesil, eski epoch, eski/tekrar sıra; 6 sn ping gelmediği için düşmüş kontrol | Girdiyi at; güncel grant'i bekle ya da `resync`. Sonsuz döngüye sokma. |
| `houseTanks.error.player_eliminated` | Tankı bu turda ölü oyuncunun `press`'i; ayrılmış oyuncu | Tur içi ölüm: "bu turda öldün", maçtan elenme değil. |
| `houseTanks.error.control_unavailable` | Oyuncu maçta yok | Kontrol yok; izleyici olarak göster. |
| `houseTanks.error.not_running` | Maç bitmiş ya da başlamamış | Girdiyi kes. |
| `houseTanks.error.invalid_input` | Yanlış aşamada oy, bozuk payload | Hata kaydı; kullanıcıya gösterme. |
| `realtime.error.rate_limited` | Saniyede 30 `press`'ten fazla ya da oyuncunun girdi kuyruğu dolu | Hızı düşür. |
| `realtime.error.unavailable` (`retryable: true`) | Sahip geçici meşgul | Kısa backoff ile tekrar. |

Sunucu kuyruğunda 1 sn'den uzun bekleyen basış hata üretmeden atılır. Bu yüzden istemci
tankın gerçek durumunu (`isDriving`, `fired` olayı) snapshot'tan okur, basışın uygulandığını varsaymaz.

HTTP tarafı (yerelleştirilmiş, 0053 migration'ı): `houseTanks.error.result_not_found`,
`result_conflict`, `invalid_result`, `finalizing`, `runtime_completion_required`.

### 3.8. Koordinat, açı ve zaman

- Arena 872 × 400 nokta, **orijin sol alt, y yukarı**. İstemci arenayı ekrana sığdırarak
  çizer; arena boyutu cihaza göre değişmez.
- Açılar radyan, +x'ten **saat yönünün tersine**. `rotation` normalize edilmez (çok
  turdan sonra 2π'nin katlarını aşabilir); çizimde normalize edilir, karşılaştırmada fark
  `NormalizedAngle` ile alınır.
- Zaman tick'tir (1/120 sn). Sunucu son tarihleri (`*EndsAt`) UTC ISO-8601'dir, kesirli
  saniye içerebilir. Geri sayımlar bu son tarihlerden çizilir, yerel sayaçtan değil.
- Kimlikler (`playerId`, `sessionId`) opak metindir; UUID'ye zorlanmaz.
- `seed` istemci tarafından kullanılmaz. Harita `mapId` + `mapVersion` ile katalogdan
  seçilir; jokerler ve doğma noktaları sunucudan gelir.

## 4. Davranış

### 4.1. Kontrol, bağlantı ve kurtarma

- **Bind.** Socket açılınca welcome ve session snapshot gelir; oyuncu roster'da değilse
  `gameSession.join`, hazırsa `ready`. Maç başlarken gateway bind eder ve
  `controlGranted` gönderir. Grant, eşleşen `runtimeEpoch` ve kendi `controlGeneration`'ı
  görünen bir `playing` snapshot'ı olmadan düğme açılmaz.
- **Kopma elenme değildir (ADR-029).** Socket kapanır ya da ping kesilirse kontrol düşer;
  tank arenada **hareketsiz hedef** olarak kalır (`controllable = false`): dönmez, sürmez,
  ateş etmez, vurulabilir ve itilebilir. Oyuncu geri bağlanınca yeni grant gelir ve kontrol
  bir sonraki tick'te döner. Kısa kopmada sınırlı backoff ile yeniden bağlan, tam durum al.
- **İkinci cihaz.** Aynı oyuncunun yeni bağlantısı kontrolü devralır, yeni nesil alır; eski
  bağlantının girdileri `stale_control` alır. Eski bağlantı yeniden bind yarışına girmez.
- **Kurtarma.** Sahip instance kaybolursa başka instance devralır:
  - `runtimeEpoch` artar; önce `phase = recovering`, sonra `playing` kare gelir. Recovering
    kare kaybolabilir; doğrudan yüksek epoch'lu `playing` de gelebilir.
  - Tüm tanklar yeniden bind edilene kadar `controllable = false`; her oyuncu yeni grant alır.
  - Kesinti için tick harcanmaz; dünya son checkpoint'ten (≤ 1 sn eski, kritik değişikliklerde
    anında) devam eder. Kuyruktaki basışlar tekrar oynatılmaz.
  - Oy penceresindeki oylar düşer; dönen oyuncu yeniden oy verir.
  - Yeni epoch eski tahminleri, sıra sayaçlarını ve olay tekilleştirme durumunu geçersiz kılar.
- **Ayrılma.** Açık "Çık" `gameSession.leave`'dir ve kalıcıdır: oyuncu bu maça dönemez,
  tankı hareketsiz kalır. Ekran kapanması, arka plana gitme ya da socket kopması ayrılma
  değildir.
- **Arka plan.** Online maç duraklamaz. Arka planda ping ve girdi durur; ön plana dönüşte
  `resync`.

### 4.2. Tur akışı

- Tur, canlı tek tank kalınca biter; hareketsiz hedefler de canlı sayılır. Son tanklar aynı
  tick'te yok olursa tur berabere biter, kimse puan almaz ama tur `roundsPlayed`'e sayılır.
- 3 tura ulaşan olursa `matchEnded`; yoksa `roundReady`. Oy kuralları:
  - bağlı herkes hazırsa (en az 2) hemen `roundCountdown`;
  - en az 2 hazır olunca 30 sn pencere açılır (`roundReadyWindowEndsAt`); pencerede bağlı
    herkes hazır olursa hemen başlar, hazır sayısı 2'nin altına düşerse pencere kapanır;
  - pencere bitince bağlı herkes oynar, bağlı olmayanların tankı hareketsizdir;
  - pencere 2 dk boyunca hiç açılmazsa maç `cancelled` / `sessionExpired` ile biter.
- Tankı ölen oyuncu kontrolünü korur ve oy verir; hazır paneli herkese gösterilir.
- Her turda kasalar yenilenir, tanklar koltuklarının başlangıç noktalarına döner, zırh 5'e
  döner, mermi ve joker temizlenir. Joker torbası maç boyunca devam eder.
- Maç 5 dk'yı aşarsa `cancelled` / `sessionExpired`, o ana kadarki skorlar korunur.
- Ayrılmamış oyuncu 2'nin altına inerse maç hemen `insufficientPlayers` ile iptal olur. Bağlı
  oyuncu 10 sn boyunca 2'nin altında kalırsa da aynı sonuç olur. Maç başında ve kurtarmadan
  sonra 16 sn boyunca herkes bağlı sayılır.

### 4.3. Girdi ve gecikme telafisi

- Tek düğme: basılı tutmak tankı sürer, basış anında ateş eder. Bırakınca tank durur ve
  dönmeye devam eder.
- Kısa dokunuş yutulmaz: aynı kare içinde gelen bas + bırak, sunucuda bas bir tick
  uygulanır, sonra bırak.
- Tank sürekli döndüğü için basış sunucuya ulaştığında namlu başka yöne bakar (50 ms'de
  ~4°). Sunucu bunu şöyle düzeltir: `clientTick` son 150 ms içindeyse (en fazla 2 tick
  ileride), tank bu aralıkta sürülmemişse ve nişan kilidi yoksa, `heading` tankın o aralıkta
  süpürdüğü yay ± 0,035 rad içindeyse tankın açısı `heading`'e çekilir ve atış oradan yapılır. Aksi halde sunucu
  açısı kullanılır; basış reddedilmez.
- Bu yüzden `clientTick` ve `heading` **ekranda görünen** durumdan alınmalıdır (ara
  değerlenmiş/tahmin edilmiş kendi tankın açısı ve onun tick'i). Uydurulmuş değer avantaj
  sağlamaz, yalnız sunucu açısına düşer.
- Kendi tankın için sınırlı tahmin yapılabilir (dönüş deterministik: 1,5 rad/sn,
  `rotationDirection` yönünde). Sunucu karesi geldiğinde uzlaştırılır; isabet, ölüm, kasa
  kırılması ve kazanan sunucudan gelir, istemci üretmez.

### 4.4. Render

- Snapshot 20 Hz gelir. Diğer tanklar ve kasalar ara değerle çizilir; başlangıç tamponu
  ölçümle ayarlanır (wire kuralı değildir).
- Mermiler düz çizgide gider: istemci her mermiyi snapshot'taki konum ve hızdan
  `(tick − snapshotTick) / 120` ile ilerletir. Duvara çarpan ya da süresi biten mermi bir
  sonraki karede listeden düşer.
- Enkaz: ölen tank `tanks[]`'ta kalır (`isAlive = false` oyuncuda) ve engel olarak çizilir.

### 4.5. Bant genişliği — sıkıştırmayı aç

8 oyunculu ağır atışta bağlantı başına kare ~12 KB, 20 Hz'de ~230 KB/s'dir (872 × 400 arena).
Karenin yaklaşık yarısı mermilerdir. permessage-deflate ile kare ~3,1 KB'a (~60 KB/s) iner. İstemci WebSocket
bağlantısında **permessage-deflate teklif etmelidir**. Gateway bunu bağlantı başına
kabul eder; teklif etmeyen istemci de çalışır, ama mobil veride pahalıdır.

### 4.6. Sonuç ve rövanş

`houseTanks.result` ve HTTP sonucu byte byte aynıdır:
`sessionId`, `houseId`, `gameKey`, `protocolVersion`, `mapId`, `mapVersion`, `status`,
`endReason`, `championId`, `roundsPlayed`, `startedAt`, `endedAt`, `durationSeconds`,
`players[] {playerId, roundsWon, finalRank}`.

- `completed` yalnız `championDecided` ile olur: tek şampiyon 3 galibiyetle, `finalRank`
  galibiyete göre, eşitler aynı sırayı paylaşır (1, 2, 2, 4).
- Diğer her son `cancelled`'dır (`insufficientPlayers`, `cancelledByUser`, `sessionExpired`,
  `recoveryFailed`, `coordinationUnavailable`, `runtimeOverloaded`): tur skorları korunur,
  `finalRank` ve `championId` `null`'dır. İstemci iptalde sıra ya da şampiyon üretmez.
- `mapId` / `mapVersion` yalnız maç hiç başlamadıysa `null` olabilir (fixture'da
  `cancelledBeforeStartResult` şema örneği). Tick oyunlarında sonuç yalnız başlamış oturum
  için yazıldığı için sunucu bu şekli bugün üretmez; istemci yine de decode edebilmelidir.
- `finalizing` sırasında sonuç beklenir. Sonuç mesajı kaybolursa eski `sessionId` ile HTTP
  sonuç okunur; commit bekleyen 404 sınırlı tekrarla denenir. Aynı sonuç iki kez gelirse
  `sessionId` ile tekilleştirilir.
- Rövanş: `PUT /game/houseTanks/session` yeni (ya da başkasının açtığı) lobiyi döndürür;
  gerekiyorsa join, sonra yeniden ready. Eski oturum yerelde sıfırlanmaz. Rövanş paneli oyun
  ekranındadır; lobi ekranına dönülmez.

## 5. Faz 0 — Swift motorunun Go fixture'larına uydurulması

Referans Go motorudur. Swift motoru (`FausCrewCore/Games/HouseTanks/`) bu fixture'ları
geçecek şekilde değiştirilir. Bunun nedeni yerel bot modunun ve online tahmin/render'ın,
sunucuyla aynı fiziği kullanmasıdır. Backend okumasına göre (24 Ekim) motorda şu işler
henüz yok:

1. **Sabit arena.** `resize(to:)` kalkar; dünya her zaman 872 × 400 noktadır.
2. **Tam sayı tick.** Değişken `step(by:)` yerine sabit 1/120 sn adım. Tüm süreler tam tick
   sayısıdır, saniye değil: ateş 70, hızlı ateş 20, vuruş koruması 44, mermi ömrü 336, roket
   ömrü 384, joker etkisi 960, yerdeki joker 1440, joker aralığı 840–1320, yeniden deneme
   120. (Swift'in `elapsed >= duration` kontrolünün geçtiği ilk tick, yukarı yuvarlanmış.)
3. **Harita kataloğu `houseTanksMaps.json`'dan.** Standart haritalar (livingRoom, corridors,
   warehouse, courtyard) 5. başlangıç noktasıyla 2–5 oyuncuyu destekler, sürüm 3 (courtyard 4). Bodrum
   (`basement`, ölçek 0,72) 6–8 oyuncu, sürüm 1, değişmedi. Swift kataloğu elle
   güncellenmez; fixture'dan okunur ya da fixture'dan üretilir.
4. **Kaydet/yükle** (bot modu ve test için): dünya durumu serileştirilebilir olmalıdır.

**Eşitlik testi.** `houseTanksSimulation.json`'daki her senaryo için:

- `mapId` ve `players` sayısıyla dünya kurulur. Koltuklar başlangıç noktalarını sırayla
  alır, arena merkezine bakar; çift koltuk saat yönünün tersine, tek koltuk saat yönünde
  döner.
- Senaryonun `tanks` alanı koltuk pozunu ezer (`firedAtTick` namluyu o tick'te ateş etmiş
  sayar, `wreck` ölü tank, `powerUp` jokeri tick 0'dan tam süre verir). `token` yere joker
  koyar.
- `inputs` dünya o tick'teyken, bir sonraki adımdan önce uygulanır: `press`, `release`,
  `loseControl` (hareketsiz hedef), `regainControl`.
- Her adım şu sırayla çalışır: tick += 1; yerdeki joker ve etkilerin süresi; tanklar (sürüş,
  hızlı ateş, dönüş, sönüm, hız sınırı, hareket); kasalar (sönüm, hareket); 3 temas geçişi;
  joker toplama; atışlar (önce namlu isabetleri); isabetler sırayla.
- Her `checkpoints[]` tick'inde tanklar, kasalar, mermiler, yerdeki joker ve olaylar
  `absoluteTolerance` (1e-6) ile karşılaştırılır. Açılar normalize edilmez.
- Senaryolarda joker dağıtımı kapalıdır. Joker kuralları dizi eşitliğiyle değil, kural
  testleriyle doğrulanır.

Fixture'ın `rules` ve `constants` alanları bu metnin kaynak hâlidir. Bir senaryo
geçmiyorsa önce Swift tarafı düzeltilir. Swift davranışı briefing'e daha uygun görünüyorsa
fark backend'e hata olarak bildirilir, fixture istemcide değiştirilmez.

**Go'nun Swift'ten bilinçli sapmaları** (Swift buna uyarlanır):

- Vadesi gelen yeni joker, yerde toplanmamış joker varsa onun yerini alır. Swift yerde
  joker varken yenisini doğurmuyordu.
- Son tanklar aynı tick'te yok olursa tur berabere biter. Swift isabetleri tek tek işleyip
  ilk vuranı kazandırıyordu.
- Yeni joker torbası önceki torbanın son türüyle başlamaz.
- Rastgelelik (tohum, harita seçimi, joker yerleri) yalnız sunucudadır. Online modda istemci
  bunları üretmez; bot modunda Swift kendi üretecini kullanabilir, dizi eşitliği aranmaz.

**Protokol fixture'ı.** `serverMessages` örneklerinin hepsi decode edilip yeniden encode
edildiğinde aynı JSON'u vermelidir (nullable ve atlanmış alanlar dahil). `clientMessages`
örnekleri istemcinin ürettiği JSON'la aynı olmalıdır. `invalidClientMessages` istemcinin asla
üretmemesi gereken biçimlerdir; her biri `errorCode` ile reddedilir.

## 6. Mobil fazlar ve çıkış ölçütleri

Fazlar sırayla yürür. Her fazdan sonra değişen dosyalar, çalıştırılan testler ve canlı
kabulün durumu ayrı raporlanır; mock testi canlı kabul diye yazılmaz.

**F0 — Motor eşitliği (§5).** Sabit arena, tam tick, fixture kataloğu, kaydet/yükle; 17
senaryo 1e-6 ile geçer; yerel bot modu çalışmaya devam eder.
*Çıkış:* eşitlik testi istemci CI'ında; fixture sha256'ları §2 ile aynı.

**F1 — Wire DTO ve transport.** §3'ün tüm mesajları ayrı wire tipleriyle; protokol
fixture'ının tüm örnekleri decode/encode testinde. String kimlik, Int64 tick/sıra/epoch,
nullable ve atlanmış alanlar, kesirli UTC, sekiz renk. permessage-deflate teklif edilir.
House Rockets'in ortak transport'u yeniden kullanılır.
*Çıkış:* fixture testleri, `invalidClientMessages`'ın hiçbirinin üretilemediği testler,
HTTP 401/403/404/409/429/503 ve iptal testleri.

**F2 — Lobi ve maç başı.** PUT → socket → welcome + session snapshot → join → ready; lobi
geri sayımında önizleme arenası (`roundPhase = null`); harita katalogda yoksa güncelleme
uyarısı; ping 2 sn'de bir.
*Çıkış:* mock lobi testleri; ortam açıksa iki hesap aynı odada geri sayımı ve haritayı görür.

**F3 — Oynanış.** Grant + epoch + kendi nesli olmadan düğme yok; bas/bırak yalnız
değişimde, sıra numarası nesle göre; `clientTick` + `heading` ekrandaki durumdan; ara değer,
mermi ilerletme, olay tekilleştirme, enkaz, joker göstergeleri; ölü tankta "bu turda öldün"
(`player_eliminated` ≠ elenme).
*Çıkış:* geciken/tekrarlanan kare, grant/snapshot sırası, dokunuş, ACK ve olay tekrar
testleri; canlı iki istemci birbirini görür, birbirini vurur.

**F4 — Tur akışı ve sonuç.** Tur geri sayımı, hazır paneli ve pencere sayacı, tankı ölen
oyuncunun oyu, berabere tur, şampiyon yalnız `ended`'da; iptal sonuçları, HTTP fallback,
rövanş.
*Çıkış:* tam maç (3 galibiyet) → sonuç → rövanş canlıda; iptal türleri mock'ta.

**F5 — Kopma, kurtarma ve yaşam döngüsü.** Hareketsiz hedef gösterimi (`controllable =
false`, `connected = false`), yeniden bağlanma, ikinci cihaz devri, epoch değişimi ve
`recovering`, arka plan/ön plan `resync`, açık çıkış, logout/ev değişimi temizliği.
*Çıkış:* kopma/geri dönüş, devir, epoch değişimi ve temizlik testleri; sahip kaybı
staging'de backend ile koordineli doğrulanır.

**F6 — Gerçek cihaz kabulü.** İki (ideal olarak 4–8) gerçek cihazla tam maç; 100/200/400 ms
RTT, jitter, kısa kopmalar; desteklenen FPS'ler; iPhone/iPad ve Android. Yayın kararı
backend ile ortaktır; istemci tek başına production'da açmaz.

## 7. Canlı test ortamı

- Sürüm henüz deploy edilmedi. Deploy'da migration 0053 (hata mesajları) ve 0054 çalışır;
  0054 oyunu tüm evlerde, kod varsayılanlarıyla açar (`houseIds` boş). Deploy olana kadar
  Fly adresinde oyun yoktur.
- Operatör sonradan oyunu kapatabilir, evlerle sınırlayabilir ya da ayarları değiştirebilir
  (super admin; güncel `version` `GET /game/configs`'tan okunur):

  ```text
  PUT /api/v1/game/houseTanks/config
  {"expectedVersion": <güncel version>, "enabled": true, "houseIds": []}
  ```

  `houseIds` dolu olduğunda oyun yalnız o evlerde görünür. Ayarlar (`settings`) ve lobi
  kuralları (`rules`) aynı istekle değiştirilebilir; yeni oturumlara dondurulur.
- Açıldıktan sonra istemci `GET /game/catalog?houseId=…` ile doğrular. Base URL, test
  hesapları ve aynı ev üyelikleri güvenli yoldan sağlanır; token ya da secret belgeye veya
  log'a yazılmaz.
- Canlı erişim yoksa DTO, mock ve fixture işleri sürer; yalnız canlı kabul "ortam
  engelli" olarak raporlanır.

## 8. Backend'in yerelde doğruladıkları

İstemci kendi testlerini tasarlarken referans olarak (`tests/houseTanks*_test.go`):

- İki instance: HTTP ile oturum, lobi geri sayımında harita, grant'ler, uzak instance'tan
  basış ve ACK, eski sıra `stale_control`, erken oy `invalid_input`, resync, ayrılma →
  `insufficientPlayers`, sonuç socket'te ve HTTP'de byte byte aynı, rövanş yeni oturum.
- Oy penceresinde sahip kaybı: skor korunur, ölü tankın sahibi dahil herkes yeni grant
  alır, oylar düşer, yeniden verilen oylar 2. turu başlatır.
- Tur ortasında süreç öldürme: tur olduğu yerden döner, tanklar hareketsiz, eski nesil
  reddedilir, yeni basış onaylanır.
- Runtime: dokunuşun yutulmaması, gecikme telafisinin kabul/ret sınırları, ölü tankın oyu,
  ayrılanın bind edilememesi, kontrol zaman aşımı → hareketsiz hedef → geri bağlanma.

## 9. İstemci agent'ına verilecek görev

> `houseTanksClientIntegration.md`'yi baştan sona oku; ortak transport için
> `houseRocketsMultiplayerPlan.md` §5–7'ye bak. Fixture'ları §2'deki sha256 ile doğrulayarak
> istemci test kaynaklarına kopyala. Önce Faz 0'ı yap: Swift FauseTanks motorunu sabit
> arena, tam tick ve fixture kataloğuna geçir ve 17 fizik senaryosunu 1e-6 ile geçir; yerel
> bot modunu koru. Sonra F1–F6 sırasıyla online FauseTanks'i bağla. Her fazın çıkış
> ölçütlerini doğrula; kod, mock ve canlı kabul durumlarını ayrı raporla. Wire sözleşmesini,
> ürün kurallarını ya da fizik sabitlerini tek taraflı değiştirme; fixture ile uyuşmazlığı
> somut farkla backend'e bildir. Production'ı açma. Ortam ya da hesap yoksa DTO/mock
> geliştirmesine devam et, yalnız canlı kabulü engelli olarak belirt. Secret paylaşma;
> commit/build/test izinleri için kullanıcının ve mobil deponun talimatlarını izle.
