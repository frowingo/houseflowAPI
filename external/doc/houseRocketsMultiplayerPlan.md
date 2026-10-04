# House Rockets — Backend ve iOS ortak geliştirme planı

Tarih: 4 Ekim 2026.

Doküman revizyonu: `mobileHandoff.1`. Bu, wire contract revizyonu değildir.

Durum: **Paket 1–6 backend geliştirmeleri ve testleri tamamlandı; Paket 7'nin
backend ölçüm/admission altyapısı ve yerel yük/regresyon testleri tamamlandı.**
Paket 7'nin gerçek Fly kapasitesi ve ortak mobil yayın kabulü henüz tamamlanmadı.
Sözleşme revision'ı `houseRockets.v2.1`, fixture schema'sı `1`, protocolVersion `2`,
courseVersion `1`. Go tanımı, wire DTO'ları, bağımsız v2 decoder ve ortak JSON
fixture'ları, deterministik simülasyon, iki instance arasında girdi yönlendiren runtime
ve gerçek v2 HTTP/socket entegrasyonu, kalıcı sonuç, yeni oturumla rematch,
checkpoint ve owner recovery mevcut.
**House Rockets yalnız açıkça etkinleştirilen local/staging ortamında kullanılabilir.**
Production kapalıdır; yayın kararı Paket 7 yük/gecikme ve mobil kabulüne bağlıdır.
Mobilde mod seçimi, yerel bot modunun korunması, sahne/simülasyon ayrımı ve servis
enjeksiyonu kullanıcı tarafından tamamlanmış olarak bildirildi. Online mobil
implementasyonu ve gerçek iki cihaz entegrasyonu henüz doğrulanmadı.

## 0. Mobil agent için güncel başlangıç rehberi

Bu belge doğrudan mobil geliştirme görevi olarak verilebilir. **Önce bu bölüm,
ardından bölüm 3–7 ve bölüm 9–10 okunur.** Bölüm 2 ilk taramanın tarihsel
kaydıdır; bölüm 8 backend paketlerinin aşamalı teslim geçmişidir. Bu bölümlerde
"henüz yok", "sonraki pakette" gibi ifadeler ilgili teslim anını anlatır;
bugünkü eksik iş listesi değildir. Güncel durumun tek özeti aşağıdaki tablodur.

### 0.1. Güncel teslim durumu ve korunacak temel

| İş grubu | Bugünkü durum | Mobil agent'ın görevi |
| --- | --- | --- |
| Mobil temel: iki mod, yerel bot, render/simülasyon ayrımı, servis enjeksiyonu | Kullanıcı tamamlandığını bildirdi; 4 Ekim salt-okunur kod kontrolünde yapı mevcut. Bu kontrol mobil build/test kabulü değildir. | Mevcut değişiklikleri koru, güncel kodu ve testleri incele; yeniden implement etme. |
| Backend Paket 1: contract/fixture | Backend testleri geçti; `houseRockets.v2.1` / protocol `2` / course `1`. | Wire DTO/decoder ve test fixture mapping'ini uygula. |
| Backend Paket 2–4: fizik/runtime/gateway/lobi | Gerçek Redis/Mongo ve iki instance HTTP/WebSocket testleri geçti. | Local/staging üzerinde online lobby ve oynanışı bağla. |
| Backend Paket 5: kalıcı sonuç/rematch | Transaction/result HTTP/socket/rematch testleri geçti. | Finalizing, sonuç fallback ve yeni session rematch akışını uygula. |
| Backend Paket 6: reconnect/recovery | Checkpoint/fencing/owner crash ve lifecycle regresyonları geçti. | Epoch/grant/resync/background/exit davranışlarını uygula. |
| Backend Paket 7: ölçüm/admission | 9 yerel yük senaryosu ve compose race regresyonları geçti. | 503/backoff'u ele al; gerçek cihaz kabulünü yürüt. Yerel ölçümü production garantisi sayma. |
| Ortak production kabulü | Açık; production House Rockets kapalı. | Cihaz/ağ ve staging kapasite sonuçları kabul edilmeden production aktivasyonu yapma. |

Mobil başlangıç kontrolünde mevcut şu yapılar görüldü:

- `HouseRocketsSessionFactory.makeBotSession` ve `AppDependencies` içindeki
  `.localOnly` enjeksiyonu; her ekran kendi session'ını oluşturur.
- `HouseRocketsMode.localBots / housemates` ve `HouseRocketsLaunchContext`.
- `HouseRocketsScene.applySnapshot`; sahne authoritative fizik ilerletmiyor.
- ViewModel'in online seçeneği hâlâ `serviceUnavailable` ile kapalı;
  start/steer/restart yalnız `localBots` için çalışıyor.
- Presentation/player/geometri kimlikleri hâlâ UUID, palet dört renk,
  snapshot kabulü tek revision filtresi. Bunlar online uyarlama noktalarıdır;
  backend ID'leri UUID'ye zorlanmaz, eski demo wire DTO diye kullanılmaz.

Bu notlar iOS checkout'unun değişiklikler içeren çalışma ağacından okunmuştur;
agent mevcut staged/unstaged işleri korur. Kendi güncel kodunu tekrar kontrol
eder, bu listeyi eksiksiz mobil audit veya test başarısı kabul etmez.

### 0.2. Kaynaklar, sürümler ve çalışma sınırı

- Kanonik belge: backend repo `external/doc/houseRocketsMultiplayerPlan.md`.
  Mobil repoda görülen `HouseFlow/houseRocketsMultiplayerPlan.md` kopyası güncel
  kabul edilmez. Kopya kullanılacaksa bu revizyonla eşitliği doğrulanır;
  farklı planlarla iki paralel sözleşme yürütülmez.
- Uygulama kodu için backend bölüm 5'teki modeller/decoder ve aşağıdaki JSON
  fixture'ları esas alınır. Belge/kod/fixture arasında uyuşmazlık görülürse
  sessizce yeni field/endpoint üretme; somut farkı backend tarafına bildir.
- Fixture `schemaVersion=1`, contract `houseRockets.v2.1`, protocol `2`,
  course `1` birbirinden ayrı sürümlerdir. JSON üst metadata'sı socket payload'ı
  değildir. `liveGatewayAvailable: false`, Paket 1 fixture'ının tarihsel
  işaretidir; bugünkü runtime feature flag'i veya mobil UI engeli **değildir**.
  Mobil bu alanı canlı ortam erişimi kararı için kullanmaz.
- Protocol fixture:
  `internal/application/game/gameSpesific/houseRockets/fixtures/houseRocketsProtocol.json`.
  Physics fixture aynı dizinde `houseRocketsSimulation.json`.
  Agent backend checkout'una erişmeli veya aynı revizyonun JSON'larını almalı.
  Mobilde kullanılacak JSON kopyaları test kaynaklarında tutulur; target/bundle
  membership doğrulanır. Backend fixture klasöründe Swift üreticisi yoktur ve
  bu iş için yeni Swift üreticisi gerekmez.
- Mobil geliştirme yalnız House Rockets online entegrasyonunu ve gerçekten
  gereken ortak transport/model sınırlarını kapsar. Backend/migration/wire
  contract değişikliği, başka oyunları dönüştürme veya global oyun framework'ü
  oluşturma bu görevin parçası değildir.

### 0.3. Canlı test ortamı — başlamadan önce kontrol et

Backend kodunun hazır olması deploy edilmiş Fly adresinde oyunun açık olduğu
anlamına gelmez. Mobil `AppEnvironment.development` şu an Fly adresine gidiyor;
bu adresin House Rockets desteklediğini varsayma. Test base URL'si backend
tarafıyla ayrıca doğrulanır; physical device'ta `localhost` telefonun kendisidir.
Gerekirse test ortamına özel base URL enjeksiyonu kullan; bütün uygulamanın
production adresini sabit bir local IP'yle değiştirme. HTTP URL `/api/v1`
path'ini korur, socket için HTTPS→WSS / HTTP→WS dönüşümü yapılır.

Backend tarafında canlı deneme için:

```text
APP_ENV=local veya development veya staging
HOUSE_ROCKETS_ENABLED=true
REALTIME_MAX_OWNED_ROOMS=<pozitif tamsayı>
REALTIME_MAX_CONNECTIONS=<pozitif tamsayı>
```

Çalışan Redis coordinator ve migration'ları uygulanmış transaction destekli
Mongo gerekir. Son test tesliminde local container'lar durdurulmuştur; bu belge
çalışan server/adres/secret sağlamaz. Ortam açılması ve limit seçimi backend
sorumluluğudur. Mobil limitleri seçmez ve production gate'i kaldırmaz.
Base URL, geçerli test hesapları ve aynı house üyelikleri güvenli yoldan
sağlanır; token, Redis/Mongo bağlantı bilgisi veya secret dokümana/log'a yazılmaz.

HTTP/token/status preflight için bölüm 5.1; yetkili socket yolu bölüm 5.2'dir.
Aktif-session GET'teki 404 tek başına feature kapalı demek değildir: henüz
aktif oda yoksa da 404 alınabilir. Aynı house ile PUT oturum oluşturur/bulur;
otomatik join/ready yapmaz. Devre dışı oyun, yanlış URL ve auth hatasını
örnek fixture'ın hazır olmasıyla gizleme. Canlı erişim yoksa DTO/mock testlerine
devam et, yalnız canlı kabulü ortam engeli olarak raporla.

### 0.4. Mobil geliştirme fazları ve çıkış ölçütleri

Buradaki M0–M6 **mobil iş sırasıdır**, backend Paket 1–7'nin yeniden yapılması
değildir. Her fazdan sonra değişen dosyalar, çalıştırılan testler ve sonraki
fazın bağımlılıkları raporlanır. Temel cancellation/context isolation baştan
kurulur; son faza kadar çalışan eski socket/task bırakılmaz.

#### M0 — Mevcut temeli doğrula ve koru

Güncel mode/factory/ViewModel/Scene/local simulation sınırlarını tara; mevcut
bot testlerinin nerede olduğunu ve yeni online akışın ekleneceği noktaları
belirle. Yerel bot fiziği, pause/resume, offline açılış, rematch, artwork ve
localization davranışı korunur. Sadece online entegrasyon için gereken
uyarlamayı yap; kullanıcı tarafından tamamlanan dört işi yeniden yazma.

Çıkış: Mevcut yapı ve test durumu raporlandı; online adapter'in bağlanacağı
factory/service sınırı açık; ilgisiz/staged değişiklikler korunuyor.

#### M1 — Wire DTO, mapping ve HTTP/WebSocket transport

Bölüm 5'in bütün mesajlarını, session/game/result DTO'larını ve hatalarını
ayrı wire tipleriyle decode et. String ID, Int64 sıra/tick/epoch, nullable vs
omitted alanlar, UTC tarih çeşitleri, sekiz renk ve gerçek `displayName`
mapping'ini tamamla. Mevcut demo Codable enum'larını client JSON diye gönderme.
HTTP status/headers korunur; localized error string'inden kod türetilmez.
Socket `Authorization` header'ı kullanır, token query'ye yazılmaz.

