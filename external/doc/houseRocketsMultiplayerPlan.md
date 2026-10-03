# House Rockets — Backend ve iOS ortak geliştirme planı

Tarih: 3 Ekim 2026.

Durum: **Paket 1 backend sözleşmesi geliştirildi ve test edildi.** Sözleşme
revision'ı `houseRockets.v2.1`, fixture schema'sı `1`, protocolVersion `2`,
courseVersion `1`. Go tanımı, wire DTO'ları, bağımsız v2 decoder ve ortak JSON
fixture'ları mevcut. **House Rockets hâlâ canlı katalog/gateway'de açık değil;**
oyun motoru, endpoint aktivasyonu ve kalıcı sonuç davranışı sonraki paketlerde.
Mobil implementasyonu ve gerçek iki cihaz entegrasyonu henüz doğrulanmadı.

## 1. Amaç ve onaylanmış ürün sınırı

House Rockets iki açık oyun seçeneğine sahip olacak:

- **Botlarla oyna:** Bir insan ve 1–3 yerel bot. İnternet, backend oyun oturumu
  ve WebSocket gerektirmeyen mevcut oynanışın devamı.
- **Ev arkadaşlarınla oyna:** Aynı evin gerçek üyelerinin katıldığı online maç.
  Bu modda bot oluşturulmayacak; eksik oyuncu botla tamamlanmayacak.

Mod seçimi kullanıcıya açıkça sunulacak. Online bağlantı başarısızlığı bot moduna
otomatik geçiş üretmeyecek. Bot modu sonucu online maç sonucu veya evin
leaderboard kaydı olarak gönderilmeyecek.

İlk online oyun House Rockets olacak. Ortak GameSession ve realtime altyapısı
başka oyunların kullanımına uygun kalacak; roket fiziği ortak oturum domain'ine
eklenmeyecek. Bu plan diğer oyunları online'a geçirme işi içermez.

Bu belgeyi kullanan backend ve mobil agent'ı aynı sözleşme üzerinden çalışır.
Wire format, kimlikler, koordinat sistemi veya hata davranışı tek tarafta
değiştirilmez. Değişiklik bu dosyaya, ortak fixture'lara ve iki tarafın
uygulama/testlerine birlikte yansıtılır.

## 2. İncelemenin kapsamı ve mevcut durum

İncelenen repository'ler:

- Backend: `/Users/frowing/Projects/houseflowApi`.
  İnceleme HEAD'i: `985a8816b044c47a48e8009fccd8ef7a4eb9604d`.
- iOS: `/Users/frowing/Projects/houseflowApp`.
  İnceleme HEAD'i: `18c183d0b38dbccb753ebb005ee901a8bb4eedd6`.

Uygulamaya başlamadan önce agent kendi checkout'undaki değişiklikleri kontrol
eder. Aşağıdaki bulgular bu HEAD'lerdeki koda aittir; testler bu planlama
çalışmasında çalıştırılmamıştır.

### 2.1. Backend'de kullanılacak mevcut parçalar

| Mevcut dosya | İşlevi ve plan açısından önemi |
| --- | --- |
| [gameSession.go](/Users/frowing/Projects/houseflowApi/internal/application/game/domain/gameSession.go) | Lobi, ready window, countdown, running, finished/cancelled lifecycle'ı; oyun fiziği içermez. |
| [gameCatalog.go](/Users/frowing/Projects/houseflowApi/internal/application/game/gameCatalog.go) | Yalnız `flappyBird` kaydı mevcut; House Rockets kaydı eklenecek. |
| [gameController.go](/Users/frowing/Projects/houseflowApi/internal/controllers/gameController.go) | Aktif oturum oluşturma/bulma HTTP işlemleri. Oluşturma oyuncuyu otomatik katmaz. |
| [gameModels.go](/Users/frowing/Projects/houseflowApi/internal/models/dtos/gameModels.go) | HTTP'de camelCase JSON ve milisaniye cinsinden lobi süreleri. |
| [roomManager.go](/Users/frowing/Projects/houseflowApi/internal/infrastructure/realtime/roomManager.go) | Oda lease'i, sıralı lifecycle komutları ve lobi deadline'ları; henüz fizik döngüsü yok. |
| [gateway.go](/Users/frowing/Projects/houseflowApi/internal/infrastructure/realtime/gateway.go) | Yetkili upgrade, oda hub'ı, presence, sınırlı outbound kuyruk ve ilk session snapshot'ı. |
| [protocol.go](/Users/frowing/Projects/houseflowApi/internal/infrastructure/realtime/protocol.go) | Sürüm 1; join/setReady/leave/cancel dışında oyun girdisi kabul etmiyor. |
| [coordinator.go](/Users/frowing/Projects/houseflowApi/internal/application/coordination/abstract/coordinator.go) | Redis lease/fencing, kalıcı komut aktarımı ve geçici oda event dağıtımı sözleşmeleri. |
| [gameSessionRepository.go](/Users/frowing/Projects/houseflowApi/internal/data/database/gameSessionRepository.go) | Mongo optimistic concurrency, command receipt ve transactional outbox. |

Mevcut gerçek HTTP ve WebSocket yolları:

```text
PUT /api/v1/game/:gameKey/session
GET /api/v1/game/:gameKey/session?houseId=:houseId
GET /api/v1/game/:sessionId/realtime
```

`PUT` body: `{ "houseId": "<houseId>" }`. Başarılı HTTP cevapları
`{ "success": true, "data": ... }`, hatalar `{ "success": false, "error": ... }`.
`houseRockets` henüz katalogda bulunmadığı için bu game key şu an çalışmaz.

Backend'deki önemli boşluklar:

- WebSocket session snapshot'ı HTTP DTO'su yerine doğrudan Go domain struct'ını
  JSON'a çeviriyor. `SessionID`, `PlayerID`, `Rules` gibi alanlar PascalCase;
  `time.Duration` alanları nanosaniye olarak serileşiyor. HTTP DTO'suyla aynı
  format olduğu varsayılmayacak.
- Gateway varsayılanı 10 saniyede 30 mesaj. Direksiyon girdisi için ayrı
  doğrulama, rate limit ve aktarım yolu gerekiyor.
- `GameSession.Finish` domain metodu var; oyun sonucunu kaydedip oturumu
  tamamlayan application command'ı ve oyun sonuç repository'si henüz yok.
- Redis fencing şu an oda event yayınında doğrulanıyor. Oyun checkpoint'i ve
  kalıcı sonuç yazımı için ayrıca sahiplik koruması gerekecek.
- Reconnect yalnız lifecycle snapshot'ını getiriyor. Running bir maçın
  koordinatlarını, efektlerini veya elenmiş oyuncularını geri kuramıyor.
- Lease TTL varsayılanı 10 saniye, yenileme 3 saniye. Owner kaybından toparlanma
  süresi bu değerlerden bağımsız veya anlık kabul edilemez.

### 2.2. iOS'taki mevcut oyun yapısı

| Mevcut dosya | Bulgu ve gerekli uyarlama |
| --- | --- |
| [GamesHubView.swift](/Users/frowing/Projects/houseflowApp/HouseFlow/Views/Discover/GamesHubView.swift) | House Rockets ekranını varsayılan initializer ile açıyor; production oyun bağımlılığı taşımıyor. |
| [HouseRocketsView.swift](/Users/frowing/Projects/houseflowApp/HouseFlow/Views/Discover/Games/HouseRockets/HouseRocketsView.swift) | Varsayılan servis demo; bot seçimi, landscape bekleme, pause, sonuç ve rematch burada. `scenePhase` değişimi yerel pause çağırıyor. |
| [HouseRocketsViewModel.swift](/Users/frowing/Projects/houseflowApp/HouseFlow/ViewModels/Games/HouseRocketsViewModel.swift) | Sıra numaralı niyet gönderiyor; tek revision filtresi var; her komut önceki Task'i bekliyor. Online direksiyon için kuyruk birikmesi önlenmeli. |
| [HouseRocketsGameServicing.swift](/Users/frowing/Projects/houseflowApp/HouseFlow/Services/Games/HouseRocketsGameServicing.swift) | Servis enjeksiyonu mevcut; ancak `start(botCount)`, `scene`, pause/resume ve yalnız snapshot akışına bağlı. Online lobby ve bağlantı hataları için yeterli değil. |
| [DemoHouseRocketsSession.swift](/Users/frowing/Projects/houseflowApp/HouseFlow/Services/Games/DemoHouseRocketsSession.swift) | Bir insan ve botları üretir; sonucu yerel hesaplar. Snapshot/bot görev aralığı 120 ms, fizik sahnede çalışır. Countdown'ın her sayısı 750 ms bekler. |
| [HouseRocketsModels.swift](/Users/frowing/Projects/houseflowApp/HouseFlow/Models/Games/HouseRocketsModels.swift) | Oyuncu ve match kimlikleri UUID; isim `nameKey`; yerel `.human` ve `.remote` rolleri var. Wire DTO değildir. |
| [HouseRocketsSimulation.swift](/Users/frowing/Projects/houseflowApp/HouseFlow/Models/Games/HouseRocketsSimulation.swift) | Ekrandan bağımsız dünya fiziği; 1/120 s alt adımlar; lider kamera takibi, yüzey teması ve hız alanları. |
| [HouseRocketsCourse.swift](/Users/frowing/Projects/houseflowApp/HouseFlow/Models/Games/HouseRocketsCourse.swift) | İndeksle belirlenen parkur geometrisi; her üretimde rastgele UUID; zamana bağlı parkur dönüşü ve viewport projection. |
| [HouseRocketsScene.swift](/Users/frowing/Projects/houseflowApp/HouseFlow/Views/Discover/Games/HouseRockets/HouseRocketsScene.swift) | Simülasyonu sahiplenir, `update` ile ilerletir ve çizimi simulation state'inden üretir. Online snapshot uygulama yolu yok. |
| [HouseRocketsTests.swift](/Users/frowing/Projects/houseflowApp/HouseFlowTests/HouseRocketsTests.swift) | Geometri, yön, hız, kamera, elenme ve dönüşler için mevcut regresyon senaryoları. |
| [AppDependencies.swift](/Users/frowing/Projects/houseflowApp/HouseFlow/Services/AppDependencies.swift) | Ortak network/keychain grafiği; henüz oyun servisi veya realtime transport yok. |
| [NetworkService.swift](/Users/frowing/Projects/houseflowApp/HouseFlow/Services/Network/NetworkService.swift) | HTTP transport; enjekte edilebilir executor; HTTP status bilgisini üst katmana yapılandırılmış biçimde taşımıyor. |
| [AppEnvironment.swift](/Users/frowing/Projects/houseflowApp/HouseFlow/Services/Network/AppEnvironment.swift) | Development URL'i Fly HTTP API'si; local multiplayer denemesi için ayrı base URL seçimi gerekecek. |
| [AppViewModel.swift](/Users/frowing/Projects/houseflowApp/HouseFlow/ViewModels/AppViewModel.swift) | `currentUserId` ve `currentHouseDetails.id` mevcut. Online açılışta context buradan oluşturulabilir; token keychain bağımlılığından okunur. |
| [GameOrientationController.swift](/Users/frowing/Projects/houseflowApp/HouseFlow/Services/Games/GameOrientationController.swift) | Landscape kilidi ve failure callback'i kullanılabilir. |

Genel oyun taramasında RPS ve House Tanks'ın da service + command + snapshot
sınırları kullandığı, House Switch'in solo fizik akışı olduğu görüldü. Ortak
HTTP/session DTO'ları ve WebSocket transport tekrar kullanılabilir; bütün oyunları
tek generic servis veya tek fizik motoruna dönüştürme ihtiyacı yok.

## 3. Korunacak oyun kuralları ve karara bağlanacak ayarlar

### 3.1. Mevcut demodan korunacak kurallar

- Joystick yön belirler; büyüklüğü hızı değiştirmez. Tam 360° yön ve geri uçuş
  desteklenir. Parmak bırakılınca son parkur yönü korunur.
- Roketler birbirine çarpmaz ve birbirini itmez. Katı engel/ray teması doğrudan
  öldürmez; gerçek yüzey normaliyle hareket düzeltilir ve kayma mümkün olur.
- Dünya X ekseni parkur boyunca ilerleme, Y ekseni koridorun enidir. Temel hız
  300 birim/s, koridor eni 360, roket radius'u 10, spawn X'i 250'dir.
- Başlangıç Y dağılımı `105 + index * 150 / max(1, playerCount - 1)`.
  Oyuncu sırası ve renkleri server tarafından maç boyunca sabit tutulur.
- Kamera en öndeki hayatta kalan roketi 420 birim arka payla takip eder; geriye
  gitmez. Herkes engelde takılırsa kendiliğinden ilerlemez.
- `worldX + rocketRadius < cameraX` olunca roket tamamen arka sınırdan çıkmış
  sayılır. Fiziksel cihaz ekran ölçüsü elenme hesabına katılmaz.
- Tek hayatta kalan kazanır; aynı fizik adımında herkes elenirse beraberlik.
  Bütün oyuncuların adımı tamamlanmadan kazanan seçilmez.
- Parkur 25 saniyede bir yatay/dikey sunuma döner; dönüş 3 saniye sürer.
  Uyarı dönüşten 3 saniye önce başlar. Cihaz landscape kalır. Dönüş tek başına
  roketi elemez veya mevcut parkur yönünü değiştirmez.
- Boost: ×1,45 hız, 1,3 s. Slow: ×0,65 hız, 1,4 s. Alan radius'u 15,4.
  Boost periyodu 3 s, slow periyodu yaklaşık 3,6667 s; salınım
  `180 + sin(time * 2π / period + phase) * 112`.
- Alan etkisi oyuncu başına bir kez uygulanır; son etki öncekinin yerini alır.
  Her beş yerleşimden biri atlanır. İleri konum sabit, enine hareket zamana bağlıdır.
- Elenen oyuncu kalan oyunu izler. Yerel tahmin sonucu elenme veya kazanan
  ilan edilmez.

Mevcut parkur profile'ları ve contact algoritmasının ayrıntıları Swift kaynakları
ve ortak fixture'larla Go'ya aktarılacak. Başlangıçta rastgele parkur/seed sistemi
eklemek gerekli değil; mevcut indeksli parkur için `courseVersion` yeterli.

### 3.2. Paket 1 başlangıç kuralları

| Konu | Bu revision'ın başlangıç kararı |
| --- | --- |
| Online oyuncu sayısı | Kullanıcının onayıyla en az 2, ev kapasitesi kadar ve en fazla 8. Mevcut ensure-session handler house kapasitesine göre limiti daraltır. Bot eklenmez. |
| Ready window / countdown | 30 s / 3 s; otoriter server deadline'ları. Demodaki 750 ms countdown online'a taşınmaz. |
| Countdown sonrası katılım | Yeni yarışmacı alınmaz; ilk sürümde yalnız maç katılımcıları yeniden bağlanıp izleyebilir. Ev üyeliği socket erişimi için gerekli, yarışmacı olmak için tek başına yeterli değil. |
| Kontrol bağlantısı | Oyuncu başına tek kontrol sahibi; ikinci cihaz aktif kontrolü sessizce devralmaz. Kopmuş bağlantı güvenle devredilebilir. |
| Bağlantı kopması | Kısa grace boyunca son yönle ilerleme; reconnect oyuncuyu yeniden üretmez, elenmişse izleyici olarak döndürür. Önerilen grace 10 s. |
| App background | Online maç ilerler; kontrol girdisi durur, foreground'da resync yapılır. Yerel bot modunda mevcut pause devam eder. |
| Maçtan açıkça çıkış | Running'de forfeiture; grace beklenmez. Lobi/countdown'da mevcut lifecycle politikası. |
| Uzayan maç | Kullanıcının onayıyla online oynanış üst sınırı 5 dakika (300 s / 36.000 physics tick). Süre sonunda hâlâ yarış devam ediyorsa `sessionExpired` ile iptal; mesafeden kazanan seçilmez. Countdown ve bounded recovery süresi fizik süresine eklenmez. Demo süre sınırı değişmez. |
| Rematch | Sonuç ekranından yeni/aktif lobiye dönülür; eski maç yerelde resetlenmez. Yeniden hazır olma gerekir. |
| Owner kaybı | Geçici checkpoint'ten sınırlı geri düzeltmeyle toparlanma; checkpoint yok/geçersizse açık iptal. Kusursuz ve kesintisiz failover garantisi yok. |

İki mod, online'da botsuz oynama, en fazla 8 kişi ve 5 dakika sınırı kullanıcı
kararıdır. Diğer satırlar bu revision için teknik başlangıç politikasıdır;
önemli ürün değişikliği kullanıcıyla netleştirilir, tablo ve fixture birlikte
güncellenir. Ready window azami 30 s'dir; kapasitenin tamamı hazırsa ortak
session domain'inin mevcut kuralıyla countdown daha erken başlayabilir.

## 4. Sorumluluklar ve kod sınırları

### 4.1. Backend

- `internal/application/game/domain`: Ortak session lifecycle'ı.
- `internal/application/game/gameSpesific/houseRockets`: Paket 1 tanım/enum'ları mevcut;
  Paket 2'de game-specific state,
  kurallar, geometri ve bağımsız simülasyon; gerekirse `domain` ve `abstract`
  alt paketleri. Go paketi HTTP/WebSocket/SpriteKit/Redis tiplerine bağımlı olmaz.
- `internal/application/game/commands` ve `queries`: Kalıcı başlatma/tamamlama,
  sonuç okuma ve runtime'a ait business geçişler; mevcut CQRS kurallarını izler.
- `internal/infrastructure/realtime`: Socket transport, oda sahibi üzerinde
  runtime çalıştırma, clock/ticker, girdi yönlendirme ve state yayını.
- `internal/infrastructure/coordination`: Geçici instance'lar arası input yolu,
  fencing korumalı checkpoint ve lease adapter'ları.
- `internal/data/database` ve `internal/data/database/abstract`: Kalıcı oyun
  sonucu ve sahiplik generation'ının Mongo implementasyonu/uygun sözleşmeleri.
  Yeni `internal/data/game` dizini açılmaz.
- `internal/data/migrations`: Gerekli collection/index/validator migration'ları;
  mevcut migration adları değiştirilmez.
- `tests`: Domain, transport, persistence ve çoklu instance kabul testleri.

RoomManager, `gameKey` ile küçük bir runtime factory'den oyun implementasyonunu
seçebilir. Yalnız start/input/snapshot/stop gibi gerçekten gereken ortak sınır
çıkarılır; House Rockets geometri ve hız etkileri genel oyun motoruna taşınmaz.

### 4.2. iOS

Önerilen sorumluluklar; yeni dosya adları camelCase, Swift tip adları mevcut
PascalCase dil kuralına göre yazılır:

- `Services/Network/gameRealtimeTransport.swift`: URLSession tabanlı socket,
  auth header, send/receive, ping, bağlantı ve cancellation. Oyunu hesaplamaz.
- `Services/Games/gameSessionService.swift`: Mevcut HTTP session yolları,
  typed DTO/envelope ve hata eşleme.
- `Services/Games/onlineHouseRocketsSession.swift`: Session akışı, game DTO
  mapping, input coalescing, sıra/epoch takibi, resync ve snapshot akışı.
- `Models/Games/gameSessionModels.swift` ve `houseRocketsWireModels.swift`:
  Wire DTO'lar; Swift associated-value enum serileştirmesi API yerine kullanılmaz.