Mevcut network/keychain dependency grafiğini genişlet; per-screen online
session factory ekle. Send/receive ve timer'lar iptal edilebilir, buffer'lar
sınırlı olur. Eski snapshot'lar latest-state şeklinde değiştirilebilir; kontrol,
rejection ve result mesajları sessizce aynı coalescing buffer'ında atılmaz.
Mock transport ve enjekte edilebilir saat ile test edilebilir sınır kullan.
Yeni connection/session generation eski async cevapların UI'ı değiştirmesini
engeller. Bilinmeyen uyumsuz protocol/course desteklenmiş gibi oynatılmaz.

Çıkış: Protocol JSON fixture'larındaki envelope/payload'lar decode ediliyor;
geçerli client mesajları sözleşmeyle aynı şekli üretiyor; nil/0/false, büyük
integer, fractional UTC, HTTP 401/403/404/409/429/503 ve cancellation testleri
var. Test iznine uygun çalıştırılan/çalıştırılamayan kontroller ayrılır.

#### M2 — Online session, lobby, ready ve countdown

Context doğrula → house/game için PUT → returned session ID ile socket →
welcome + session snapshot → roster'da yoksa join → landscape sonrası ready.
Ev/oyun başına tek aktif oda paylaşılır; kullanıcı başına yeni oda açılmaz.
Session version, oyun `(runtimeEpoch,stateSequence)` ve input sequence ayrı
tutulur. Pending command `messageId` ile eşlenir; commandAccepted uygulanmış
ready/leave/cancel kanıtı değildir. Server deadline'larından countdown çizilir;
yerel demo countdown'u online maçı başlatmaz. Application heartbeat hazır
katılım sırasında da sürer; transport pong onun yerine geçmez.

Çıkış: Mock lobi/ready/rejection yarışı ve duplicate/out-of-order session
snapshot testleri geçiyor. Ortam hazırsa aynı evin iki gerçek hesabı aynı
session'da join/ready/countdown görüyor; tek hazır kişi maçı başlatmıyor.
Canlı test yapılamadıysa fazın canlı kabulü ayrıca açık kalır.

#### M3 — Authoritative render, kontrol ve prediction/reconciliation

Tam game snapshot'ını mevcut render sınırına map et; online modda Scene yerel
authoritative simulation başlatmaz. Kamera, parkur açısı, alanlar ve bütün
oyuncular aynı render zamanını kullanır. Diğer oyuncular interpolation ile;
local roket bounded prediction + ACK reconciliation ile gösterilir. Bölüm 6.2
başlangıç buffer hedefleri ölçümle ayarlanır, wire/oyun kuralı değildir.

Grant + tam eşleşen epoch/session/generation, playing, foreground ve alive
koşulları olmadan steering açma. Grant ve snapshot geliş sırasına bağımlı olma.
Joystick'i parkur koordinatına bir kez dönüştür; latest niyet en çok 20 Hz,
yön sabitse sürekli steer yok. Input sequence generation'a göre artar; ACK
sonrası buffer temizlenir. Snapshot kaybında bounded extrapolation/resync
uygulanır, sınırsız Task/input backlog üretilmez. Yeni epoch ve control generation
eski prediction/input'ları geçersiz kılar; eski steering replay edilmez.

Çıkış: 2/4/8 oyunculu fixture mapping, 25 s parkur dönüşü, hız alanları,
geciken/tekrarlanan frame, grant/snapshot sırası, ACK, bounded buffer ve
eşzamanlı elenme testleri var. Canlı iki client birbirini ve ortak engelleri
görüyor; elenen kişi izliyor. Mobil elenme/kazanan otoritesi üretmiyor.
Prediction/render testleri gerçek cihaz kontrol hissi kabulü yerine geçmez.

#### M4 — Kalıcı sonuç, iptal ve rematch

Finalizing'de kayıt bekle; kesin sonucu `houseRockets.result` veya eski session
ID'siyle HTTP result üzerinden al. Completed/draw/cancelled ve tied rank'ları
ayrı göster; cancelled veya expiry için distance'tan winner/rank üretme.
Result dedup session/event ID ile yapılır; terminal session'a socket retry yok.
Commit bekleyen 404 bounded retry edilir, lobby/countdown cancellation'da
result kaydı olmayabileceği korunur. Rematch PUT → yeni/başkasının açtığı aktif
lobby → gerekiyorsa join → yeniden ready; eski session yerelde resetlenmez.

Çıkış: Sonuç event'i kaybında HTTP fallback, aynı sonucun tekrar gelişi,
iptal/beraberlik, geç sonuç ile yeni maç yarışı ve rematch isolation testleri
var. Ortam hazırsa tam lobby→playing→result→rematch akışı canlı doğrulanır.

#### M5 — Reconnect, recovery, background ve context lifecycle

Bölüm 6.3 ve Paket 6 mobil sırasını uygula. Socket kapanması business leave
değildir; kısa kopmada sınırlı jitter/backoff ile reconnect/full sync gerekir.
Recovery frame'i kaybolabilir; daha yüksek epoch'ta doğrudan playing de
gelebilir. Yeni grant gelmeden kontrol açma; diriltme veya yeni local spawn yok.
Background online maçı pause etmez; heartbeat/steering durur, foreground'da
resync alınır. Bot modu mevcut pause davranışını korur.

Açık Exit ayrı leave niyetidir; logout/house değişimi/mod değişimi/eski ekran
kapanışı idempotent cleanup üretir. Eski görev hiçbir yeni context'i etkileyemez.
401/403/unsupported protocol/terminal conflict retry edilmez. 429 hızı düşürür;
geçici network/503 bounded retry edilir, `Retry-After` asgari beklemedir. İkinci
cihazın kontrol devrinde yalnız güncel grant geçerlidir; eski connection'ın
stale-control hatası sonsuz bind/resync yarışına dönüştürülmez.

Çıkış: Kopma/grace, spectator reconnect, epoch/grant değişimi, background,
logout/house değişimi, yetki/token kaybı, kapasite 503 ve tekrar cleanup
testleri var. Owner kaybı senaryosu backend tarafıyla koordineli staging'de
doğrulanır; agent izinsiz production process'i öldürmez/secret değiştirmez.

#### M6 — Ortak gerçek cihaz ve yayın kabulü

İki gerçek cihaz/hesap ile tam maç; 100/200/400 ms RTT, jitter, kısa kopmalar,
30/60/120 FPS desteklenen cihazlar, iPhone/iPad/orientation ve reduceMotion
kontrolü. Yerel bot/diğer oyun regresyonları korunur. Test yöntemi, ortam,
cihaz/FPS, ağ koşulu, sonuç ve açık sorunlar raporlanır; mock kabul diye yazılmaz.
Production flag, global concurrent oda hedefi ve instance/maliyet limitleri
backend ile ortak yayın kararına bağlıdır. Mobil agent tek başına açmaz.

Çıkış: Bölüm 9 senaryoları ve Paket 7 ortak kapısı için kanıtlı kabul kaydı;
çözülmemiş kontrol hissi/doğruluk/lifecycle sorunu varsa production kapalı
kalır. Mobil yalnız M1–M5 implementasyonu bitti diye yayın kabulü tamamlandı
sayılmaz.

### 0.5. Mobil agent'a doğrudan verilecek görev

> Bu belgenin güncel başlangıç rehberini ve bölüm 3–7/9–10'u oku. Mobil repo'da
> tamamlanan mode seçimi, yerel bot, scene/simulation ayrımı ve servis enjeksiyonu
> değişikliklerini koru; güncel codebase ve repo talimatlarını kontrol et.
> M0 kontrolünden sonra M1 ile online House Rockets entegrasyonuna başla,
> fazların çıkış ölçütlerini doğrulayarak M2–M6 sırasıyla ilerle. Backend hazır
> local/staging sözleşmesini kullan; production'ı açma, wire/ürün kurallarını
> tek taraflı değiştirme. Her teslimde kod/test/mock/canlı kabul durumlarını
> ayrı raporla. Ortam veya hesap eksikse güvenli DTO/mock geliştirmesine devam
> et ve yalnız canlı kapıyı engellenmiş olarak belirt. Secret'ları paylaşma;
> commit/build/test izinleri için kullanıcı ve mobil repo talimatlarını izle.

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

## 2. İlk codebase incelemesi — tarihsel kayıt

İlk planlama sırasında incelenen repository'ler:

- Backend: `/Users/frowing/Projects/houseflowApi`.
  İnceleme HEAD'i: `985a8816b044c47a48e8009fccd8ef7a4eb9604d`.
- iOS: `/Users/frowing/Projects/houseflowApp`.
  İnceleme HEAD'i: `18c183d0b38dbccb753ebb005ee901a8bb4eedd6`.

Aşağıdaki bulgular yalnız bu ilk HEAD'lerdeki koda aittir; "mevcut" kelimesi
ilk inceleme anını anlatır. İlk planlama çalışmasında test çalıştırılmamıştır;
sonraki backend paketlerinde çalıştırılan testler bölüm 8'de kayıtlıdır.
4 Ekim mobil başlangıç kontrolü backend `9c5a3d0`, mobil `30e80f0` HEAD'leri
ve mobildeki staged değişiklikler üzerinden salt-okunur yapılmıştır; güncel
gözlemler bölüm 0'dadır. Mobil build/test bu doküman düzenlemesinde yapılmadı.
Agent kendi checkout'undaki değişiklikleri kontrol eder ve tarihsel tablodaki
eksikleri bugünkü eksik iş listesi olarak uygulamaz.

### 2.1. İlk incelemedeki backend parçaları (Paket 1 öncesi)

Bu alt bölüm ilk incelemenin tarihsel kaydıdır; güncel aktivasyon, teslim
durumu ve mobil devam işleri bölüm 0'dadır.

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

İlk incelemede bulunan HTTP ve WebSocket yolları:

```text
PUT /api/v1/game/:gameKey/session
GET /api/v1/game/:gameKey/session?houseId=:houseId
GET /api/v1/game/:sessionId/realtime
```

`PUT` body: `{ "houseId": "<houseId>" }`. Başarılı HTTP cevapları
`{ "success": true, "data": ... }`, hatalar `{ "success": false, "error": ... }`.
İlk incelemede `houseRockets` katalogda yoktu. Bu tarihsel eksik Paket 4'te
giderildi; bugün local/staging'de bölüm 0.3 koşullarıyla kullanılabilir.

Başlangıç taramasında backend'deki önemli boşluklar (tarihsel tespitler;
güncel teslim durumu bölüm 0; ayrıntılı geçmiş bölüm 8'dedir):

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

### 2.2. İlk incelemedeki iOS oyun yapısı

Bu tablo mobil temel refactor'undan öncedir. Scene'in fizik sahibi olması ve
factory/enjeksiyon eksikliği gibi eski tespitler bölüm 0'daki tamamlanan temel
işlerle güncellenmiştir; tekrar uygulanacak görev değildir.

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
ve ortak fixture'larla Go'ya aktarıldı. Başlangıçta rastgele parkur/seed sistemi
eklemek gerekli değil; mevcut indeksli parkur için `courseVersion` yeterli.