- `HouseRocketsViewModel`: Mod, lobby, connection ve presentation state;
  kullanıcı niyetleri; hata ve pending command durumları.
- `HouseRocketsScene`: Çizim; online modda authoritative snapshot + render buffer.
  `update` online maçın authoritative simülasyonunu ilerletmez.
- `DemoHouseRocketsSession` ve `HouseRocketsSimulation`: Yerel bot modu ve
  gerekiyorsa online yerel oyuncu tahmini için kullanılan kural parçaları.

Mevcut `HouseRocketsGameServicing`'e her oyun için bir generic framework
eklenmeyecek. Demo'ya özgü bot ayarı online start parametresi yapılmayacak.
Online session service ayrı başlatma/join/ready operasyonlarına sahip olabilir;
sunum tarafında ortak render state kullanılabilir. ViewModel veya küçük bir
coordinator seçilen moda göre uygun servisi yönetir.

Production servis/factory `AppDependencies` içinde oluşturulup GamesHub'a
enjekte edilir. Oyun ekranı kendi canlı network/keychain örneğini oluşturmaz.
Preview/test açıkça demo servisini enjekte edebilir. Her açılışın bağlamı
`houseId`, `localPlayerId`, seçilen mod ve session generation'ı ile yakalanır.
Ev değişimi/logout eski görevleri, socket'i ve prediction buffer'ını kapatır.

Stateful session ve SpriteKit scene her ekran/maç akışı için factory'den
oluşturulur; tek global scene veya tek global aktif-match singleton'ı paylaşılmaz.
Bağlantı/hata/lobby olayları online servis akışında tipli event olarak veya ayrı
published state olarak taşınır; snapshot gelmemesi tek başına hata bilgisi değildir.
Mevcut Xcode projesi app/test dizinlerini filesystem-synchronized root group
olarak tanımlıyor; yeni Swift dosyaları için gereksiz project.pbxproj entry
üretmeden target membership doğrulanır.

Mod seçiminden sonra ayrı yerel lobi ve online lobi sunulur. Online UI'da bot
sayısı, "online later" metni veya bütün maçı durduracak Pause düğmesi bulunmaz.
Menü açılırsa maçın devam ettiği gösterilir ve açık çıkış sunulur. TR/EN mode,
ready, reconnect, gerçek oyuncu elenmesi ve iptal metinleri mevcut localization
yapısına eklenir; gerçek isimler translation key olarak kullanılmaz.

## 5. Ortak ağ sözleşmesi — houseRockets.v2.1

Bu bölüm Paket 1'de sabitlendi. Go modelleri ve decoder fixture'larla test
ediliyor; **bu, canlı gateway'in v2 mesajlarını kabul ettiği anlamına gelmez.**
HTTP/socket aktivasyonu Paket 4, sonuç endpoint'i Paket 5 teslimidir.

Ortak referanslar:

- [Protokol fixture'ı](../../internal/application/game/gameSpesific/houseRockets/fixtures/houseRocketsProtocol.json): Geçerli/geçersiz
  client mesajları, welcome/session/countdown/playing/recovering/result/error
  örnekleri ve HTTP response'ları. `liveGatewayAvailable: false` kapısı bilinçli.
- [Fizik fixture'ı](../../internal/application/game/gameSpesific/houseRockets/fixtures/houseRocketsSimulation.json): Mevcut Swift
  kaynakları değiştirilmeden çalıştırılarak elde edilen 2/4/8 kişilik spawn,
  altı hareket senaryosu, geometri, dönüş, field salınımı ve yüzey teması.
  Kaynak SHA-256'ları dosyada. Go motor karşılaştırması Paket 2'de yapılacak;
  Paket 1 fizik motoru implementasyonu veya mobil test suite doğrulaması değil.
- `internal/application/game/gameSpesific/houseRockets/definition.go`: Oyun-specific sabitler
  ve enum'lar; ortak GameSession domain'ine fizik/puan mantığı eklenmedi.
- `internal/application/game/gameSpesific/houseRockets/models.go`: House Rockets wire modelleri.
- `internal/application/game/gameSpesific/houseRockets/protocol.go`: Oyuna özgü mesaj/hata tanımları.
  Ortak ready/ping/pong modelleri `internal/models/dtos/realtimeModels.go` içinde.
- `internal/infrastructure/realtime/protocolV2.go`: Bağımsız v2 validation ve
  server envelope'ları; mevcut gateway henüz bu decoder'ı çağırmıyor.
- `tests/houseRocketsContract_test.go`: JSON round-trip, kurallar, input
  sınırları, typed rejection ve mevcut v1/katalog izolasyonu testleri.

### 5.1. Sürüm ve HTTP

House Rockets için `protocolVersion: 2`. Mevcut v1 session WebSocket
payload formatını sessizce değiştirmek yerine yeni camelCase sözleşme sürümü
tanımlansın. `flappyBird` v1 tanımı ve mevcut regression testleri korunur.
House Rockets katalog tanımı v2; bu kayıt ancak ilgili runtime/protokol hazırken
production'da açılır.

HTTP session oluşturma/keşfetme aynı yolları kullanır:

```text
PUT /api/v1/game/houseRockets/session
GET /api/v1/game/houseRockets/session?houseId=<houseId>
```

`PUT` retry'sı aynı house/game için aktif session'a döner. Oluşturma sonrasında
join ayrıca yapılır. Mobil, keychain'den güncel token ile HTTP ve socket açar.

Paket 5'te uygulanacak kalıcı read yolu:

```text
GET /api/v1/game/:sessionId/result
```

Bu endpoint henüz mevcut değil. Terminal oturum socket upgrade'ında şu an
reddedildiği ve aktif-session GET terminal maçı döndürmediği için kayıp sonuç
event'i bu HTTP endpoint ile telafi edilecek. Sonuç henüz yoksa typed 404;
transient finalization sırasında UI bunu kısa aralıklı sınırlı retry ile ele alır.

HTTP error body'sindeki metin parse edilerek auth/forbidden/not-found ayrımı
yapılmaz. Network error modelinin HTTP status'u koruması planlanır. Mevcut tüm
HTTP error sözleşmesini değiştirmek gerekli değildir; error code eklenirse iki
taraf için burada açıkça tanımlanır.

### 5.2. Socket açılışı ve envelope

Paket 4'te uygulanacak v2 açılışı:

```text
GET /api/v1/game/<sessionId>/realtime?protocolVersion=2
Authorization: Bearer <JWT>
```

Query yalnız protokol uyumluluğu için; session ve game key server tarafından
doğrulanır. Mevcut gateway query negotiation yapmıyor; eklenecek. v1'in query'siz
açılışı v1 oturumlar için korunur. Desteklenmeyen/mismatched sürümde upgrade
öncesi açık hata döner. Native client için Origin zorunlu değildir.

URL `AppEnvironment.baseURL` üzerinden path korunarak türetilir; HTTPS → WSS,
HTTP → WS. `/api/v1` iki kere eklenmez. Token URL query'sine konmaz.

Client envelope:

```json
{
  "protocolVersion": 2,
  "messageId": "2a96a71c-8ed8-44b4-8ebc-048cabd28dba",
  "type": "houseRockets.steer",
  "payload": {
    "controlGeneration": "serverIssuedGeneration",
    "inputSequence": 42,
    "heading": 0.7853981633974483
  }
}
```

Envelope'da `actorId`, `playerId`, konum, skor veya kazanan kabul edilmez.
Session socket yolundan, actor JWT'den, connection ID gateway'den gelir.
`heading` finite radyan; `[-π, π]` aralığına normalize edilir. `inputSequence`
pozitif artan integer; server tarafından verilen control generation'a bağlıdır.

Server envelope: `protocolVersion`, `type`, `sentAt` ve tipine göre
`messageId`, `sequence`, `payload` veya `error`. `sentAt` UTC RFC3339;
decoder kesirli saniyeli/kesirsiz biçimi kabul eder. Eksik optional alanlar Swift
decode hatası yaratmaz. Bütün v2 alanları camelCase, süreler açık isimli
milisaniye/saniye değerleridir.

Başarılı v2 envelope'unda `payload` her zaman vardır; commandAccepted payload'ı
`{}`. Rejection yalnız `error` taşır, `payload` taşımaz; `error.args` her zaman
array (boşsa `[]`), `retryable` zorunlu bool. `messageId` korelasyon gereken
cevaplarda vardır, unsolicited snapshot'ta yoktur. `sequence` isteğe bağlıdır;
varsa integer, 0 değeri omission'a dönüşmez. Welcome runtime ayarlarını
fixture'daki açık milisaniye/Hz isimleriyle taşır.

Client `messageId` ve control generation boş/baş-son boşluklu olamaz ve en fazla
128 UTF-8 byte'tır; client JSON mesajı en fazla 16 KiB. `inputSequence` pozitif
Int64'tür; kesirli/taşan değer reddedilir. Join/leave/cancel/resync payload'ı
atlanabilir veya `{}` olabilir; `null` kabul edilmez. Ready'de açık `false`,
steer'de heading `0` geçerlidir; eksik/null alan yerine konmaz. Bilinmeyen alan,
case alias, duplicate key ve art arda iki JSON değer reddedilir. Decoder yalnız
şekil/sayı kontrolüdür; oyuncu yetkisi, rate limit ve sequence/generation
geçerliliği runtime/gateway'de ayrıca uygulanacak.

`gameSession.snapshot` payload'ı mevcut HTTP `GameSessionResponseModel`
biçimiyle aynı olacak. `sequence` yalnız session version'ıdır. Domain
struct'larının JSON çıktısı wire DTO olarak kullanılmaz.

### 5.3. Mesajlar ve tamamlanma anlamı

| Yön | Type | İşlev |
| --- | --- | --- |
| Client → server | `gameSession.join` | Oturuma katılma; roster server tarafından belirlenir. |
| Client → server | `gameSession.setReady` | `{ "ready": true/false }`; deadline'lardan sonra reddedilir. |
| Client → server | `gameSession.leave` | Açık çıkış; running'de runtime forfeiture ile tutarlılaştırılır. |
| Client → server | `gameSession.cancel` | Mevcut yetki/state kuralı; normal kullanıcı herkesi dilediği an iptal edemez. |
| Client → server | `houseRockets.steer` | Son yön niyeti; kalıcı lifecycle command'ı değildir. |
| Client → server | `houseRockets.resync` | Güncel tam oyun durumu ve gerekli control binding'i talebi. |
| Client → server | `realtime.ping` | Echo kimliğiyle clock/RTT ölçümü ve kontrol canlılığı; bounded rate. |
| Server → client | `realtime.welcome` | Session, bağlantı kimliği, seçilen sürüm ve desteklenen runtime ayarları. |
| Server → client | `realtime.pong` | Ping korelasyonu ve server zamanı. |
| Server → client | `gameSession.commandAccepted` | Gateway kabulü; application işlemi tamamlandı anlamına gelmez. |
| Server → client | `gameSession.snapshot` | Uygulanan lifecycle sonucunun version'lı durumu. |
| Server → client | `gameSession.commandRejected` | Korelasyonlu code/args/retryable hata. |
| Server → client | `houseRockets.controlGranted` | Oda sahibinin verdiği generation; ancak tek geçerli kontrol bağlantısına. |
| Server → client | `houseRockets.snapshot` | Tam, boyutu sınırlı game state. İlk sync, periyodik yayın ve resync aynı DTO. |
| Server → client | `houseRockets.result` | Kalıcılaştırılmış completed/cancelled sonucu; HTTP ile tekrar okunabilir. |

Yeni type'lar backend decoder, router ve mobile mapper'a birlikte eklenir.
`steer` için her girdi başına commandAccepted + Mongo receipt üretilmez;
`lastProcessedInputSequence` oyun snapshot'ında ACK işlevi görür. Validation
hatası için korelasyonlu rejection kullanılabilir; tekrarlayan flood bounded
limitten sonra bağlantıyı kapatır.

Lifecycle komutları aynı `messageId` ile aynı içerik/actor/session üzerinden
retry edilir. `player_already_joined` durumunda güncel roster doğrulanır;
reconnect sırasında her seferinde yeni join gönderilmez. Ready UI'sı yalnız
commandAccepted ile kesin hazır durumuna geçmez.

### 5.4. House Rockets snapshot alanları

| Alan | Tip / anlam |
| --- | --- |
| `sessionId`, `gameKey` | String; `gameKey = houseRockets`. Session maçın tek kimliğidir. |
| `runtimeEpoch` | Integer; owner generation/fencing ile ilişkili. Yeni epoch yalnız tam sync ile kabul edilir. |
| `stateSequence` | Integer; epoch içinde yayınlanan game state revision'ı. Kontrol/phase değişiminde physics tick ilerlemese de artar. |
| `tick` | Integer; tamamlanmış 1/120 s fizik adımı sayısı. Başlangıç 0. |
| `elapsedSeconds` | Number; otoriter oynanan simülasyon süresi, tick'ten üretilir. |
| `phase` | `countdown`, `playing`, `recovering`, `finalizing`, `ended`, `cancelled`. Lobby ayrı session DTO'dadır. |
| `courseVersion` | Integer; geometri ve dönüş kurallarının sürümü. |
| `cameraX`, `courseAngle` | Number; ortak dünya kamerası ve o tick'teki sunum açısı. |
| `players` | Maç başında dondurulan sıra; `playerId`, `displayName`, `color`, `worldX`, `worldY`, `courseHeading`, `isAlive`, `connected`, `distance`, `speedEffect`, `effectRemainingSeconds`, `eliminatedAtTick`, `eliminationReason`, `lastProcessedInputSequence`, `controlGeneration`. |
| `gates` | Sınırlı aktif parkur penceresi; string `id`, `worldX`, section `{ offsetX, lowerY, upperY }` listesi. |
| `speedFields` | Sınırlı aktif pencere; string `id`, `worldX`, `effect`, `phase`, `periodSeconds`. Enine konum server elapsed süresinden çizilir. |
| `countdownEndsAt` | Countdown için kesirli saniye destekleyen UTC deadline; local 3,2,1 döngüsü otorite değildir. |
| `winnerId` | Nullable string; yalnız kesin result ile kazanan gösterilir. |

Bu tablo JSON alan adlarının tam sözlüğüdür. Yeni oyun snapshot DTO'sunda
`winnerId`, `countdownEndsAt`, `speedEffect`, `eliminatedAtTick`,
`eliminationReason`, `controlGeneration` alanları yokken atlanmaz, açık `null`
olur. `players`, `gates`, `speedFields` ve gate `sections` her zaman array'dir;
boşsa adapter `[]` üretir, `null` değil. Countdown dışındaki phase'lerde
`countdownEndsAt: null`; canlı roketin elimination alanları `null` olur.
`speedEffect: null` olduğunda kalan etki süresi 0'dır. Alive/connected farklı
alanlardır; kopmuş roket grace boyunca hâlâ hayatta olabilir.

Player renkleri sabit roster sırasıyla `mint`, `coral`, `blue`, `gold`, `violet`,
`orange`, `pink`, `teal`. Son dört renk mobil enum/palete eklenecek; mobil kod
henüz yalnız ilk dördünü biliyor. Gerçek görsel renk mobile paletinden gelir.
`controlGeneration` sadece ilgili oyuncunun kontrol bağlantısına verilir;
ortak yayın gateway'de alıcıya projekte edilir, diğer oyuncularda/spectator'da
`null` olur. Actor + connection binding doğrulaması yine zorunludur.

Ortak `gameSession.snapshot` DTO'sundaki tarih alanları mevcut HTTP mapper'ın
kesirsiz UTC formatını ve `omitempty` davranışını korur. Yeni game snapshot /
result tarihleri RFC3339 kesirli saniyeyi destekler ve nullable alanları açık
`null` kullanır. Mobil iki DTO'nun optional kurallarını karıştırmamalı.

İlk sürümde tam snapshot tercih edilir: her mesaj oyuncu durumlarını ve yalnız
aktif geometri penceresini taşıdığı için Pub/Sub kaybından sonraki mesajla
toparlanabilir. Sonsuz parkurun tamamı gönderilmez. Delta/compression veya ayrı
geometri cache protokolü ancak ölçüm bunu gerektirirse eklenir; eksik delta
üzerinden yanlış engel çizme sorunu ilk entegrasyona taşınmaz.

Geometri ID'leri indeks bazlı stabil string olabilir: `gate:0`, `field:0`.
Kimlik scope'u `sessionId + courseVersion + id`; rematch/cache karışmaz.

### 5.5. Typed rejection sözlüğü

| Code | Anlam / retry |
| --- | --- |
| `houseRockets.error.invalid_input` | Eksik/geçersiz steer şekli veya finite olmayan heading; retryable false. |
| `houseRockets.error.control_unavailable` | Bu connection kontrol sahibi değil; aynı input'u retry etme, binding/sync bekle. |
| `houseRockets.error.stale_control` | Eski generation/connection; eski input buffer'ını temizle ve resync. |
| `houseRockets.error.player_eliminated` | Elenmiş oyuncu; spectator olarak kal, steer gönderme. |
| `houseRockets.error.not_running` | Şu phase'de steer uygulanamaz; authoritative state'i bekle. |
| `houseRockets.error.result_not_found` | Kalıcı sonuç henüz yok; HTTP 404 status'u korunur, finalizing'de bounded retry. |
| `realtime.error.invalid_command` | Geçersiz envelope/unsupported type; retryable false. |
| `realtime.error.unsupported_protocol` | v2 dışındaki sürüm; retryable false. |
| `realtime.error.rate_limited` | Rate limit; aynı hızda retry edilmez, tekrarda connection kapanabilir. |
| `realtime.error.unavailable` | Geçici coordination erişim sorunu; bounded backoff/resync. |
| `realtime.error.command_failed` | Beklenmeyen server hatası; bounded retry, internal detaylar gönderilmez. |

İlk ve envelope/protocol rejection'ları bağımsız decoder'da mevcut; kontrol,
phase, rate limit ve result hatalarının üretimi sonraki paketlerin işi. Mevcut
application lifecycle error code'ları da v2 rejection içinde taşınabilir.
HTTP error formatı `success/error` olarak korunur; mobile localized error
metninden bu code'ları türetmez, HTTP status üzerinden karar verir.

### 5.6. Kimlik, koordinat ve sıra uyumu

- Wire `playerId`, mevcut backend user ID string'idir; UUID'ye zorla parse
  edilmez. Swift oyun presentation ID'leri ortak string tipe geçirilir veya
  tipli wrapper kullanılır. Yerel bot kimlikleri yerelde üretilmiş UUID string'i
  olabilir. Node dictionary'leri de bu kimlikleri kullanır.
- Wire'da `.human` gönderilmez: her telefonda human olan kişi farklıdır.
  `localPlayerId` karşılaştırması local/remote rolünü presentation'da üretir.
- `nameKey` yalnız yerel bot/localization içindir. Gerçek ad `displayName`
  olarak kullanıcı verisinden gösterilir; localization key diye çevrilmez.
  Lobby için mevcut house member verisi kullanılabilir; maçın başında server
  onaylı görünüm bilgisi dondurulur. Bu bilgi steering yetkisi değildir.
- `sessionVersion`, game state'in `(runtimeEpoch, stateSequence)` sırası ve
  `inputSequence` üç ayrı sıralamadır. Physics `tick` yalnız simülasyon
  saatidir: aynı tick'te finalizing/ended/control geçişleri olabilir. Mevcut
  tek `revision` filtresi bütün akışları karşılaştırmak için kullanılamaz.
  Game mesaj envelope'undaki `sequence` kullanılırsa stateSequence ile aynı
  değer olur; lifecycle mesajlarındaki session version ile karşılaştırılmaz.
- Server snapshot heading'i parkur koordinatlarındadır. Joystick hedefi ekran
  yönündedir. Mobil yeni niyeti authoritative zamana göre
  `courseHeading = normalize(screenHeading - estimatedCourseAngle)` hesabıyla
  wire'a çevirir. Prediction aynı değeri kullanır. Server alınan yönü tekrar
  ekran açısıyla dönüştürmez; gecikme boyunca parkur dönerken çift dönüş olmaz.
- Server client timestamp'i ile hareket/efekt süresini geriye sarmaz. Girdi
  sıradaki fizik batch'inde uygulanır. Yeni yön gelmeyince courseHeading sabit
  kalır; parkur dönüşünde joystick yeniden dokunulunca yeni yön gönderilebilir.

## 6. Runtime, trafik ve bağlantı davranışı

### 6.1. Döngü ve girdi aktarımı

Başlangıç ölçüm hedefleri: 60 Hz runtime scheduling, her çalışmada iki adet
1/120 s fizik adımı; 20 Hz snapshot. Oyun tick'i 120 Hz fizik sayacıdır.
Client çizimi cihazın kare hızındadır. Bu sayılar production kapasite garantisi
değil, yük ve gecikme testlerinin başlangıç konfigürasyonudur.

Owner üzerindeki state'i tek oyun loop'u değiştirir. Lifecycle persistence,
Redis I/O veya socket writer bu loop'u bekletmez; sonucu bounded kanallarla
geri döner. Lease yenileme uzun süren DB command'ının arkasında kalmaz.
Kaçırılan scheduling süreleri bounded catch-up ile işlenir; bir gecikmeden sonra
sınırsız physics burst veya dev bir delta uygulanmaz. Kalıcı overload ölçülür;
gerekirse açık recovery/cancellation üretir.

Girdi gönderimi en fazla 20 Hz coalesced son niyettir. Yön değişmediyse sürekli
paket gönderilmez. ACK görülmeyen son niyet aynı sequence ile bounded aralıkla
yeniden gönderilebilir; owner aynı sequence'i iki kez uygulamaz. App lifecycle
ve kontrol canlılığı ayrı ping ile izlenir. Online ViewModel her dokunuş için
bir öncekini bekleyen sınırsız Task zinciri oluşturmaz.

Yerel owner'a girdi memory üzerinden ulaşır; uzak owner'a ayrı kısa ömürlü
Pub/Sub yolu önerilir. Gateway aynı oda için girdileri bounded batch'leyebilir.
Envelope actor/connection bilgisini server ekler. Owner, control generation,
source binding, epoch ve artan input sequence'i doğrular. Input subscription
hazır olmadan controlGranted verilmez. Owner değişiminde resync/control rebind
yapılır; eski girdi yeni maç veya yeni control generation'a taşınmaz.

Bu girdi yolu anlık veri kaybedebilir; retransmit/latest-state yaklaşımı kaybı
telafi eder. Ready/leave gibi kalıcı business niyetleri mevcut Streams yolunda
kalır. Snapshot dağıtımı oda başına/instance başına mevcut hub'ı kullanır.

Control ve gameplay rate limit'leri ayrılır. Başlangıç önerisi steering için
30 mesaj/s üst sınır, normal gönderim 20 Hz; resync/ping ayrıca bounded.
Client bu değerleri welcome ayarlarından okuyabilir. Gateway'de sırf tek
maximumMessages değerini büyütmek yeterli değildir.

Outbound akış iki davranış içerir: eski game snapshot'ını daha yenisiyle
değiştirebilen latest-state buffer; command rejection/result gibi önemli
mesajlar için bounded kuyruk. Hiçbir slow client diğer oyuncuları veya physics
döngüsünü bekletmez. Terminal state/result kalıcı HTTP okumasıyla telafi edilir.

### 6.2. Mobil render ve prediction

- Online sahne `applySnapshot`/render state üzerinden güncellenir. Demo sahne
  modu yerel simülasyonu çalıştırır. Online renderer authoritative kamera,
  elenme ve sonucu değiştirmez.
- Diğer oyuncular küçük bir interpolation buffer'ından çizilir; başlangıç
  hedefi 100 ms, ölçümle ayarlanır. Ortak kamera, parkur açısı ve field salınımı
  aynı render zamanını kullanır; HUD ile sahne farklı saat kullanmaz.
- Yerel roket yeni yöne hemen tepki verir. Server'ın ACK'lediği input'lar
  buffer'dan çıkarılır; kalan girdiler authoritative konum üstünden tekrar
  uygulanır ve küçük farklar yumuşatılır. Prediction görünüm kolaylığıdır.
- Extrapolation bounded'dır; örneğin 150 ms'yi aşan veri boşluğunda uzak
  oyuncuyu sonsuza dek uçurmak yerine bağlantı/resync görünümü gösterilir.
- Epoch değişiminde buffer ve eski tahminler temizlenir; tam snapshot'a
  oturulur. Eski epoch/maç/connection mesajları bırakılır.
- `lastEliminatedId` tek alanı eşzamanlı çoklu elenmeyi kaybedebilir. Önceki ve
  yeni roster farkından bütün elenmeler çıkarılır; aynı tick/generation için
  notice/haptic tekilleştirilir. Reconnect geçmiş elenmeleri tekrar titretmez.
- Accessibility, reduceMotion, landscape projection, ortak artwork ve yerel
  joystick davranışı korunur. Kontrol yalnız localPlayer alive, playing,
  active, synced ve geçerli control generation varsa açık olur.

### 6.3. Lobi, foreground ve çıkış

Online akış: ev/user context doğrula → aktif session bul/oluştur → socket aç →
ilk session snapshot → gerekiyorsa join → landscape hazırla → ready → server
countdown → tam game snapshot + control grant → playing → spectating/result.

`snapshot != nil` bugün View'da oyun sahnesi göstermek için kullanılıyor.
Online'da session lobby snapshot'ı bulunması yarışın başladığı anlamına gelmez;
UI mod/session phase/connection state'i ayrı taşır. Başlangıç yarışmacıları
countdown kilidinde server tarafından dondurulur. Orientation başarısızsa ready
gönderilmez; failure daha sonra oluşursa typed leave/cancel politikası uygulanır.

Bağlantı state'i önerisi: `idle`, `connecting`, `connected`, `reconnecting`,
`syncing`, `failed`. Bunlar oyun phase'i değildir. Foreground sonrası eski
snapshot'tan kontrol açılmaz; önce tam sync alınır.

Mevcut socket timeout 45 s, presence TTL 30 s; bunlar önerilen 10 s grace'i
tek başına sağlayamaz. Online control heartbeat için başlangıç önerisi 2 s
ping, 6 s canlılık deadline'ı ve sonrasında 10 s reconnect grace. Owner gerçek
kontrol bağlantısının heartbeat'ini izler. Bu süreler ve server scheduling
payı fixture/testlerde açık olacak. Background'da heartbeat'in durması maçın
pause edilmesine yol açmaz. Hayatta kalan oyuncu grace boyunca hâlâ elenebilir.

Lobide bağlantı süresi dolan ready oyuncusu server system geçişiyle ready'den
çıkarılır; eksik oyuncuyla countdown başlamaz. Running'de açık leave veya
grace bitimi forfeiture üretir. Ortak session leave ve oyun roster'ı ayrı
kalıcı/geçici state olduğu için idempotent reconciler ikisini tutarlılaştırır;
yeniden teslimde ikinci eleme veya çifte sonuç olmaz. Yetkiyi kaybeden house
üyesinin kontrolü de iptal edilir; reconnect/start/critical commands üyeliği
yeniden doğrular, üyelik kaldırma akışı aktif kontrolü geçersizleştirir.

`disconnect()` transport cleanup'tır; otomatik olarak business leave değildir.
Kullanıcının açık Exit eylemi ayrı leave niyetidir. `onDisappear`, logout ve
ev değişiminde cleanup idempotent olur; kontrol niyeti gönderilmiş olsa bile
socket closure başarı kanıtı sayılmaz. Bağlantısız çıkış grace ile sonuçlanır.

JWT geçersiz/401 ise sonsuz reconnect döngüsü yapılmaz; mevcut auth akışına
dönülür. Mevcut projede refresh-token akışı varmış gibi tasarım yapılmaz.
403, terminal session ve unsupported protocol retry edilmez. Geçici network
ve unavailable hatalarında jitter'lı bounded backoff önerisi 0,5/1/2/4/8 s;
grace ve kullanıcı çıkışı retry bütçesini sınırlar.

## 7. Sonuç, checkpoint ve sahiplik güvenliği

Kalıcı sonuç sözlüğü: `sessionId`, `houseId`, `gameKey`,
`protocolVersion`, `courseVersion`, `status` (`completed`/`cancelled`),
`endReason`, `winnerId`, `startedAt`, `endedAt`, `durationSeconds`, `players`.
Player sonucu: `playerId`, `rank`, `eliminatedAtTick`, `eliminationReason`,
`distance`. Online distance dünya ilerlemesidir; puan/mesafe bazlı kazanan
kuralı otomatik eklenmez. Distance `max(0, worldX - spawnX)` olarak son durumdan
üretilir; geriye uçuş mesafeyi azaltabilir, sıra belirlemez.

Paket 1 sonuç politikası:

- Bir tam fizik tick'indeki bütün roketler işlenir. Tek survivor varsa completed
  / `lastSurvivor`, winner o oyuncudur. Hiç survivor kalmazsa completed /
  `simultaneousElimination`, `winnerId: null`; mesafeyle beraberlik bozulmaz.
- Survivor rank 1'dir. Elenenin rank'i `1 + kendisinden daha geç elenen veya
  hayatta kalan kişi sayısı`; aynı tick eşit rank, sonraki sıra atlanır
  (örnek: 1, 2, 2, 4). Final tick'te herkes elenirse son grubun tamamı rank 1.
  Result players sırası frozen roster sırasıdır, UI isterse rank'e göre gösterir.
- Süre sınırının son tick'inde önce normal survivor/draw çözümü yapılır;
  devam eden yarış 300 s'de cancelled / `sessionExpired` olur. Kazanan ve bütün
  rank'ler `null`. Teknik/user cancellation da rank/kazanan üretmez.
- Başlamadan iptal: `startedAt: null`, `durationSeconds: 0`. Oynanmış maçta
  startedAt UTC tarih, durationSeconds simülasyon süresidir; recovery yüzünden
  wall-clock endedAt-startedAt farkıyla aynı olmak zorunda değildir.
- Nullable rank, winnerId, startedAt, eliminatedAtTick, eliminationReason açık
  `null`; players array. Elenme reason'ları `behindCamera`, `forfeit`,
  `connectionExpired`, `membershipRevoked`. End reason'lar `lastSurvivor`,
  `simultaneousElimination`, `insufficientPlayers`, `cancelledByUser`,
  `sessionExpired`, `recoveryFailed`, `coordinationUnavailable`, `runtimeOverloaded`.

Completed, beraberlik, 5 dakika expiry ve başlamadan iptal fixture'ları mevcut.
Result persistence/HTTP endpoint henüz uygulanmadı; bu bölüm sözleşme sınırıdır.

Result unique `sessionId` ile tekilleştirilir. Game result, ortak session finish,
aktif-session pointer temizliği, command receipt ve ilgili outbox event'leri
aynı Mongo transaction sınırında tutulur. Client finalizing sırasında sonucu
bekler; commit olmadan kesin kazanma ekranı yayınlanmaz. Commit sonrası publish
hatasında retry veya HTTP result aynı kalıcı sonucu döndürür.

Room lease yayın koruması, Mongo yazım korumasının yerine geçmez. Kalıcı owner
generation kaydı ve CAS bariyeri tasarlanır: yeni owner takeover sırasında
generation'ı kaydeder; checkpoint/result işlemleri kendi generation'ını taşır;
eski owner yeni generation kaydedildikten sonra transaction tamamlayamaz.
Redis lease okuması ile Mongo write arasında atomik transaction olduğu iddia
edilmez. Takeover/finish yarışı için doğrulanmış karar ve test gerekir.
Generation'ı yalnız transaction içinde okumak yeterli sayılmaz; completion
aynı ownership kaydına conditional write/CAS yaparak takeover ile write
conflict oluşturmalıdır. Redis token sayacının resetlenmesi durumu da bu
kalıcı bariyerle doğrulanır; eskiden geçerli token yeniden yetki kazanamaz.

Paket 3 için seçilen sınır: Session başına unique ownership kaydı; takeover
Mongo'da önceki generation'a CAS yapıp generation'ı atomik artırır. Yeni
`runtimeEpoch` bu kalıcı generation'dır; Redis FencingToken doğrudan epoch
sayılmaz. Kayda ownerInstanceId ve mevcut leaseId/proof da bağlanır. Bu kayıt
başarılı olmadan yeni owner control grant/state yayını yapmaz. Completion aynı
generation ve ownership kaydına conditional mutation yapar; Mongo takeover
ile conflict/retry sonunda yalnız geçerli generation commit edebilir.
Checkpoint script'i Redis lease identity/fence ile epoch/stateSequence'i
birlikte doğrular. Redis counter reset'i tek başına eski Mongo generation'a
yetki vermez. Bunlar tasarım kararlarıdır; adapter/CAS implementasyonu ve
takeover testleri Paket 3/5/6 teslimlerinde yapılacak.

Geçici Redis checkpoint başlangıç önerisi 1 s aralık ve kritik elenme/forfeit
geçişlerinde, valid lease'i aynı script içinde doğrulayarak yazmaktır.
Script ayrıca checkpoint epoch/stateSequence sırasını doğrular; geç biten eski
periyodik write daha yeni kritik checkpoint'in üzerine yazamaz.
Checkpoint: bütün roket state'leri, input ACK/generation'ları, tick, kamera,
aktif geometri ve next index, hız efekti süreleri/temas geçmişi, sonuçlandırma
niyeti ve schema/course version. Scene çizim state'i checkpoint değildir.

Elenme duyurulmadan önce kritik checkpoint korunur; owner değişiminde daha eski
checkpoint ile oyuncu diriltilmez. Checkpoint yazılamayan kritik geçişte runtime
açık recovering durumuna girer veya güvenli iptal üretir. Redis tamamen veri
kaybederse aktif maçın her hareketini kurtarma garantisi verilmez; kalıcı
session/result okunur, sonuç yoksa maç tutarlı biçimde iptal edilir.

Failover driver mevcut socket'e veya yeni kullanıcı komutuna bağımlı kalmaz.
Lease kaybını gözleyen instance kontrollü takeover/resync başlatır. Valid
checkpoint ile yeniden kurulumda runtimeEpoch artar; eski mesajlar reddedilir.
Son connection da kaybolmuşsa orphan running oturumlar bounded aralıklarla,
indeksli active-session taramasıyla uzlaştırılır; aktif pointer sonsuza kadar
kalmaz. Her instance'ın sürekli bütün session koleksiyonunu taraması gerekmez.
Recovery sırasında simülasyon süresi durmuş kalabilir; wall-clock interruption
kullanıcının haberleşemediği süre kadar onu hareket ettirip elemez. Disconnect
grace/heartbeat'leri yeni owner'ın monotonic saatine kalan süreyle taşınır.
Recovery bounded sürede tamamlanamazsa cancelled sonuç kaydedilir.

Bir saniyelik checkpoint aralığı küçük hareket geri düzeltmesine izin verir.
Bu görünür davranış ve mevcut lease bekleme süresi kabul senaryolarında
belirtilir; kesintisiz failover diye sunulmaz. Checkpoint TTL'i önerilen azami
maç/recovery süresini kapsar; bitmiş odalar temizlenir. Kalıcı sonucu Redis'te
tutmak veya her fizik tick'ini Mongo'ya yazmak gerekli değildir.

## 8. Backend geliştirme paketleri ve mobil başlangıç noktaları

Bu bölüm 3 Ekim 2026'da backend teslimlerine göre yeniden düzenlendi. Önceki
Faz 0–6 sıralaması artık Paket 1–7 olarak adlandırılır; paralel mobil işler
aşağıdaki başlangıç tablosunda ayrıca gösterilir. Diğer bölümler bu paketlere
referans verir. Paket 1 backend sözleşmesi doğrulandı; diğer backend paketleri
ve mobil teslimler henüz doğrulanmadı.

**Sıradaki backend işi Paket 2 — Sunucu otoriteli domain ve simülasyon.**
Mevcut session/coordination altyapısı kullanılacak; socket endpoint'ini veya
Mongo repository temelini baştan kurmak gerekmiyor.

Bağımlılık sırası:

```text
Paket 1 → Paket 2 → Paket 3 → Paket 4 → Paket 5 → Paket 6 → Paket 7
Sözleşme   Motor     Runtime   Gateway   Sonuç     Recovery   Yayın kabulü
```

Her paket kendi testlerini içerir. Testler sona bırakılmaz; Paket 7 önceki
paketlerde doğrulanan parçaların birleşik kapasite/gerçek cihaz kabulüdür.

### Paket 1 — House Rockets oyun ve protokol sözleşmesi

Amaç: Backend ve mobilin aynı kimlikleri, dünya kurallarını ve mesajları
uygulayacağı kesin referansı üretmek.

Backend teslimleri:

- Oyun state ve wire DTO sözlüğü; v2 upgrade/sürüm seçimi, HTTP/session/result
  sözleşmeleri, mesaj tipleri ve typed error listesi.
- String player ID, course heading, session version, runtime epoch,
  stateSequence/tick ve inputSequence anlamlarının kesinleştirilmesi.
- Player limit, ready/countdown, disconnect/grace, kontrol devri, maç expiry,
  beraberlik/sıralama ve rematch kurallarının kesinleştirilmesi. Belgedeki
  öneriler kullanıcı kararı yerine geçmez; materially farklı ürün kuralı
  geliştirmeden önce netleştirilir.
- Runtime sahiplik generation'ı, checkpoint/sonuç transaction sınırı ve
  takeover yarışı için uygulanacak korumanın tasarımı.
- Ortak protokol/physics fixture'ları:
  `internal/application/game/gameSpesific/houseRockets/fixtures/houseRocketsProtocol.json` ve
  `internal/application/game/gameSpesific/houseRockets/fixtures/houseRocketsSimulation.json`. İkisi de oluşturuldu;
  fixture schema `1`, contract revision `houseRockets.v2.1`.
- DTO JSON encode/decode ve sınır değerleri için contract testleri.

Çıkış ölçütü: Join, ready, countdown, steer, resync, result ve rejection örnekleri
belirsiz alan/birim içermiyor; Go DTO'ları fixture'larla eşleşiyor. Mobil bu
fixture'ları tüketebilir; oyun motorunun veya canlı socket'in bitmesi gerekmez.

Mobil eşzamanlı iş: Mod seçimi/yerel bot akışı ve renderer ayrımı hemen
hazırlanabilir. Bu paket tamamlanınca gerçek online DTO, transport ve lobby
geliştirmesi mock mesajlarla başlayabilir.

### Paket 2 — Sunucu otoriteli House Rockets domain ve simülasyonu

Amaç: Ağdan bağımsız, server'ın hareketi ve sonucu belirlediği oyun motoru.

Backend teslimleri:

- House Rockets player/world state, stabil engel/field ID'leri ve mevcut
  parkur profile'larının Go implementasyonu.
- Sabit fizik adımı, tam yön kontrolü, yüzey temas/kayma algoritması, boost/slow,
  lider kamera takibi ve fiziksel ekrandan bağımsız arka-sınır elenmesi.
- Ortak parkur dönüş zamanı, eşzamanlı elenme, kazanan/beraberlik hesabı.
- Authoritative snapshot ve restore edilebilir oyun state'i üretimi.
- Enjekte edilen başlangıç/zaman; golden fixture, geometri, NaN/infinite,
  frame-rate bağımsızlığı ve elenme sınırı testleri.

Çıkış ölçütü: Socket/Redis olmadan girdilerle maç simüle edilebilir; Swift
referansıyla Float64 toleransı içinde uyumludur. Ortak GameSession domain'ine
roket fiziği eklenmez; production katalog kaydı henüz açılmaz.

Mobil eşzamanlı iş: Demo regresyonları, string ID/isim mapping, fixture üzerinden
online renderer ve interpolation buffer. Prediction aynı yön/physics
sözleşmesiyle geliştirilir; client sonucunu otorite yapmaz.

### Paket 3 — Oyun runtime'ı ve çoklu instance girdi koordinasyonu

Amaç: Paketteki motoru tek oda sahibinde çalıştırmak ve her instance'taki
oyuncunun girdisini doğru sahibine ulaştırmak.

Backend teslimleri:

- Küçük game runtime factory, room lifecycle'ına start/stop bağlama ve
  60 Hz scheduling / 120 Hz physics başlangıç konfigürasyonu.
- Tek yazarlı state loop'u, bounded catch-up, bağımsız lease yenileme;
  Redis/DB/socket I/O'nun simülasyonu bloklamaması.
- Kalıcı owner generation/CAS bariyerinin temeli ve runtimeEpoch üretimi;
  Paket 5/6 bu korumayı result/checkpoint için kullanacak.
- Tek oyuncu/tek controller binding, control generation, input sequence,
  ACK, coalescing ve eski/yetkisiz girdinin reddi.
- Local owner memory yolu ve uzak owner için kısa ömürlü input aktarımı;
  subscriber hazırlığı, snapshot yayını ve bounded queue davranışı.
- İki gerçek backend runtime/coordinator instance'ıyla route, duplicate,
  stale input, lease kaybı ve scheduling testleri.

Çıkış ölçütü: Farklı instance'lardan gelen girdiler tek authoritative motoru
değiştiriyor; bir oda iki owner tarafından yönetilemiyor; yavaş I/O physics ve
diğer oyuncuları bekletmiyor. Bu kapı test client'larıyla doğrulanabilir.

Mobil eşzamanlı iş: Fixture/mock transport üzerinden input ACK/coalescing,
epoch/stateSequence filtresi ve prediction reconciliation. Canlı endpoint'i
bu paket bitince hazır kabul etmeyin; gateway teslimi Paket 4'tedir.

### Paket 4 — WebSocket gateway, katalog ve online lobby entegrasyonu

Amaç: Mobilin canlı backend'e bağlanıp aynı maçta hareket görebileceği ilk
uçtan uca test ortamını sağlamak.

Backend teslimleri:

- v2 negotiation/decoder/router, camelCase session payload, welcome,
  ping/pong, control grant, game snapshot ve resync mesajları.
- House Rockets katalog kaydı ve mevcut aktif-session HTTP yollarına bağlama;
  ilk açılış local/staging içindir. Production açılışı Paket 7 kabulüne bağlı.
- Join/ready/landscape sonrasında server countdown, sabit başlangıç roster'ı,
  runtime start ve running leave/üyelik etkisinin uzlaştırılması.
- Auth/session access, ayrı gameplay/lifecycle rate limit'leri, initial
  snapshot-event yarışının ele alınması, snapshot coalescing ve slow consumer.
- İki instance'a dağılmış en az iki hesap/socket ile gerçek transport testleri.

Çıkış ölçütü: Session oluşturma, katılma, hazır olma, ortak başlangıç, yönlendirme,
karşı oyuncunun konumu ve authoritative elenme gerçek socket'lerde çalışır.
Online'da bot üretilmez. Maçın kalıcı tamamlanması henüz Paket 5'e bağlıdır;
bu ara teslim production-ready veya tamamlanmış multiplayer sayılmaz.

Mobil teslim kapısı: Bu paketin test ortamı hazır olunca gerçek HTTP/socket
entegrasyonu ve iki cihazlı hareket/kontrol denemesi başlar. UI/DTO/transport
implementasyonu daha önce mock fixture'larla hazırlanmış olabilir.

### Paket 5 — Kalıcı maç sonucu, oturum tamamlama ve rematch

Amaç: Başlayan maçın güvenilir şekilde bitmesi ve yeni maçın açılabilmesi.

Backend teslimleri:

- Result repository, unique session ID/index ve gerekli Mongo validator/index
  migration'ları; application interface ve data/database adapter sınırı.
- Runtime/system actor ile finish/cancel command'ları; result + session
  terminal state + active pointer temizliği + receipt/outbox transaction'ı.
- Paket 3 owner generation bariyerinin completion transaction'ında kontrolü;
  duplicate finish, takeover/finish ve Mongo hata senaryoları.
- Commit sonrası result event ve yetkili result HTTP read endpoint'i.
- Finalizing davranışı, publish kaybında HTTP fallback; eski match'i resetlemek
  yerine yeni/aktif lobby ile rematch.

Çıkış ölçütü: Maç bir kez sonuçlanır; cihazlar aynı kalıcı sonucu okur;
bitmiş session aktif pointer'da kalmaz. Rematch yeni ready/countdown akışından
geçer. Commit/publish yarışı çifte kazanan üretmez.

Mobil teslim kapısı: Sonuç/finalizing/iptal ekranı ve rematch canlı backend'le
birlikte tamamlanır. Paket 1–5 tamamlanınca kontrollü ortamda baştan sona
multiplayer maçı oynanabilir; bağlantı/owner kaybı ve yayın kapıları hâlâ bekler.

### Paket 6 — Reconnect, owner recovery ve maç lifecycle dayanıklılığı

Amaç: Mobil bağlantı kopması veya instance kapanması maçın yanlış/takılı
kalmasına yol açmasın.

Backend teslimleri:

- Fencing ve monotonic state sırası korumalı periyodik/kritik checkpoint;
  input binding, efekt/temas geçmişi, kamera/tick ve restore bilgileri.
- Takeover/recovery driver, epoch artışı ve geçerli checkpoint'ten resync;
  elenmiş oyuncuyu diriltmeyen kritik geçiş koruması.
- Heartbeat, grace, controller devri, açık leave/forfeit, kopan ready oyuncusu
  ve üyelik kaybında kontrolün kaldırılması.
- Stale/orphan room ve onaylı session expiry cleanup; Redis/Mongo kesintisinde
  bounded recovery/finalization veya tutarlı cancellation.
- Owner process kill, Redis kesintisi, reconnect, grace timeout, eski owner'ın
  write denemesi ve aynı anda exit/finish için entegrasyon testleri.

Çıkış ölçütü: Oyuncu çoğalmaz/dirilmez, eski owner yazamaz, bitmiş maç tekrar
başlamaz. Toparlanma sınırı ve geri düzeltme görünürdür; başarısız recovery
kalıcı ve ortak cancelled sonuç üretir.

Mobil teslim kapısı: Foreground tam sync, reconnect backoff, control generation
yenileme, recovery ekranı ve logout/ev değişimi cleanup canlı senaryolarda
doğrulanır. Bu davranışlar daha önce mock state'lerle kodlanabilir.

### Paket 7 — Yük, gecikme ve production kabulü

Amaç: Özelliği gerçek çok kullanıcılı trafik için ölçülmüş sınırlarla açmak.

Backend teslimleri:

- Temsili 2/4/8 oyunculu çoklu oda testleri ve belirlenecek hedef concurrent room
  sayısı için CPU/memory, physics lag, input latency ve network ölçümleri.
- Redis operasyonu ve payload bandwidth bütçesi; referans instance/region
  bilgisi; admission limiti ve gerekli konfigürasyon ayarı.
- Bounded catch-up/backpressure, migration uyumluluğu, regression ve graceful
  shutdown doğrulaması; yapılandırılmış realtime hata/kapasite ölçümleri.

20 Hz yayın oda başına saatte 72.000 snapshot publish demektir; input,
checkpoint, lease ve script içindeki Redis işlemleri buna eklenir. Bu sayı
fiyat veya sağlayıcının billing command sayısı değildir. Local-owner kısayolu,
batch ve yayın sıklığı ölçüme dahil edilir; desteklenen oda sayısı tahmin edilmez.

Ortak kabul: İki gerçek cihaz/hesap, farklı network koşulları, 100/200/400 ms
RTT, jitter ve kısa kesintiler; 30/60/120 FPS, iPhone/iPad ve orientation
geçişleri. Yerel bot modu regresyonları da geçer. Backend kapasite hedefi ve
mobil kontrol hissi birlikte doğrulanınca production katalog/online mod açılır.

### 8.8. Mobil agent ne zaman başlamalı?

Mobil agent bütün backend'i beklememeli. Başlangıç ve canlı test kapıları:

| Mobil iş grubu | Başlayabileceği zaman | Canlı doğrulama bağımlılığı |
| --- | --- | --- |
| İki mod seçimi, yerel bot modu regresyonları, scene/simulation ayrımı, dependency factory | Şimdi; mevcut mobil kod ve onaylı mod sınırı yeterli | Backend gerektirmez. |
| Online wire DTO, string ID/isim mapping, HTTP/socket transport, lobby state, mock server | Paket 1 sözleşmesi/fixture'ları sabitlenince | Paket 4. |
| Input coalescing, ACK/epoch filtreleri, interpolation ve prediction/reconciliation | Paket 1 sonrasında fixture üzerinden; Paket 2 golden çıktılarıyla uyum kontrolü | Paket 3 runtime ve Paket 4 gateway. |
| Gerçek ev üyeleriyle join/ready/countdown/oynanış entegrasyonu | Paket 4 test ortamı hazır olunca | Paket 4 ve mobilde önceki iş grupları. |
| Sonuç ekranı, HTTP result fallback, rematch | Paket 1 result fixture'ıyla mock geliştirme | Paket 5. |
| Reconnect/background/recovery/logout/ev değişimi | Paket 1 politikalarıyla mock geliştirme | Paket 6. |
| İki cihazlı nihai kabul ve gecikme hissi | İlk canlı kontrol Paket 4; tam maç Paket 5; hata testleri Paket 6 | Yayın kararı Paket 7. |

Her backend tesliminde agent aynı dokümana package status, test özeti,
contract/fixture revision ve doğrulanabilen yolları işler. Local/staging bağlantı
adresleri environment ayarı olarak paylaşılır; token/secret bu dosyaya yazılmaz.
Mobil agent hazır olmayan mesajı/backend davranışını mock olarak etiketler;
mock sonucu gerçek entegrasyon başarısı diye raporlamaz.

### 8.9. Backend'in ne zaman tamamlanmış sayılacağı

- **Paket 1 sonrası:** Mobil online geliştirmesi için sabit sözleşme var.
- **Paket 4 sonrası:** Gerçek socket ve cihazlarla aynı maçta hareket testi var.
- **Paket 5 sonrası:** Kontrollü ortamda lobi → oynanış → kalıcı sonuç → rematch
  akışı tamamlanabilir.
- **Paket 6 sonrası:** Reconnect/instance kaybı/iptal senaryoları tutarlı.
- **Paket 7 sonrası:** Multiplayer production kabulüne hazır.

House Rockets'ı düzgün ve çoklu instance'a uygun multiplayer olarak yayınlamak
için Paket 1–7'nin tamamı gerekir. Paket 6/7 ileride isteğe bağlı iyileştirme
değil, ilk yayın kapsamının parçasıdır. Yerel bot modunun geliştirilmesi ve
korunması backend paketlerinin tamamlanmasını beklemez.

## 9. Birlikte doğrulanacak senaryolar

| Senaryo | Beklenen sonuç |
| --- | --- |
| Ağ kapalı, bot modu | 1 insan + 1–3 bot oynanır; HTTP/socket oluşturulmaz. |
| Online'da yalnız bir hazır oyuncu | Maç başlamaz; bot eklenmez. |
| Aynı house/game için eşzamanlı PUT | Tek aktif session; üyeler aynı session ID'yi alır. |
| Ready commandAccepted, sonra rejection | UI yanlış biçimde kesin hazır göstermez. |
| Landscape başarısızlığı | Hazır onayı verilmez; yerel otomatik maç başlamaz. |
| Aynı kullanıcı, iki cihaz | Tek controller; eski/ikinci connection input'ları oyunu yönetemez. |
| Yön bırakma/dönüş sırasında yeni dokunma | Son parkur yönü korunur; yeni ekran yönü doğru dönüştürülür. |
| Çok sayıda steer / kayıp / tekrar | Bounded latest input; başka oyuncu veya session etkilenmez. |
| NaN/infinite/geçersiz payload | World state korunur; typed rejection/limit davranışı. |
| Eşzamanlı elenme | Bütün oyuncuların aynı fizik adımı işlenir; tek, ortak sonuç. |
| Kısa network kopması | Server devam eder; tekrar bağlanan roket çoğalmaz, tam sync alır. |
| Grace bitimi/açık Exit | Bir kez forfeit; kalan oyuncu/sonuç tutarlı. |
| Elendikten sonra reconnect | Spectator görünümü; tekrar steering yok. |
| Snapshot/rejection/result aynı anda | Ayrı sıralamalar, doğru korelasyon; sonuç slow-state kuyruğunda kaybolmaz. |
| Owner process kaybı | Epoch değişir; valid checkpoint veya açık iptal; eski owner'ın yazısı yok. |
| Result commit sonrası publish kaybı | HTTP read aynı sonucu getirir; duplicate sonuç yok. |
| Logout/ev değişimi/rematch | Eski task/message yeni user/house/session'a uygulanmaz. |
| Bilinmeyen v2 / eski v1 istemci | Uyumluluk açıkça doğrulanır; sessiz yanlış decode yok. |
| Çok uzun/takılmış maç | Onaylı session expiry/cleanup; kaynaklar sonsuza kadar tutulmaz. |

Backend testleri mevcut `tests/gameSessionDomain_test.go`,
`tests/realtimeRoomRuntimeIntegration_test.go`,
`tests/realtimeGatewayIntegration_test.go` ve persistence/catalog testleri
üzerine genişletilir. Yeni Go test dosyaları `houseRockets..._test.go` gibi
camelCase gövde ve Go'nun zorunlu `_test.go` suffix'ini kullanır.

Mobilde mevcut HouseRocketsTests korunur; DTO fixtures, mock transport,
connection/prediction/lifecycle testleri ayrı anlamlı senaryolarla eklenir.
Go ve Swift fizik fixture karşılaştırmaları Float64 toleransı kullanır;
platformlar arasında bit-bit aynı floating-point sonuç varsayılmaz. Kritik
elenme sınırları tolerans ve server authority ile ayrıca doğrulanır.

## 10. Agent'lar için çalışma ve teslim kuralları

- Önce bu belgenin **mevcut durum**, **sabit sözleşme** ve **paket kapıları**
  ayrımını okuyun. DTO/fixture bulunması canlı API'nin hazır olduğunu göstermez.
- Her paket için durum `planlandı`, `geliştiriliyor`, `doğrulandı` olarak bu
  dosyada güncellenebilir; test edilmemiş paket tamamlandı sayılmaz.
- Backend ve mobil ilerlemeleri ayrı raporlayın; ikisi doğrulanmadan ortak
  teslim kapısını geçmiş saymayın. Mock test canlı iki cihaz testi yerine geçmez.
- `external/doc` altında yalnız Markdown (`.md`) dokümanları tutulsun;
  başka dizinlere yeni dokümantasyon dağıtılmasın. Oyuna özgü tanımlar,
  modeller, kurallar ve JSON contract/physics fixture'ları
  `internal/application/game/gameSpesific/<gameName>` altında tutulsun.
  House Rockets fixture'ları bu oyun dizininin `fixtures` alt dizinindedir.
- Yeni dosya/değişken gövdelerinde snake_case kullanılmasın; mevcut Go
  migration dosyaları yeniden adlandırılmasın. Dilin zorunlu Go `_test.go`
  suffix'i korunur; tip/exported semboller dilin mevcut kurallarını izler.
- Backend application use-case'lerinde mevcut CQRS/typed error kuralları
  izlenir; altyapı işleri sırf her şeyi handler yapmak için DB command'ına
  çevrilmez. Yeni soyutlamalar gerçek ikinci kullanım veya test sınırıyla
  gerekçelendirilir.
- Yerel bot modu ve diğer mevcut oyunlar regresyon kontrolünde korunur;
  online auth/yetki kontrolleri demo kimliklerine güvenmez.
- Test için açılan local process/container'lar teslimde kapatılır;
  kullanıcının önceden çalışan ilgisiz servisleri kapatılmaz.
- Commit yalnız kullanıcının verdiği commit mesajı ve yetkiyle yapılır.

İlerleme durumu (3 Ekim 2026):

| Backend paketi | Backend | İlgili mobil doğrulama | Ortak kapı |
| --- | --- | --- | --- |
| 1 — Sözleşme | Doğrulandı (Go contract) | Planlandı; mock/DTO başlayabilir | Mobil kabul bekleniyor |
| 2 — Oyun motoru | Planlandı | Planlandı | Geçilmedi |
| 3 — Runtime / coordination | Planlandı | Planlandı | Geçilmedi |
| 4 — Gateway / online lobi | Planlandı | Planlandı | Geçilmedi |
| 5 — Sonuç / rematch | Planlandı | Planlandı | Geçilmedi |
| 6 — Dayanıklılık | Planlandı | Planlandı | Geçilmedi |
| 7 — Yayın kabulü | Planlandı | Planlandı | Geçilmedi |

Paket 1 doğrulama: `go test ./...` ve
`go test -race ./tests -run '^TestHouseRockets' -count=1` başarılı.
V2 decoder ayrıca 5 s fuzz testinde 46.272 girdiyle beklenmeyen başarısızlık/panic üretmeden
doğrulandı (`-parallel=2`). Bu kapasite/yük testi değildir.
Mongo/Redis environment'ı olmayan integration testleri kendi kurallarıyla
skip edildi; bu teslimde canlı Mongo/Redis/socket veya Docker compose testi
yapılmadı. Swift'in mevcut üç Foundation/CoreGraphics kaynak dosyası bağımsız
bir referans executable ile çalıştırıldı; iOS uygulaması build edilmedi ve
mobil kaynak değiştirilmedi. Test için server/container açılmadı.

Mobil agent şimdi fixture'larla wire DTO, mock transport, lobby ve online render
ayrımına başlayabilir. Yerel bot modunun mevcut fiziği korunur. Canlı House
Rockets endpoint'ine bağlanma veya production'da oyunu açma kapısı henüz geçilmedi.