### 3.2. Paket 1 başlangıç kuralları

| Konu | Bu revision'ın başlangıç kararı |
| --- | --- |
| Online oyuncu sayısı | Kullanıcının onayıyla en az 2, ev kapasitesi kadar ve en fazla 8. Mevcut ensure-session handler house kapasitesine göre limiti daraltır. Bot eklenmez. |
| Ready window / countdown | 30 s / 3 s; otoriter server deadline'ları. Demodaki 750 ms countdown online'a taşınmaz. |
| Countdown sonrası katılım | Yeni yarışmacı alınmaz. Güncel ev üyeliği olan roster dışı kişi snapshot alıp seyredebilir; yarışmacı olmak veya steering için frozen roster + geçerli kontrol gerekir. Maç katılımcısı yeniden bağlanabilir. |
| Kontrol bağlantısı | Oyuncu başına tek geçerli generation/connection. Mevcut backend'de yeni başarılı control bind generation'ı döndürür; eski bağlantı input gönderemez. İkinci cihaz reddedilecek varsayımı yapılmaz, stale-control sonrası iki cihaz arasında sonsuz rebind yarışı kurulmaz. |
| Bağlantı kopması | Kısa grace boyunca son yönle ilerleme; reconnect oyuncuyu yeniden üretmez, elenmişse izleyici olarak döndürür. Güncel grace 10 s. |
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
- `internal/application/game/gameSpesific/houseRockets`: Tanım/enum, game-specific
  state, kurallar, geometri ve bağımsız simülasyon mevcut. Oyun completion/result
  handler'ları bu dizinin `commands`/`queries` altındadır. Saf motor
  HTTP/WebSocket/SpriteKit/Redis tiplerine bağımlı değildir.
- `internal/application/game/commands` ve `queries`: Ortak session oluşturma/
  keşfetme, üyelik/lifecycle ve yetkilendirme geçişleri; mevcut CQRS kurallarını izler.
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

Online devam için sorumluluklar aşağıdadır. Bölüm 0'da tamamlanmış temel korunur;
mevcut factory/scene/service sırf bu liste nedeniyle tekrar oluşturulmaz.
Dosya yolları sorumluluk önerisidir; güncel mobil dizin düzeniyle uzlaştırılır.
Yeni dosya adları camelCase, Swift tip adları mevcut PascalCase dil kuralını izler:

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
ediliyor. Paket 4 ile v2 gateway ve HTTP/socket aktivasyonu local/staging için
uygulandı; Paket 5 ile kalıcı sonuç endpoint'i de eklendi.

Ortak referanslar:

- [Protokol fixture'ı](/Users/frowing/Projects/houseflowApi/internal/application/game/gameSpesific/houseRockets/fixtures/houseRocketsProtocol.json): Geçerli/geçersiz
  client mesajları, welcome/session/countdown/playing/recovering/result/error
  örnekleri ve HTTP response'ları. `liveGatewayAvailable: false` Paket 1'in
  tarihsel metadata'sıdır; güncel local/staging kapısı değildir (bölüm 0.2).
- [Fizik fixture'ı](/Users/frowing/Projects/houseflowApi/internal/application/game/gameSpesific/houseRockets/fixtures/houseRocketsSimulation.json): Mevcut Swift
  kaynakları değiştirilmeden çalıştırılarak elde edilen 2/4/8 kişilik spawn,
  altı hareket senaryosu, geometri, dönüş, field salınımı ve yüzey teması.
  Paket 2'de üç navigation/effect senaryosu eklendi: 30 s parkur sürüşü,
  boostThenHold ve slowThenHold. `absoluteTolerance: 1e-9` ile Go motoru
  bütün referanslarla karşılaştırılıyor. Kaynak SHA-256'ları dosyada.
  Bu bağımsız Swift referans kontrolüdür; mobil test suite/iOS build kabulü değil.
- `internal/application/game/gameSpesific/houseRockets/definition.go`: Oyun-specific sabitler
  ve enum'lar; ortak GameSession domain'ine fizik/puan mantığı eklenmedi.
- `internal/application/game/gameSpesific/houseRockets/models.go`: House Rockets wire modelleri.
- `internal/application/game/gameSpesific/houseRockets/protocol.go`: Oyuna özgü mesaj/hata tanımları.
  Ortak ready/ping/pong modelleri `internal/models/dtos/realtimeModels.go` içinde.
- `internal/infrastructure/realtime/protocolV2.go`: Gateway'in kullandığı v2 validation ve
  server envelope'ları.
- `tests/houseRocketsContract_test.go`: JSON round-trip, kurallar, input
  sınırları, typed rejection ve mevcut v1/katalog izolasyonu testleri.
- `tests/houseRocketsSimulation_test.go`: Swift golden karşılaştırmaları,
  fizik/sonuç/restore sınırları, frame-rate bağımsızlığı ve state fuzz testi.

### 5.1. Sürüm ve HTTP

House Rockets için `protocolVersion: 2`. Mevcut v1 session WebSocket
payload formatını sessizce değiştirmek yerine yeni camelCase sözleşme sürümü
tanımlandı. `flappyBird` v1 tanımı ve mevcut regression testleri korunur.
House Rockets katalog tanımı v2; bu kayıt ancak ilgili runtime/protokol hazırken
production'da açılır.

HTTP session oluşturma/keşfetme aynı yolları kullanır:

```text
PUT /api/v1/game/houseRockets/session
GET /api/v1/game/houseRockets/session?houseId=<houseId>
```

`PUT` retry'sı aynı house/game için aktif session'a döner. Oluşturma sonrasında
join ayrıca yapılır. Mobil, keychain'den güncel token ile HTTP ve socket açar.

Paket 5 ile eklenen kalıcı read yolu (Swagger'da da bulunur):

```text
GET /api/v1/game/:sessionId/result
```

Terminal oturum socket upgrade'ında reddedildiği ve aktif-session GET terminal
maçı döndürmediği için kayıp sonuç event'i bu HTTP endpoint ile telafi edilir.
JWT ve güncel ev üyeliği gerekir; ev dışındaki kullanıcıya 403, token yoksa 401.
Sonuç henüz yoksa `houseRockets.error.result_not_found` ile typed 404;
transient finalization sırasında UI bunu kısa aralıklı sınırlı retry ile ele alır.

HTTP error body'sindeki metin parse edilerek auth/forbidden/not-found ayrımı
yapılmaz. Network error modelinin HTTP status'u koruması planlanır. Mevcut tüm
HTTP error sözleşmesini değiştirmek gerekli değildir; error code eklenirse iki
taraf için burada açıkça tanımlanır.

### 5.2. Socket açılışı ve envelope

Paket 4 ile uygulanan v2 açılışı:

```text
GET /api/v1/game/<sessionId>/realtime?protocolVersion=2
Authorization: Bearer <JWT>
```

Query yalnız protokol uyumluluğu için; session ve game key server tarafından
doğrulanır. Gateway query negotiation yapar. v1'in query'siz
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
geçerliliği runtime/gateway'de ayrıca uygulanır.

`gameSession.snapshot` payload'ı mevcut HTTP `GameSessionResponseModel`
biçimiyle aynıdır. `sequence` yalnız session version'ıdır. Domain
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

Envelope/protocol rejection'ları decoder'da; kontrol/phase/rate-limit hataları
runtime/gateway'de, result hataları application katmanında mevcuttur. Mevcut
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
paket gönderilmez. ACK görülmeyen son niyet gerekiyorsa bounded aralıkla **yeni
artan inputSequence** ile gönderilir; bu retransmit de 20 Hz bütçesine dahildir.
Mevcut runtime aynı/eski sequence'i idempotent başarı olarak kabul etmez,
stale-control rejection üretir. Lifecycle command'larının aynı messageId ile
retry politikası steering'e uygulanmaz. ACK coalescing nedeniyle gönderilen
her sequence'i ayrı ayrı göstermeyebilir; sürekli resync/rebind döngüsü kurma.
App lifecycle ve kontrol canlılığı ayrı ping ile izlenir. Online ViewModel her dokunuş için
bir öncekini bekleyen sınırsız Task zinciri oluşturmaz.

Yerel owner'a girdi memory üzerinden ulaşır; uzak owner'a ayrı kısa ömürlü
Pub/Sub yolu uygulanmıştır. Bu adapter yolu mobilin yönlendirme sorumluluğu
değildir; client owner instance'ını seçmez.
Envelope actor/connection bilgisini server ekler. Owner, control generation,
source binding, epoch ve artan input sequence'i doğrular. Input subscription
hazır olmadan controlGranted verilmez. Owner değişiminde resync/control rebind
yapılır; eski girdi yeni maç veya yeni control generation'a taşınmaz.

Bu girdi yolu anlık veri kaybedebilir; retransmit/latest-state yaklaşımı kaybı
telafi eder. Ready/leave gibi kalıcı business niyetleri mevcut Streams yolunda
kalır. Snapshot dağıtımı oda başına/instance başına mevcut hub'ı kullanır.

Control ve gameplay rate limit'leri ayrıdır. Güncel steering ayarları
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

Genel socket timeout 45 s ve presence TTL 30 s, oyundaki control grace'in
yerine geçmez. Güncel online ayarları 2 s application ping, 6 s kontrol
canlılığı deadline'ı ve 10 s reconnect grace'tir. Owner gerçek
kontrol bağlantısının heartbeat'ini izler. Bu süreler ve server scheduling
payı fixture/testlerde tanımlıdır. Background'da heartbeat'in durması maçın
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

Güncel sonuç politikası (Paket 1'de sabitlendi, Paket 5–6'da uygulandı):

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
Result persistence ve HTTP endpoint Paket 5'te uygulandı.

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
yetki vermez. Owner adapter/CAS ve Redis counter reset testi Paket 3'te uygulandı;
Completion transaction Paket 5'te, checkpoint koruması Paket 6'da uygulandı.

Geçici Redis checkpoint varsayılanı 1 s aralık ve kritik elenme/forfeit
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

## 8. Backend paketleri — kapsam ve aşamalı teslim geçmişi

Bu bölüm backend paketlerinin tarihsel kapsamını ve her teslimdeki sınırları
korur. Özellikle Paket 2–4'teki "sonuç/recovery henüz yok" notları bugün geçerli
değildir; Paket 5–6 bunları tamamladı. Güncel durum bölüm 0.1'dir.
Önceki Faz 0–6 sıralaması artık Paket 1–7 olarak adlandırılır; paralel mobil işler
aşağıdaki başlangıç tablosunda ayrıca gösterilir. Diğer bölümler bu paketlere
referans verir. Paket 1–6 teslimleri, iki-instance gerçek socket senaryoları ve
Paket 7 yerel backend ölçümleri mevcut; gerçek Fly/mobil yayın kabulü açıktır.

**Sıradaki implementasyon mobil M1–M5, ardından ortak M6/Paket 7 kabulüdür.**
Mobil agent online geliştirmeye başlayabilir; yeni bir backend paketini
beklemez. Canlı bağlantı için çalışan local/staging ortamı gerekir.

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

Paket 2 uygulanan API ve sınırlar:

- Kodlar `internal/application/game/gameSpesific/houseRockets` altında:
  `course.go`, `simulation.go`, `simulationState.go`, `simulationResult.go`.
  Ortak GameSession domain'i, katalog ve gateway değiştirilmedi.
- `NewSimulation(NewSimulationParams)`: Authoritative session/house kimliği,
  injected UTC başlangıç zamanı ve sıralı 2–8 gerçek oyuncu roster'ı alır.
  Engine oynanış başında oluşturulur; lobby/countdown, üyelik/auth ve bot
  oluşturma işi yapmaz. Online backend'e bot AI eklenmedi.
- `Steer(playerId, courseHeading)`: Parkur koordinatında finite radyan;
  normalize edilir, ekran açısı tekrar çıkarılmaz. Engine control generation /
  inputSequence bilmez; bu yetki ve ACK kontrolü Paket 3 runtime sorumluluğudur.
- `AdvanceTicks(n)`: 0–30 tam fizik tick'i, her biri tam 1/120 s. Negatif/büyük
  batch reddedilir; gizlice delta clamp veya fractional step uygulanmaz.
  Scheduler, fractional süre birikimi ve bounded catch-up Paket 3'e aittir.
  Motor saat/ticker/socket/Redis/Mongo çalıştırmaz; mutable motor tek runtime
  loop'una aittir. Concurrent erişim için mutex'li ortak engine kullanılmaz;
  owner snapshot/state kopyasını aldıktan sonra diğer akışlara verir.
- `EliminatePlayers(ids, reason)`: Forfeit, connectionExpired veya
  membershipRevoked system geçişini atomic batch uygular. Önce bütün batch
  doğrulanır, sonra eleme ve survivor/draw hesabı yapılır; tekrar eleme yeni
  sonuç üretmez. Fizik arka-sınır elenmesini engine hesaplar.
- `Cancel(reason)` ve `ProposeResult(endedAt)`: Pure terminal outcome ve henüz
  commit edilmemiş result proposal. Son tick'teki survivor/draw expiry'den
  önce çözülür; devam eden oyun tick 36.000'de cancelled olur. Result sırası
  frozen roster'dır, eşit elimination tick'leri eşit competition rank alır.
  Proposal'ın endedAt'i UTC'ye çevrilir ve oynanan süreden önce olamaz.
  Result transaction/outbox/HTTP veya kesin sonuç yayını **Paket 5'te** yapılacak.
- `Snapshot()`: World positions, authoritative kamera/açı, bounded aktif
  geometri ve copied player/outcome state'i. Bu `WorldSnapshot` internal
  engine çıktısıdır, v2 wire DTO'su değildir; Paket 3/4 runtime metadata,
  bağlantı/ACK bilgisi ve alıcıya özel control generation ile wire'a map eder.
- `State()` / `RestoreSimulation(state)`: Internal checkpoint schema `1`,
  courseVersion `1`. Fizik tick'i, kamera, roster, heading, efekt kalan süresi,
  field temas geçmişi ve next gate index korunur. Geometri stabil indekslerden
  yeniden üretilir; cihaz/UUID/random state saklanmaz. Corrupt/NaN/infinite,
  uyumsuz sürüm, invalid outcome ve imkânsız geometri/alan bilgisi reddedilir.
  Snapshot, state ve result proposal caller-owned deep copy'dir. Checkpoint'in
  Redis'e yazılması, ownership fencing, control bindings ve eski checkpoint'in
  oyuncu diriltmesini önleme bariyeri Paket 3/6'da tamamlanacak.

Navigation fixture driver'ları yalnız test/reference içindir: İki oyuncu lane
offset 0 ile her 14 tick'te ilk geçide yönlendirilir. Boost senaryosu 294 tick
düz sürüşten; slow senaryosu iki oyuncu X > 1730 olduktan sonra alana yönelir.
Temas hedefi field'ın `elapsedSeconds + 0.05` konumu, temas sonrası heading π/2
ve 240 tick bekleme. Bu controller production online bot değildir.
Navigation referans değerleri iOS simülasyonundan üretilmiş JSON fixture'larında
saklanır. Backend reposunda Swift üreticisi bulunmaz; Go testleri Swift compiler'a
bağımlı değildir ve repoda tutulan JSON fixture'larını kullanır.

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

Paket 3 uygulanan API ve sınırlar:

- Oyun kuralları ve kontrol state'i `internal/application/game/gameSpesific/houseRockets/runtime.go`
  altında. Ortak room manager fizik bilmez; `internal/infrastructure/realtime/gameRuntime.go`
  küçük factory/adapter ile start, cancel ve fanout bağlar. Üretimde bot yoktur.
- `Runtime.Run`: 60 Hz scheduling, 120 Hz tam fizik tick'i. Fractional süre
  biriktirilir, snapshot deadline'ı 20 Hz ilerler; gecikme bir sonraki snapshot
  deadline'ını kaydırmaz. En fazla 250 ms / 30 tick catch-up yapılır; daha büyük
  boşluk `runtimeOverloaded` ile finalizing üretir, sessiz zaman atlama olmaz.
- `RoomManager.DispatchGameplay`: Kimlikler authenticated transport tarafından
  verilir; input içindeki player/connection değerleri caller kimliğiyle değiştirilir.
  Local owner'a memory mailbox yolu, remote owner'a Redis Pub/Sub gameplay kanalı
  kullanılır. Gameplay kalıcı command stream'e yazılmaz, başka owner'a yeniden
  yönlendirilmez/replay edilmez. Remote publish başarısı yalnız aktarımı gösterir;
  gerçek işlem onayı control grant ve snapshot ACK'sidir.
- Gameplay subscriber'ı Redis subscription confirmation'ından sonra hazır sayılır.
  Subscriber yoksa publish unavailable döner. Gameplay payload/envelope boyutu
  sınırlıdır; dolu ephemeral receive buffer mesaj düşürür. Girdiler 1 s TTL,
  runtime epoch, lease fence, actor/connection ve control generation ile doğrulanır.
  Kayıp bind için grant gelmediyse yeniden bind gerekir; steer/heartbeat güncel
  değerlerle devam eder. Transport retry/resync davranışı Paket 4'te bağlanacak.
- Tek controller binding: Yeni connection bind olduğunda generation yenilenir,
  input sequence/ACK sıfırlanır ve eski pending heading temizlenir. Aynı connection
  bind'i idempotent grant verir. Eski generation/epoch, duplicate sequence,
  roster dışı veya elenmiş oyuncu girdileri uygulanmaz. Oyuncu başına en yeni
  steer saklanır; ACK yalnız motora uygulanan sequence'i taşır. 30 mesaj/s sınırı
  ayrıca uygulanır; mobil gönderim hedefi 20 Hz olarak kalır.
- Physics loop'ta DB/Redis/socket I/O yoktur. Lease yenileme ayrı worker'dadır;
  konservatif request-start deadline'ı monotonic clock ile tutulur. Kaybedilen
  lease runtime'ı durdurur; snapshot yayını ayrıca Redis lease CAS ile fenced'dır.
  Fanout ayrı worker: snapshot buffer 1, control-event ve lifecycle mailbox 64.
  Yavaş snapshot consumer en güncel frame'i alır; kontrol olayları sessizce
  kaybolursa devam etmek yerine event overflow runtimeOverloaded üretir.
- `GameRuntimeOwner` collection ve `0041_gameRuntimeOwner.go` migration'ı:
  session ID unique `_id`; kalıcı generation, ownerInstanceId, leaseId, started.
  Generation lease alınmadan önce okunur, lease alındıktan sonra tek CAS ile
  artırılır; CAS başarısızsa aynı lease altında generation yeniden okunup denenmez.
  Runtime epoch Redis sayacı değil bu Mongo generation'dır. Claim sonrası lease
  yeniden doğrulanmadan engine/control yayını başlamaz. Mongo/Redis arasında
  atomik transaction olduğu iddia edilmez.
- `MarkRuntimeStarted`: generation + owner + lease conditional write ile spawn'dan
  yalnız bir kez başlatır. Lease kaybı sonrası started maç checkpoint olmadan
  yeniden spawn edilmez; `ErrRuntimeRecoveryRequired` döner. Bu, Paket 6 recovery
  implementasyonu yerine geçmez. Bu Paket 3 teslimindeki sınırdır; güncel
  recovery/cancellation davranışı Paket 6 bölümünde açıklanır.
- `RuntimeFrame` internal fanout formatıdır, public v2 snapshot DTO'su değildir.
  Private control generation sadece connection-targeted grant'te yayınlanır.
  Sonuç henüz commit edilmez; terminal frame **finalizing** olarak kalır, kesin
  result event'i üretilmez. Paket 3'teki isim placeholder'ları Paket 4 ile gerçek,
  countdown başında dondurulan kullanıcı isimlerine dönüştürüldü. Running
  leave/üyelik reconciler'ı ve authenticated gateway Paket 4 ile bağlandı.
- Ortak session finished/cancelled olduğunda oda worker'ları durur ve lease bırakılır.
  Testler `tests/houseRocketsRuntime_test.go` ve
  `tests/houseRocketsRuntimeIntegration_test.go` altında; mevcut room runtime
  deadline/handoff regresyonları da aynı test kapısına dahildir. Ephemeral bus
  subscriber hazırlığı/no-replay kontrolü `tests/gameplayCoordinationIntegration_test.go`
  içindedir.

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

Paket 4 teslim notu:

- `HOUSE_ROCKETS_ENABLED=true` açıkça verilmelidir. `APP_ENV` `local`,
  `development` veya `staging` olmalıdır; boş, `production`, `prod` ve bilinmeyen
  environment'larda flag verilse bile katalog ve gateway kapalı kalır.
  Redis coordinator yoksa HTTP kataloğuna da eklenmez. Production deploy veya
  secret değişikliği yapılmadı. Compose API'yi açarken flag ayrıca environment
  olarak geçirilmelidir; mevcut Compose varsayılanı oyunu açmaz.
- Mevcut `PUT /api/v1/game/houseRockets/session` oturum oluşturur/bulur;
  `GET /api/v1/game/houseRockets/session?houseId=...` başka instance'tan aynı
  oturumu keşfeder. Oluşturma otomatik join/ready yapmaz. House kapasitesiyle
  sınırlandırılan 2–8 oyuncu, 30 s hazır katılım ve 3 s countdown kuralları korunur.
  Mevcut ortak domain, tüm slotlar hazırsa ready window'u erken kapatabilir.
- Socket `?protocolVersion=2` ile açılır. İlk mesaj `realtime.welcome`, ikinci
  mesaj camelCase `gameSession.snapshot` olur. Session süreleri milisaniyedir;
  v1'in eski session wire formatı değiştirilmedi. İşlem kabulü uygulandığı
  anlamına gelmez; uygulanan lifecycle değişikliği session snapshot ile izlenir.
- Mobil landscape'e geçişini tamamladıktan sonra `{ "ready": true }` yollar.
  Backend cihazın fiziksel yönelimini ölçemez; ready mobilin bu önkoşulu
  tamamladığı beyanıdır. Ready penceresinden itibaren uygulama seviyesinde
  `realtime.ping` 2 s aralıklarla gönderilir; WebSocket control pong ve server
  presence yenilemesi uygulamanın aktif olduğu yerine kullanılmaz.
- Owner, aktif House üyelerini ve User.isActive durumunu varsayılan 2 s
  aralıklarla ve countdown/start öncesinde okur. Üyelik kaldırma/inaktivasyon
  bu poll + I/O sınırında uygulanır; transaction seviyesinde anlık revocation
  iddiası yoktur. Lobby/countdown'da 6 s heartbeat kaybı ready oyuncuyu çıkarır;
  yetersiz kalan ready window lobby'ye döner, countdown iptal edilir.
- Countdown başladığında gerçek firstName/lastName, oyuncu sırası ve renkleri
  dondurulur. Profilin sonra değişmesi maç isimlerini değiştirmez. Online'da
  bot oluşturulmaz. Oyun başlayınca yalnız frozen roster'daki canlı oyuncu
  kontrol alır; evin roster dışındaki geç katılan üyesi seyirci olarak snapshot
  alabilir, fakat steer veya geç join ile maça giremez.
- Normal steer 20 Hz client hedefi / 30 mesaj/s server tavanıyla ayrı sınırlanır;
  lifecycle varsayılanı 30 mesaj/10 s, uygulama ping'i 5 mesaj/s'dir. Steering
  accepted mesajı üretmez; processed ACK snapshot'tadır. Remote owner rejection
  aynı client messageId ile geri gelir. Player/connection kimliği JWT/gateway'den
  bağlanır; client payload'ı actorId/connectionId belirleyemez.
- Control grant yalnız kontrol bağlantısına gider. Diğer oyuncu/seyirci snapshot'ında
  private controlGeneration `null` olur. Aynı kullanıcının yeni bağlantısı
  generation'ı döndürür; eski bağlantı steer yapamaz. Runtime epoch ve game state
  sequence ile eski frame'ler elenir. Control değişiminde tick ilerlemese de
  state sequence artar. Lobby session version ile game state sequence farklıdır.
- `houseRockets.resync` güncel session ve varsa tam game snapshot'ı ister;
  yaşayan roster oyuncusu yeniden kontrol isteyebilir. Eski steering replay
  edilmez. Yalnız öldürülen oyuncu snapshot alarak seyredebilir, yeniden canlanmaz.
  Snapshot kuyruğu en güncel bir frame'i tutar; welcome/control/session/resync
  mesajları sınırlı ayrı kuyruktadır. Dolu control kuyruğu/yazma timeout'u bağlantıyı
  kapatır; bir client'ın socket yazımı oda event fanout'unda yapılmaz.
- `leave` running oyuncuda forfeit, üyelik kaybı membershipRevoked üretir.
  Ani bağlantı kapanması business leave değildir; 10 s reconnect grace akışı
  kullanılır. Aynı Advance'taki mixed elenmeler birlikte değerlendirilir.
  Token süresi dolan veya üyeliği kaldırılan socket kesilir. Kapanışta her zaman
  okunabilir WebSocket close frame gelmesi garanti edilmez; mobil HTTP status ve
  token yenileme/üyelik kontrolüyle sınırlı retry yapmalıdır.
- Engine terminal olduğunda snapshot `finalizing` kalır, `winnerId: null` olur;
  kalıcı sonuç/event/result HTTP ve otomatik rematch bu pakette **yoktur**.
  Aktif session bu yüzden yeni maç için resetlenmemelidir. Test maçını kapatmak
  için yetkili lifecycle cancel kullanılabilir. Running owner kaybında checkpoint
  recovery bu Paket 4 tesliminde yoktur; mevcut started bariyeri yanlış yeniden
  spawn'ı engeller. Güncel sonuç/recovery davranışı Paket 5/6 bölümlerindedir.

Mobil agent şimdi gerçek local/staging HTTP/socket, lobby ve karşı oyuncu hareketi
entegrasyonuna başlayabilir. Gerçek iki iOS cihazındaki gecikme/UX kabulü ayrı
olarak yapılmalıdır; backend testleri bu cihaz kabulünün yerine geçmez.

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

#### Paket 5 teslim notu — Backend ve mobil entegrasyon davranışı

- Ortak application port `internal/application/game/abstract/gameMatchRepository.go`,
  Mongo adapter `internal/data/database/gameMatchRepository.go` altındadır.
  `internal/application/game/domain/matchResult.go` yalnız ortak kimlik, payload
  şema sürümü, status, bitiş nedeni ve zamanları içerir. Repository House Rockets
  DTO'suna veya kurallarına bağımlı değildir; diğer oyunlar aynı transaction'ı
  kendi JSON payload'ları ve result event type'larıyla kullanabilir.
- Henüz uygulanmamış `0042_gameMatchResult.go` migration'ı doğrudan ortak
  `GameMatchResult` koleksiyonunu, validator'ünü ve sonuç akışının hata çevirilerini
  hazırlar. Tekrarlanabilir; mevcut sonuçlara veya outbox kayıtlarına dokunmaz.
  Oyuna özgü ayrı bir sonuç koleksiyonu veya eski kayıt taşıma adımı oluşturulmaz.
  `_id = sessionId` unique primary index'tir; aynı session'a ikinci sonuç eklenmez.
- Kimlik/schemaVersion/status/zaman/epoch alanları BSON, kanonik wire payload JSON binary'dir.
  JSON null'ları, integer tick ve tarih hassasiyetini korur. Payload hash replay'de
  değiştirilmiş sonucu reddeder; ortak metadata veya event type değişimi de conflict'tir.
  Kazanan, rank, oyuncu kimlikleri, süre/tick ve mesafe House Rockets application
  handler'ında doğrulanır. `gameSpesific/houseRockets/resultPersistence.go` wire DTO ile
  ortak envelope dönüşümünü ve okunan payload'ın oyun metadata uyumunu sağlar.
  House Rockets payload schemaVersion `1`, courseVersion `1` ve protocolVersion `2`
  birbirinden bağımsız kavramlardır; ortak schemaVersion mobil response'a eklenmez.
- Engine terminal kararı bir kez önerir; bu sırada `phase: finalizing`,
  `winnerId: null`, bütün `controlGeneration` değerleri null'dır. Fizik ilerlemez.
  Owner aynı öneriyi saklama hatalarında yeniden dener; lease yenileme DB
  döngüsünden bağımsızdır. Commit'e kadar aktif session slot'u korunur.
- `CompleteMatchCommand` yalnız system/runtime yolundadır; client sonuç/puan/
  kazanan gönderemez. Transaction ownership kaydına generation + instance + lease
  + started + completed koşullarıyla **write** yapar. Sonuç, session terminal
  durumu, yalnız o session'ın aktif slot'unun silinmesi, system receipt, varsa
  kullanıcı cancel receipt'i ve outbox birlikte commit olur. Claim/completion aynı
  ownership kaydına yazar; eski generation commit edemez. Aynı sonucu tekrar
  tamamlama kanonik kaydı döndürür, yeni maçın slot'unu silmez.
- Session bitiş olayı kendi version'ında; sonuç event'i outbox'ta
  `aggregateType: gameMatch`, `aggregateId: sessionId`, version `1` ile
  tutulur. Böylece session'ın unique aggregate/version indeksine çakışmaz.
  Mobil mesaj tipi, `houseRockets.result:<sessionId>` event ID'si ve HTTP payload'ı değişmez.
- Commit sonrası session terminal snapshot'ı, `ended`/`cancelled` oyun snapshot'ı
  ve `houseRockets.result` yayımlanır. Terminal oyun snapshot'ı hareket
  snapshot'ları gibi coalesce edilmez; sonuçla birlikte bounded control queue'ya
  alınır. `houseRockets.result:<sessionId>` event ID'si dedup anahtarıdır.
  Redis publish kabulü bütün cihazların mesajı aldığının ACK'i değildir.
- Publish kaybında sonuç DB'de kalır; result HTTP aynı payload'ı döndürür.
  Yayın başarısızsa outbox satırı unpublished kalır. Bu paket otomatik outbox
  replay worker'ı eklemez; mobil HTTP fallback kullanır. Process/lease kaybında
  RAM'deki henüz commit edilmemiş öneriyi kurtarmak ve finalizing süresini
  sınırlamak Paket 6 checkpoint/recovery tesliminde tamamlandı.
- Running `gameSession.cancel` önce güncel üyelik + creator/house owner yetkisi
  ve command ID reuse kontrolünden geçer. Engine iptal önerisi ve kullanıcı
  receipt'i aynı result transaction'ına girer; DB command ACK'i commit'ten sonra
  verilir. Terminal `gameSession.snapshot` iptal isteğinin `messageId` değerini
  korur; mobil pending cancel'ı bu yanıtla kapatabilir. Lobby/countdown iptali
  sonuç üretmez; başlamamış maç için HTTP 404 normaldir.
- Completed maçta kazanan veya aynı tick'te herkes elendiyse beraberlik ve tied
  rank'lar korunur. Cancelled maçta `winnerId: null` ve bütün `rank: null`;
  mobil distance'tan kazanan üretmez. Beş dakikalık physics sınırı da cancelled'dır.

Mobil agent şimdi finalizing, leaderboard/beraberlik, iptal ve rematch ekranlarını
canlı local/staging backend'e bağlayabilir:

1. `finalizing` geldiğinde kesin kazanan göstermeden kayıt bekleme ekranı aç.
2. `houseRockets.result` payload'ını session ID ile sakla; aynı event tekrarını
   yeni sonuç sayma. Socket event kaybolursa bilinen **eski session ID** ile
   `GET /api/v1/game/:sessionId/result` çağır. Henüz commit yoksa 404 için kısa,
   sınırlı backoff uygula; 401/403'ü kayıt beklemesi sayma.
3. Finished/cancelled session için yeni WebSocket upgrade veya steering gönderme.
   Rematch'te eski socket/controls/sequences temizlenir; mevcut
   `PUT /api/v1/game/houseRockets/session` house body ile çağrılır.
4. Başka kullanıcı yeni aktif lobby açtıysa PUT o lobby'yi döndürür; eski session
   asla resetlenmez. Yeni session ID üzerinde join + hazır onayı yeniden gerekir.
   Eski session sonucu HTTP'den okunmaya devam eder.

`tests/houseRocketsResultIntegration_test.go` transaction rollback/retry,
duplicate completion, stale owner, takeover/finish yarışı, yayın kaybı,
yetkili iptal ve migration tekrarını doğrular. İki instance gateway testi HTTP ve
socket sonuç eşitliğini ve rematch'i doğrular. `tests/gameMatchResult_test.go`
ortak envelope ve House Rockets codec kontrollerini kapsar.
`tests/gameMatchPersistenceIntegration_test.go` House Rockets alanları olmayan
sıra tabanlı örnek payload'la ortak repository kullanımını, immutable replay'i,
metadata/payload bozulmasını ve mevcut sonuç/outbox'a dokunmayan migration tekrarını doğrular.
Bu test yeni bir oyun feature'ı veya catalog kaydı eklemez.
`go vet ./...` ve canlı Mongo/Redis
ile `go test -race ./... -count=1` kabul kontrolleridir. Mobil repository
değiştirilmedi/build alınmadı; gerçek iki iOS cihazı ve Paket 7 kapısı bekler.

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

#### Paket 6 backend teslimi — 4 Ekim 2026

Wire sözleşmesi `houseRockets.v2.1` / protocol `2` / course `1` değişmedi.
Yeni migration açılmadı; aktif oturum taraması mevcut unique `sessionId`
index'ini kullanır. Mongo owner kaydındaki opsiyonel `completionTrigger` alanı,
yetkilendirilmiş cancel niyetini runtime değişmeden önce saklar; mevcut
validator ile uyumludur. Cancellation receipt'i yine sonuç transaction'ında
üretilir; niyet kaydı tek başına başarılı cancel ACK'i değildir.

Uygulanan akış:

1. Oda sahibi başlangıç checkpoint'ini Redis'e yazar, sonra Mongo'daki
   `started` bariyerini geçirir. Çalışan maç yeniden spawn edilmez.
2. Normal hareket checkpoint'i aralıklı kaydedilir. Control generation devri,
   elenme/forfeit ve terminal öneri kritik geçiştir: önce checkpoint korunur,
   sonra ilgili grant/state yayınlanır. Yazma hatasında oda durur.
3. Yeni owner geçerli Redis lease'i ve Mongo CAS ile daha yüksek epoch alır.
   Geçerli checkpoint'ten fizik, kalan efektler, gate temasları, kamera ve
   fractional tick birikimi kurulur. DB'deki leave/üyelik değişimi ilk recovered
   state yayınından önce uygulanır. Duyurulmuş elenme geri alınmaz.
4. Eski bağlantı/generation, input sequence ve bekleyen steering taşınmaz.
   Oyuncular yeni epoch için yeniden kontrol alır. Recovery sırasında geçen
   kesinti süresi physics tick'lerine veya kalan reconnect grace'e eklenmez;
   maçın 5 dakika sınırı simülasyon süresidir.
5. Checkpoint yoksa, bozuksa veya hareket state'i çok eskiyse maç açık şekilde
   `cancelled/recoveryFailed` olur; kazanan/rank üretilmez. State tamamen
   kayıpsa süre/distance `0` ve bilinmeyen elenme alanları `null` olur; bunlar
   gerçek oynanmış skor iddiası değildir. Sahte `playing` frame'i yayınlanmaz.
6. Terminal öneri recovery yaş sınırından bağımsız korunur; kayıt retry'ı
   `endedAt`, kazanan veya sıralamayı yeniden hesaplamaz. Mongo commit'i
   olmadan kesin sonuç/leaderboard yayınlanmaz. Yetkili cancel niyeti owner
   crash'inden sonra da aynı command receipt'iyle tamamlanır.
7. Socket/komut gelmese bile tek seçilmiş recovery scanner, aktif pointer
   index'ini sayfalar; owner'ı olmayan readyWindow/countdown/running oturumları
   devralır. Boş lobby'yi sürekli ayağa kaldırmaz. İstemcisi kalmayan lobby'nin
   RAM runtime ve lease'i serbest bırakılır; DB lobby keşfedilebilir kalır ve
   sonraki bağlantı yeniden aktive eder. Terminal commit aktif pointer'ı siler;
   checkpoint temizlenir veya TTL ile düşer.

Operasyon varsayılanları `RoomManagerOptions` içindedir; yeni ürün kuralı veya
client'ın seçebildiği değer değildir:

| Ayar | Varsayılan | Anlamı |
| --- | --- | --- |
| `CheckpointInterval` | 1 saniye | Normal hareket kaydı; kritik geçiş bunu beklemez. |
| `CheckpointTTL` | 10 dakika | Geçici state saklama; kalıcı sonuç Mongo'dadır. |
| `RecoveryMaxAge` | 30 saniye | Nonterminal state daha eskiyse devam yerine iptal. Başlangıcı çok gecikmiş, hiç spawn olmamış running maç da iptal edilir. |
| `RecoveryScanInterval` / `RecoveryScanBatch` | 5 saniye / 100 | Tek seçilmiş scanner için indeksli sayfa; bütün oturumların 5 saniyede taranacağı garantisi değildir. `-1` scanner'ı kapatır. |
| `FinalizationTimeout` | 30 saniye | Bir owner'ın kayıt retry bütçesi. Aşılınca lease/RAM bırakılır, güvenli öneri sonraki owner tarafından tekrar denenir. |
| `IdleRoomTimeout` | 1 dakika | Presence'sız lobby'nin owner runtime'ını bırakma eşiği; tarama tick'i/lease temizliği ek gecikme yaratabilir. |

Redis ulaşılamıyorsa sahiplik/checkpoint doğrulanamaz: güvensiz yayın durur,
geçici erişim hatası state'in kesin kaybolduğu şeklinde yorumlanmaz. Redis
geri gelir ve state bulunamazsa tutarlı cancellation kaydedilir. Mongo
ulaşılamıyorken kalıcı sonuç/iptal veya ACK garanti edilemez; storage düzelince
scanner/finalization retry tamamlar. Per-owner retry bütçesi bu gerçeği
değiştirmez; DB kesintisini gizleyerek başarılı sonuç üretilmez.

#### Paket 6 mobil agent uygulama sırası

Backend teslimi mobil implementasyonun yapıldığı anlamına gelmez. Agent bu
doküman ve JSON fixture'larıyla local/staging üzerinde şu akışı doğrulamalı:

1. Socket kesilince online ekranı koru; sınırlı backoff ile reconnect yap.
   Bot moduna otomatik geçme. Foreground dönüşünde tam snapshot/resync al.
2. Daha yüksek `runtimeEpoch` gözlenince eski prediction/interpolation state,
   pending input'lar, ACK ve `controlGeneration` temizlenir. Yeni epoch'taki
   authoritative snapshot başlangıç state'i olur; düşük epoch mesajları atılır.
3. `phase: recovering` varsa bekleme/toparlanma durumunu göster. Bu frame
   coalescing yüzünden atlanabilir; toparlanmayı yalnız bu mesajın gelmesine
   bağlama. Aynı socket üzerinden daha yüksek epoch'la doğrudan `playing`
   gelebilir. Küçük hareket geri düzeltmesi normaldir, kusursuz failover yoktur.
4. Yeni `houseRockets.controlGranted` gelmeden steering gönderme. Yeni
   generation için input sequence `1`'den başlar; eski input'lar replay edilmez.
   Grant ile ilk snapshot'ın geliş sırasına bağımlı olma. Elenmiş oyuncu kontrol
   alamaz; spectator olarak kalır.
5. Terminal `gameSession.snapshot` / `houseRockets.result` maç sonudur. State
   kaybı iptalinde son physics frame'i gelmesi şart değildir. `cancelled` için
   kazanan çıkarma, bilinmeyen skor alanlarını gerçek skor gibi gösterme.
   Result event kaybolduysa eski session ID ile HTTP result fallback uygula;
   404 henüz commit olmadığını gösterebilir, 401/403 retry edilecek kayıt değildir.
6. Logout/ev değişiminde socket, heartbeat, input loop ve pending UI command'ları
   temizle. Yeni kullanıcı/ev eski bağlantının kontrolünü devralmaz. Rematch
   daima yeni session'dır; ready onayı yeniden gerekir.

Doğrulama kapsamı: saf runtime restore/grace/generation testleri, iki-instance
gateway reconnect/controller devri/üyelik/forfeit regresyonları, gerçek child
owner process kill, checkpoint fencing ve büyük integer sırası, Redis veri
reset'i, eksik/bozuk/eski checkpoint, kritik checkpoint yazma hatası,
socket/komut olmadan orphan cancellation, bounded finalization sonrası aynı
önerinin retry'ı, crash öncesi cancel receipt'i ve boş lobby cleanup.
Tam test komutları: `go vet ./...` ve local compose Mongo/Redis ile
`go test -race -v ./... -count=1`. Production kapasite, gerçek iOS cihazları ve
ağ gecikmesi kabulü bu testlerin yerine geçmez; Paket 7 kapsamındadır.

4 Ekim 2026 doğrulama sonucu: host `go test ./...`, `go vet ./...` ve
`git diff --check` başarılı. Canlı compose Mongo/Redis ile bütün proje
`go test -race -v ./... -count=1` başarılı (236 saniye). Son gözden geçirme
değişiklikleri ve yeni uzun-süre-sahipsiz oturum/ongoing-elimination testleri,
Paket 6 testlerinin tamamıyla ayrıca `-race -count=1` çalıştırıldı ve başarılı
(38 saniye). Gerçek child process kill testi bu iki çalışmada da geçti.
Testler sonrası yerel test container'ları durdurulur; production açılmaz.

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

#### Paket 7 backend teslimi — ölçüm ve kontrollü kabul

Durum: Backend ölçüm/admission altyapısı uygulanmıştır. Aşağıdaki yerel
ölçümler production kapasite garantisi değildir; ortak mobil/Fly kabulü hâlâ
açıktır. Production flag/katalog kapısı bu pakette açılmamıştır. Mobil repo,
wire contract, oyun kuralları ve migration numaraları değiştirilmemiştir.

**Oda kuralı ile instance kapasitesi farklıdır.** Ürün kuralı, bir ev ve bir
oyun için aynı anda yalnız bir aktif session olmasıdır. Mevcut Mongo unique
aktif-session pointer ve eşzamanlı PUT testleri bunu zaten sağlar. Diğer evler
aynı anda kendi odalarında oynayabilir; bütün sistemin toplam concurrent oda
hedefi henüz belirlenmemiştir. Ölçümdeki 1/4/8 oda, ayrı evleri temsil eder;
ürün limiti değildir.

Local/staging House Rockets açılırken şu iki environment ayarı zorunludur:

- `REALTIME_MAX_OWNED_ROOMS`: Instance'ın aynı anda sahiplenebileceği oda
  sayısı. Devam eden local başlangıçlar da rezervasyon hesabına katılır.
  Instance doluyken mevcut odaları ve başka instance'ın sahip olduğu odaların
  socket/input trafiğini taşımaya devam edebilir.
- `REALTIME_MAX_CONNECTIONS`: Instance üzerindeki bütün realtime bağlantılar
  ve devam eden upgrade rezervasyonlarının toplam sınırı. Oyuncu sayısı değil,
  socket sayısıdır; tekrar bağlantı ve birden fazla cihaz da bütçeye girer.

Değerler pozitif tamsayıdır. Feature açıkken eksik veya hatalı değer startup'ı
durdurur; ölçülmemiş bir kapasite varsayılanı eklenmemiştir. Örneğin yalnız
kontrollü **tek oda staging denemesi** için `REALTIME_MAX_OWNED_ROOMS=1`,
`REALTIME_MAX_CONNECTIONS=8` verilebilir. Sekiz dolu bağlantı varken ek cihaz
veya kısa reconnect overlap'ı da reddedilebilir; bu örnek production tavsiyesi
değildir. Feature kapalıysa verilmeyen limitler mevcut davranışı korur.

Kapasite dolduğunda HTTP upgrade `503`, mevcut `realtime.error.unavailable` error
contract'ı ve `Retry-After: 1` döner. Lease yeni alınmışsa bırakılır; reddedilen
oda için Mongo ownership claim yapılmaz. Upgrade rezervasyonu başarısız
başlangıçta serbest bırakılır; hijack gerçekleşmezse upgrade timeout'unda
sona erer. Kapanış pending rezervasyonları temizler ve devam eden oda
aktivasyonlarını da bekler. Yeni bir oyun session'ı yaratmak kapasite aşımı
için çözüm değildir.

Mobil agent: Bu 503'ü maç sonucu/elenme olarak yorumlama. Son session ID'sini
koru, kontrollü bekleme göster ve sınırlı exponential backoff + jitter ile
reconnect uygula. Kullanıcı ekrandan çıkınca retry'ı iptal et. `Retry-After`
asgari beklemedir; 401/403/terminal conflict aynı retry politikasına girmez.
Online'a otomatik bot ekleme veya kullanıcıyı habersiz bot moduna geçirme.

**Ölçümler ve anlamları:** Her instance 30 saniyede bir yapılandırılmış
`realtimeMetrics` log'u üretir. Runtime hata kanalları da boşaltılır;
`realtimeError` yalnız sınırlı kategori içerir, ham hata/Redis URL/token/oyuncu
ID'si loglanmaz. Public diagnostics HTTP endpoint'i eklenmemiştir.

- `activeRooms`, `pendingActivations`, `activeConnections`,
  `pendingConnections` ve ayarlanmış maksimumlar kapasiteyi gösterir.
- `physicsGap`: Runtime advance çağrıları arasındaki süre; 60 Hz referansı
  yaklaşık 16,67 ms'dir. `physicsStep`: Kilit beklemesi dahil advance maliyeti.
  Mevcut 250 ms bounded catch-up aşılırsa overload sayılır; fizik kuralları
  değiştirilmez ve sınırsız geçmiş tick işleme yapılmaz.
- `serverInputAge`: Backend gameplay girişinden input'un gerçekten fizikte
  uygulanmasına kadar geçen süre. Mobil RTT veya client saat ölçümü değildir.
  Coalescing nedeniyle eski steering input'ları tek tek uygulanmak zorunda
  değildir. `inputsCoalesced` ve `framesCoalesced` bunun sayacıdır.
- Checkpoint süre/başarı/hata/JSON byte'ları, başarılı socket JSON byte'ları,
  runtime fanout payload byte'ları, input JSON byte'ları, slow-consumer ve
  admission/error sayaçları tutulur. JSON byte'ları TCP/TLS/WebSocket framing
  veya Redis RESP overhead'ini içermez. Runtime fanout sayacı bütün lifecycle
  mesajlarının toplamı değildir.
- Redis `clientCommands`, go-redis üzerinden yapılan komutları sayar; Lua
  içindeki komutları veya sağlayıcının billing hesabını saymaz. `clientFailures`
  cold-start `NOSCRIPT` gibi beklenen fallback'leri de içerebilir; tek başına
  maç hatası değildir. Blocking stream okumaları `clientDuration` içinde
  olduğundan bu süre saf Redis network latency diye yorumlanmaz.
- Histogramlar sabit boyutludur. `p95UpperMilliseconds` bucket üst sınırıdır,
  kesin percentile değildir; eşzamanlı snapshot yaklaşık olabilir. Sayaçlar
  process ömrü boyunca kümülatiftir; hız için zaman aralığındaki fark alınır.
  Oyuncu/oda bazlı sınırsız metric label veya latency listesi tutulmaz.
- Log'daki `goHeapBytes` ve `goRuntimeBytes` RSS değildir. Yük testinde ayrıca
  process peak RSS ve OS user+system CPU zamanı ölçülür.

**Tekrarlanabilir yük ölçümü:** `tests/houseRocketsLoadIntegration_test.go`
iki ayrı backend test process'i başlatır; yük üreticisi bunlardan ayrıdır.
Gerçek Mongo transaction'ı, Redis lease/checkpoint/fanout, JWT-auth HTTP
aktif-oda keşfi ve WebSocket v2 kullanılır. Oyuncular iki gateway arasında
paylaştırılır; input'un bir kısmı remote owner'a yönlenir. Maç fixture'ı
running hazırlanır; ready/countdown beklemesi yalnız bu performans testinde
kısaltılır. Normal lobby/ready/finalization akışları ayrı regresyon testlerinde
doğrulanır. Synthetic controller'lar yalnız test client'ıdır, online bot modu
değildir.

Her worker `GOMAXPROCS=1`, `GOMEMLIMIT=200MiB` ile çalışır. Bunlar hard CPU
quota/RSS limiti veya Fly shared CPU performans modeli değildir. Worker,
API'nin bütün diğer modüllerinin startup/HTTP yükünü içeren production binary
değil, gerçek realtime bileşenlerini kullanan test sunucusudur. Referans ortam
yerel OrbStack compose, Linux/arm64, yakın Mongo/Redis ve loopback gateway'dir;
Fly region/Upstash WAN latency ölçümü yapılmamıştır. Her senaryoda bütün
oyuncuların grant + playing snapshot alması beklenir, ardından 3 saniye boyunca
kişi başına en çok mevcut `maximumInputRateHz=20` steering gönderilir. Bu kısa
matris uzun süreli soak veya beş dakikalık maç kabulü yerine geçmez.

Opt-in komut (test Mongo/Redis ortamında):

```sh
docker compose --profile test run --rm \
  -e HOUSEFLOW_REALTIME_LOAD=true integrationTests \
  go test -v ./tests -run '^TestHouseRocketsLoadMatrix$' -count=1
```

`HOUSEFLOW_LOAD_DURATION` isteğe bağlı `2s`–`20s` ölçüm penceresidir; uzun
pencerede synthetic controller'ın doğal elenmesi de testi başarısız kılabilir.
3 saniyelik varsayılanın artırılması tek başına production soak testi değildir.
Her alt test disposable DB kullanır; Redis test DB'si resetlenir. Başka
entegrasyon testleriyle paralel çalıştırılmaz ve production URL verilmez.
İki worker graceful kapatılır; açık oda/socket/aktivasyon/upgrade kalmaması
kontrol edilir. Normal CI'da 4 oda × 2 oyunculu kısa regresyon otomatik çalışır;
performans matrisi opt-in'dir ve `-race` olmadan ölçülür. CI compose regresyonu
ise artık bütün Go paketlerini `-race` ile doğrular.

4 Ekim 2026 ilk yerel matris sonucu: **9/9 senaryo başarılı** (67,2 saniye
toplam). Tablo CPU için daha yoğun worker'ı; RSS için iki worker'ın daha yüksek
peak değerini; Redis/socket hızları için iki worker'ın toplamını gösterir.
KB/MB yerine byte/s korunmuştur; süre penceresi her satırda 3 saniyedir.

| Ayrı ev/oda | Oyuncu/oda | Toplam socket | En yüksek worker CPU, 1 core % | En yüksek peak RSS, MiB | Redis client komut/s, toplam | Socket JSON byte/s, toplam | En yüksek client input→ACK, ms |
| --- | --- | --- | --- | --- | --- | --- | --- |
| 1 | 2 | 2 | 3,11 | 24,7 | 64,7 | 68.356 | 34,1 |
| 1 | 4 | 4 | 4,19 | 24,8 | 108,3 | 205.111 | 50,4 |
| 1 | 8 | 8 | 6,01 | 25,7 | 191,0 | 656.104 | 55,0 |
| 4 | 2 | 8 | 6,45 | 25,6 | 246,7 | 272.345 | 54,2 |
| 4 | 4 | 16 | 9,54 | 26,0 | 410,0 | 820.340 | 53,2 |
| 4 | 8 | 32 | 13,56 | 27,6 | 743,3 | 2.623.693 | 55,3 |
| 8 | 2 | 16 | 10,27 | 27,7 | 503,7 | 546.301 | 54,0 |
| 8 | 4 | 32 | 14,43 | 28,0 | 822,3 | 1.642.799 | 51,3 |
| 8 | 8 | 64 | 18,70 | 30,2 | 1.446,7 | 5.248.259 | 53,0 |

Bütün satırlarda runtime overload, checkpoint failure, slow consumer,
beklenmeyen admission rejection ve runtime/gateway error sıfırdır. Physics
gap p95 bucket üst sınırı 20 ms; en yüksek tek gap 62,6 ms'dir. Ölçülen client
input→ACK, test client'ın gönderiminden kendi processed-input sequence'ini
snapshot'ta görmesine kadardır; gerçek mobil RTT değildir. ACK örnekleri
coalescing yüzünden bütün gönderimler için ayrı ayrı oluşmaz.

Bu ölçümde maliyet açısından dikkat çeken nokta sekiz kişilik oda fanout'udur:
oda başına yaklaşık 656 bin JSON byte/s socket çıkışı vardır. Sekiz oda için
5,25 milyon byte/s toplam görülmüştür. Bunlar sürekli trafik/fatura tahmini
değildir; region/provider overhead'i ve normal maç evreleri ölçülmemiştir.
Production hedefi belirlendiğinde bu hızlar ile Redis sağlayıcı ölçümleri için
bütçe çıkarılmalıdır. Contract değiştiren binary/delta/compression protokolü
bu teslimde varsayılmamıştır; gerekli olup olmadığı ölçülerek kararlaştırılır.

Çok odalı ölçüm ayrıca bir transport-lifetime hatasını ortaya çıkarmıştır:
Fiber HTTP `Params` değeri request buffer'ını ödünç alıyordu. Gateway bu oda
kimliğini uzun ömürlü runtime'a kopyalamadan verdiğinden request reuse önceki
lease kimliğini değiştirebiliyordu. Kalıcı string kopyasıyla düzeltilmiştir;
4 odalı regresyon, oda ilerlemesini ve kapanışta haritanın temizlenmesini
kontrol eder. Public WebSocket/Mongo sözleşmesi değişmemiştir.

**Production açma kapısı — henüz tamamlanmamış işler:**

1. Farklı evlerden hedef toplam concurrent oda/socket sayısını ve maliyet
   bütçesini belirle. Ev/oyun başına tek oda kuralı bunu cevaplamaz.
2. Aynı ölçümü gerçek Fly instance/region ve bağlı Redis/Mongo ile staging'de
   tekrar et; normal HTTP yükünü, ownership dengesizliğini ve instance kaybında
   kalan instance'ın kapasitesini dahil et. Admission değerleri bu sonuç ve
   headroom'a göre seçilir; yerelde 8 oda geçti diye production limiti 8 olmaz.
3. Hedef süre boyunca peak/steady trafik, CPU/RSS/GC, Redis ve egress bütçesi
   için soak doğrulaması yap. Tolerans/bütçeler yayından önce açıkça onaylanır.
4. Mobil agent iki gerçek hesap/cihazla 100/200/400 ms RTT, jitter, kısa kesinti,
   background/reconnect, 30/60/120 FPS, orientation ve yerel bot regresyonunu
   raporlar. Backend matrisinin geçmesi bunları geçmiş saydırmaz.
5. Ortak sonuçlar kabul edildikten sonra production gate ayrı ve bilinçli
   geliştirmeyle açılır. Şu anki değişiklik production'a otomatik online açmaz.

Backend doğrulaması: Host `go test ./...`, `go vet ./...` ve `git diff --check`
başarılı. Compose Mongo/Redis üzerinde bütün proje
`go test -race -v ./... -count=1` başarılı (243 saniye); migration/result/
recovery/child-process-kill regresyonları da bu çalışmada geçti. Son kapasite
değişiklikleri ve eklenen pending upgrade expiry/shutdown testi ayrıca gerçek
compose ortamında `-race -count=1` ile geçti (28 saniye). 12 paralel upgrade'de
3 socket limiti için 3 kabul/9 HTTP 503; limit doluyken remote owner'a ulaşma;
yarım kalan upgrade rezervasyonunun timeout'ta ve shutdown'da temizlenmesi
doğrulandı. Testlerden sonra yalnız yerel test container'ları durdurulur;
Mongo volume silinmez, deploy/commit yapılmaz.

### 8.8. Mobil agent ne zaman başlamalı?

Bu tablo bağımlılıkları özetler; uygulama sırası bölüm 0.4'teki M0–M6'dır.
Backend kapıları artık mevcuttur; mobil temel tamamlanmış olarak bildirilmiştir.

| Mobil iş grubu | Başlayabileceği zaman | Canlı doğrulama bağımlılığı |
| --- | --- | --- |
| İki mod seçimi, yerel bot, scene/simulation ayrımı, dependency factory | Tamamlandığı bildirildi; M0'da güncel kod/test kontrolü | Backend gerektirmez; korunur. |
| Online wire DTO, string ID/isim mapping, HTTP/socket transport, mock server | Şimdi, M1 | Paket 4 backend mevcut; canlı ortam gerekir. |
| Gerçek ev üyeleriyle join/ready/countdown | M1 sonrası, M2 | Paket 4 mevcut; aynı evden iki hesap gerekir. |
| Input coalescing, ACK/epoch, interpolation ve prediction/reconciliation | M2 sonrası, M3; fixture testleri M1'de hazırlanabilir | Paket 2–4 mevcut; canlı kontrol hissi ayrıca ölçülür. |
| Sonuç ekranı, HTTP result fallback, rematch | M3 sonrası, M4 | Paket 5 mevcut. |
| Reconnect/background/recovery/logout/ev değişimi | M5; temel cancellation/context isolation M1'den itibaren | Paket 6 mevcut; owner kaybı backend ile koordineli test edilir. |
| İki cihazlı nihai kabul ve gecikme hissi | M6; önceki fazlarda da canlı smoke yapılır | Paket 7 ortak yayın kabulü henüz açık. |

Her backend tesliminde agent aynı dokümana package status, test özeti,
contract/fixture revision ve doğrulanabilen yolları işler. Local/staging bağlantı
adresleri environment ayarı olarak paylaşılır; token/secret bu dosyaya yazılmaz.
Mobil agent hazır olmayan mesajı/backend davranışını mock olarak etiketler;
mock sonucu gerçek entegrasyon başarısı diye raporlamaz.

### 8.9. Backend'in ne zaman tamamlanmış sayılacağı

- **Paket 1 sonrası:** Mobil online geliştirmesi için sabit sözleşme var.
- **Paket 4 sonrası:** Gerçek HTTP/socket backend testleri geçti; mobil cihazlar
  aynı maçta hareket entegrasyonuna başlayabilir. İki fiziksel cihaz kabulü ayrı iştir.
- **Paket 5 sonrası:** Kontrollü ortamda lobi → oynanış → kalıcı sonuç → rematch
  akışı tamamlanabilir.
- **Paket 6 sonrası:** Reconnect/instance kaybı/iptal senaryoları tutarlı.
- **Paket 7 ortak kabulü sonrası:** Gerçek Fly/mobil hedefleri doğrulanınca
  bilinçli production açılışına hazır. Yalnız backend yerel testleri yeterli değildir.

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
| Aynı kullanıcı, iki cihaz | Yalnız güncel grant/generation kontrol eder; control bind devrinden önceki connection/input'lar reddedilir, istemciler rebind yarışına girmez. |
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

Backend testleri `tests/gameSessionDomain_test.go`,
`tests/realtimeRoomRuntimeIntegration_test.go`,
`tests/realtimeGatewayIntegration_test.go` ve persistence/catalog testleri
üzerine genişletildi; Paket 5–7 result/recovery/admission/yük testleri de mevcut.
Mobil agent bu backend testlerini yeniden implement etmez. Yeni Go test dosyaları `houseRockets..._test.go` gibi
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
- Mobil faz tesliminde faz adı, değişen dosyalar, wire/fixture revizyonu,
  test komutu/sonucu, mock/canlı ayrımı, açık kabul kapıları ve sıradaki iş
  raporlanır. Kullanıcının "tamamlandı" beyanı ve salt-okunur inceleme,
  çalıştırılmış mobil test gibi sunulmaz. Backend erişimi yoksa kanonik belgede
  değişiklik yapılmış gibi iddia edilmez; durum özeti kullanıcıya verilir.
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

## 11. Önceki paketlerin doğrulama kayıtları — tarihsel

Bu bölüm o teslim anındaki test/ortam kapsamını korur; mobil başlangıç görevi
değildir. Paket 5–7'nin daha yeni doğrulamaları bölüm 8'deki teslim notlarındadır.
Güncel ilerleme tablosu yalnız bölüm 0.1'de tutulur; aşağıdaki eski live-gateway
ve eksik paket ifadeleri bugünkü backend erişim kapısını değiştirmez.

### Paket 1 doğrulama kaydı

`go test ./...` ve
`go test -race ./tests -run '^TestHouseRockets' -count=1` başarılı.
V2 decoder ayrıca 5 s fuzz testinde 46.272 girdiyle beklenmeyen başarısızlık/panic üretmeden
doğrulandı (`-parallel=2`). Bu kapasite/yük testi değildir.
Mongo/Redis environment'ı olmayan integration testleri kendi kurallarıyla
skip edildi; bu teslimde canlı Mongo/Redis/socket veya Docker compose testi
yapılmadı. Swift'in mevcut üç Foundation/CoreGraphics kaynak dosyası bağımsız
bir referans executable ile çalıştırıldı; iOS uygulaması build edilmedi ve
mobil kaynak değiştirilmedi. Test için server/container açılmadı.

Paket 1 teslim anında mobil yalnız fixture'larla wire DTO/mock geliştirebilirdi;
canlı gateway kapısı o aşamada geçilmemişti. Bugün local/staging canlı gateway
mevcuttur; yalnız production gate kapalıdır (bölüm 0.3).

### Paket 2 doğrulama kaydı

Go contract/engine testleri, tüm `go test ./...` ve House
Rockets race kontrolü başarılı. Fizik sabitleri, 2/4/8 spawn, altı kısa hareket,
üç navigation/effect senaryosu ve gerçek convex yüzeyler Swift fixture'larıyla
1e-9 toleransında eşleşti. 30/60/120/144 FPS batching aynı state'i üretiyor;
expiry, son-tick survivor/draw önceliği, effect replacement/expiry, terminal
state değişmezliği, state/result copy isolation, corrupt restore ve bounded
geometry/contact geçmişi testleri mevcut.
State restore fuzz testi 5 s / 2 worker'da 10.231 girdiyi başarıyla çalıştırdı.
Contract/engine unit testleriyle House Rockets Go package statement coverage'ı
%97,4 ölçüldü; bu tüm backend veya canlı entegrasyon coverage'ı değildir.
Sekiz sabit konumlu oyuncuyla iki tick'lik mikro benchmark Apple M2'de yaklaşık
9,5 µs/op, 0 B/op ve 0 allocs/op ölçtü; bu network/Redis yüklü oda kapasitesi
veya production garantisi değildir. Gerçek çoklu instance/yük kabulü Paket 7'dir.
Mongo/Redis environment'ı olmadığı için mevcut integration testleri skip edildi;
bu pakette canlı socket/DB/compose testi yapılmadı, server/container açılmadı.

### Paket 3 doğrulama kaydı

Host üzerinde `go test ./...`, House Rockets race testleri
ve `go vet ./...` başarılı. Docker Compose'un izole test ortamında gerçek Mongo
replica set ve Redis ile tüm `go test -race ./tests -count=1` başarılı (115 s).
Son adapter/ownership mapping düzenlemesinden sonra runtime/owner/gameplay bus
ve mevcut room runtime testleri tekrar race detector ile geçti (19 s).
İki gerçek RoomManager/RedisCoordinator instance'ı ile local/remote steering,
grant/ACK, stale epoch ve duplicate input, lease kaybında durma, spawn restart
bariyeri, Mongo CAS yarışı, Redis fencing counter reset ve yavaş fanout sırasında
physics/lease yenileme doğrulandı. Subscriber yokken aktarım unavailable döndü;
abonelikten önce gönderilen input sonradan replay edilmedi. 30/60/120/144 frame
gruplamasında 120 fizik tick'i ve 20 snapshot cadence, bounded catch-up/queue,
control timeout/grace/reconnect ve concurrent input testleri geçti.
Bu canlı mobil socket entegrasyonu veya production yük testi değildir; bu
kapılar Paket 4/7'de kalır. iOS repo değiştirilmedi ve build edilmedi.
Test için başlatılan Mongo/Redis/init container'ları durduruldu; çalışan container
kalmadığı kontrol edildi. Volume'lar korundu, kod commit edilmedi.

### Paket 4 doğrulama kaydı

Host `go test ./...`, `go vet ./...` ve `git diff --check`
başarılı. Compose üzerinde gerçek Mongo replica set + Redis ile tüm proje
`go test -race ./... -count=1` geçti. Son düzenlemelerden sonra game/session,
runtime ve v1/v2 gateway regresyonları ayrıca race detector ile tekrar geçti;
resync yarış düzeltmesi ve exclusion testi de son gateway koşusunda doğrulandı.
İki ayrı Service/RedisCoordinator instance'ı ve gerçek HTTP/WebSocket bağlantıları
ile aynı oturumun HTTP keşfi, welcome/initial ordering, join/ready/countdown,
gerçek frozen isimler, 20 Hz steer ve karşı oyuncu ACK/konumları, private generation,
remote rejection correlation, resync, forfeit/finalizing, membership revocation,
seyirci ve controller takeover doğrulandı. Upgrade 401/403/400, devre dışı gateway,
token expiry ve transport pong sürerken application heartbeat'i kesilen ready
oyuncunun çıkarılması testleri geçti. Mobil repo değiştirilmedi/build edilmedi;
iki fiziksel iOS cihazı veya production yük/failover kabulü yapılmadı.
Test için açılan Mongo/Redis/init container'ları durduruldu; çalışan container
kalmadığı doğrulandı. Volume'lar korundu, deploy ve commit yapılmadı.
