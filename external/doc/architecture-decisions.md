# HouseFlow Mimari Karar Taslağı

Bu belge, oyun modülü geliştirilmeden önce HouseFlow'un çok kullanıcılı kullanıma
hazırlanması için önerilen mimari kararları ve uygulama sırasını tanımlar.
Kararlar kod değişikliği değildir. `Kabul` durumundaki maddeler ürün yönünden
zaten netleşen konuları, `Öneri` durumundakiler ise onay bekleyen teknik yönü
gösterir.

## 1. Mevcut durum

Uygulama Go ve Fiber ile çalışan, MongoDB kullanan modüler bir monolittir. HTTP
controller'ları doğrudan service katmanına, service'ler de ortak generic MongoDB
repository'sine bağlıdır.

Concurrency bakımından öne çıkan riskler:

- Generic `Update`, document'i okuyup entity'nin neredeyse tamamını `$set` ile
  yeniden yazar. Eşzamanlı iki değişiklik birbirini ezebilir.
- Hane oluşturma ve haneye katılma, hem `House` hem `User` document'ini
  değiştirir; bu yazmalar tek atomik işlem değildir. İkinci yazma başarısız
  olduğunda iki kayıt farklı üyelik bilgisi taşıyabilir.
- Hane kapasitesi ve mükerrer üyelik kontrolleri, yazmadan önce uygulama
  belleğinde yapılır. Aynı anda gelen katılım istekleri kontrolü birlikte
  geçebilir.
- Görev durumu, durum geçmişi ve değerlendirme oyu birden fazla yazmayla
  ilerler. Aradaki bir hata kısmi sonuç bırakabilir.
- Toplu görev güncellemesi gerçek anlamda atomik değildir; listenin ortasında
  hata olursa önceki görevler güncellenmiş kalabilir.
- Bazı sorgular N+1 biçiminde çalışır ve bazı okuma hatalarını atlayarak eksik
  veriyi başarılı cevap olarak döndürebilir.
- Repository çağrıları context'i çağırandan zorunlu olarak alır; HTTP isteklerinin
  deadline/cancel bilgisi controller ve service üzerinden veri tabanına taşınır.
- Mevcut Docker Compose MongoDB'yi standalone çalıştırır. Çok-document
  transaction kullanılacaksa replica set topolojisine geçilmelidir.

Bu bulgular WebSocket'ten bağımsızdır. Önce normal HTTP işlemlerindeki veri
tutarlılığı çözülmelidir.

## ADR-001 — Ana iletişim yöntemi HTTP olacak

**Durum:** Kabul

Kullanıcı, hane, görev, profil, duyuru ve benzeri normal uygulama işlemleri HTTP
üzerinden devam eder. WebSocket bütün API'nin yerine geçmez.

WebSocket yalnızca düşük gecikmeli iki yönlü iletişimin gerçekten gerektiği oyun
oturumları ve gerekirse oyun presence bilgisi için kullanılacaktır. Oyun oluşturma,
oyun listesi ve geçmiş oyunları görüntüleme gibi işlemler yine HTTP olabilir.

**Sonuç:** Mevcut API sözleşmeleri korunur; gerçek zamanlı katman ayrı bir giriş
noktası ve ayrı yaşam döngüsü olarak tasarlanabilir.

## ADR-002 — Kademeli, uygulama içi command/query ayrımı

**Durum:** Kabul — CQRS Faz 0-6 tamamlandı

İlk aşamada ayrı servisler, ayrı read database veya event sourcing içeren tam
ölçekli CQRS kurulmayacaktır. Aynı process içinde use-case bazlı ayrım yapılacaktır:

```text
HTTP Controller
    ├── Command Handler → state değiştirir
    └── Query Handler   → veri okur
```

Örnek command'lar:

- `CreateHouse`
- `JoinHouse`
- `CreateChore`
- `AdvanceChoreStatus`
- `ReviewChore`

Örnek query'ler:

- `GetHouseDetails`
- `GetHouseMembers`
- `GetChoreDetails`

Controller yalnızca taşıma katmanı sorumluluklarını üstlenir: request parse,
kimlik bilgisi, validation çağrısı ve HTTP response eşlemesi. İş kuralları ve
transaction sınırı handler'da bulunur. Handler'lar uygulamaya ait context-first,
process-içi mediator üzerinden çağrılır. Bu mediator harici message broker,
dayanıklı queue veya event bus değildir.

Handler sözleşmeleri ve process-içi mediator `internal/infrastructure/cqrs`
altında tanımlanır. Her handler
`Handle(context.Context, Request) (Result, error)` imzasını kullanır. Command ve
query modelleri Fiber gibi HTTP taşıma tiplerine bağımlı olmaz. Application
katmanının controller, Fiber veya eski service katmanına bağımlı hale gelmesi
mimari testlerle engellenir.

Repository sözleşmesi `internal/data/database/abstract/dbRepository.go` altında,
MongoDB implementasyonu ise `internal/data/database/dbContext.go` altında tutulur.
Application handler ve policy'leri yalnız repository interface'ine bağımlıdır;
concrete `DbContext` yalnız composition root ve entegrasyon test fixture'larında
oluşturulur.

CQRS geçişi modül bazında şu sırayla yürütülür:

1. House pilotu.
2. Chore.
3. User ve Image Asset.
4. Auth.
5. Localization endpoint'leri.
6. Eski business service katmanının kaldırılması ve tam sözleşme testi.

Health endpoint'i, middleware, migration, notification adaptörü ve localization
cache yaşam döngüsü application use-case'i olmadıkları için CQRS handler'ına
dönüştürülmez.

**Sonuç:** Oyun modülü için temiz bir uygulama sınırı oluşur fakat operasyonel
karmaşıklık artmaz.

## ADR-003 — Concurrency stratejisi katmanlı olacak

**Durum:** Kabul — uygulandı

Her probleme transaction uygulanmayacaktır. Aşağıdaki sıra kullanılacaktır:

1. Tek document değişiminde MongoDB'nin atomik update operatörleri kullanılacak.
2. İş kuralı update filtresine taşınacak. Örneğin kapasite, mevcut status ve
   beklenen version yazma anında tekrar kontrol edilecek.
3. Tekrarlanan değerler için `$addToSet`, sayaçlar için `$inc`, dar alan
   değişimleri için `$set` kullanılacak; document'in tamamı yazılmayacak.
4. Birden fazla document tek bir iş kuralı olarak birlikte değişmek zorundaysa
   transaction kullanılacak.
5. Aynı entity üzerinde yarışabilecek durum geçişlerinde `version` üzerinden
   optimistic concurrency uygulanacak.
6. İstemcinin tekrar gönderebileceği kritik command'larda `commandId` ile
   idempotency değerlendirilecek.

Örnek görev geçişi filtresi:

```text
_id = choreId AND status = Progress AND version = 7
```

Update hiçbir document eşleştirmezse işlem eski veriyle yapılmış kabul edilir ve
conflict cevabı döner. Sunucu sessizce son yazanı kazanan yapıya düşmez.

**Transaction adayları:**

- `House.memberIds` ve `User.houseIds` birlikte değişiyorsa hane oluşturma/katılma.
- Görev state'i ile zorunlu status history birlikte yazılıyorsa durum geçişi.
- Değerlendirme oyu ile görev sonucunun birlikte kesinleşmesi gerekiyorsa review.

**Sonuç:** Concurrency yalnız oyun katmanında değil, mevcut HTTP use-case'lerinde
de ölçülebilir ve test edilebilir hale gelir.

## ADR-004 — MongoDB transaction öncesinde replica set kararı

**Durum:** Kabul — uygulandı

Çok-document transaction kullanılacaksa development, test ve production
ortamları replica set veya desteklenen sharded cluster olmalıdır. Standalone
MongoDB için sessizce transactionsız çalışma seçeneği eklenmemelidir; bu seçenek
ortamlar arasında farklı veri tutarlılığı üretir.

Tek düğümlü replica set local development için yeterlidir; yüksek erişilebilirlik
sağlamaz. Production topolojisi uygulamadan önce ayrıca doğrulanmalıdır.

MongoDB topolojisinin deployment konfigürasyonunda sabit ve doğrulanmış olması
beklenir. Uygulama her başlangıçta ayrıca topology/capability sorgusu çalıştırmaz;
yanlış yapılandırılmış bir ortam transaction kullanılmaya çalışıldığında hata verir.

**Karar kapısı:** Replica set kullanılamayacaksa çift yönlü üyelik tutmak yerine
tek bir kaydı source of truth yapmak gibi veri modeli değişiklikleri
değerlendirilmelidir.

## ADR-005 — Redis şimdilik eklenmeyecek

**Durum:** Ertelendi

Tek API/WebSocket instance ile geliştirilen ilk oyun MVP'sinde bağlantı ve oda
kayıtları process memory'de tutulabilir. Kalıcı oyun state'i yalnız bellekte
tutulmamalıdır; oyunun gereksinimine göre MongoDB'de saklanmalıdır.

Redis aşağıdakilerden biri gerçekleştiğinde yeniden değerlendirilir:

- Birden fazla WebSocket instance çalıştırılması.
- Aynı oyun odasındaki kullanıcıların farklı instance'lara bağlanması.
- Instance'lar arasında pub/sub, presence veya kısa ömürlü koordinasyon ihtiyacı.
- Ölçümler MongoDB okumalarının gerçekten cache gerektirdiğini göstermesi.

Redis cache olarak eklenirse invalidation ve source-of-truth kuralları ayrıca
belgelenmelidir. Redis kalıcı ana oyun state'inin varsayılan sahibi olmayacaktır.

## ADR-006 — RabbitMQ şimdilik eklenmeyecek

**Durum:** Ertelendi

RabbitMQ, command/query ayrımının veya WebSocket'in ön koşulu değildir. Aşağıdaki
dayanıklı asenkron işler oluştuğunda değerlendirilir:

- Push notification veya e-posta gönderimi.
- Oyun sonu ödül/puan hesaplama.
- Retry gerektiren arka plan işlemleri.
- Bir domain olayını birden fazla bağımsız consumer'ın işlemesi.
- Uygulama kapansa bile kaybolmaması gereken görevler.

RabbitMQ eklendiğinde database write ile broker publish arasında dual-write
yapılmayacaktır. State değişimiyle aynı transaction içinde outbox kaydı yazılır;
ayrı worker outbox mesajını broker'a yayınlar. Consumer'lar en az bir kez teslim
olasılığına karşı idempotent tasarlanır.

## ADR-007 — Kafka seçilmeyecek

**Durum:** Öneri

Mevcut ihtiyaçlar yüksek hacimli, tekrar oynatılabilir bir event stream,
uzun süreli event saklama veya geniş analytics pipeline gerektirmemektedir.
Bu nedenle Kafka'nın operasyonel maliyeti şu aşamada karşılık bulmaz.

İleride event replay, yüksek throughput ve çok sayıda bağımsız stream consumer
somut ihtiyaç haline gelirse karar yeniden açılır. Dayanıklı iş kuyruğu ihtiyacı
önce oluşursa RabbitMQ daha uygun adaydır.

## ADR-008 — Oyun protokolü altyapıdan önce tasarlanacak

**Durum:** Öneri

WebSocket endpoint'i yazılmadan önce ilk oyunun kuralları belirlenmelidir:

- Sıra tabanlı mı, sürekli gerçek zamanlı mı?
- Oda başına oyuncu sayısı ve oyun süresi nedir?
- Bağlantı kopması, yeniden bağlanma ve oyuncunun terk etmesi nasıl ele alınır?
- State kalıcı mı; biten oyun ne kadar süre saklanır?
- Zamanlayıcılar sunucuda mı çalışır?
- Aynı oyuncının iki cihazdan bağlanmasına izin verilir mi?

Sunucu authoritative olacaktır. İstemci yeni state göndermek yerine niyetini
bildiren command gönderir. Asgari mesaj zarfı:

```json
{
  "type": "submit_action",
  "gameId": "...",
  "commandId": "...",
  "expectedVersion": 7,
  "payload": {}
}
```

Sunucu üyelik, sıra, oyun durumu ve version kontrolünden sonra state'i değiştirir.
Kabul edilen değişiklik yeni version ile odadaki bağlantılara yayınlanır. Yeniden
bağlanan istemci yalnız event geçmişine güvenmez; güncel snapshot alır.

## Önerilen uygulama sırası

### Faz 0 — Kararların onaylanması

- Bu belgedeki `Öneri` kararlarını kabul et, değiştir veya reddet.
- İlk production dağıtımının tek instance/çoklu instance hedefini belirle.
- MongoDB production topolojisini doğrula.

**Çıkış kriteri:** Veri tutarlılığı ve altyapı sınırları üzerinde ortak karar.

### Faz 1 — Concurrency temeli

- Request context'ini repository'ye kadar taşıyacak portları tanımla.
- Dar atomik update, koşullu update ve transaction yeteneklerini oluştur.
- Standart application error ve conflict cevaplarını belirle.
- Unique/compound index ihtiyaçlarını gözden geçir.
- Race senaryoları için integration test altyapısını hazırla.

**Çıkış kriteri:** İki eşzamanlı isteğin sonucu otomatik testle doğrulanabiliyor.

### Faz 2 — Pilot use-case'ler

Önce en yüksek riskli iki akış taşınır:

1. `JoinHouse`: kapasite, mükerrer üyelik ve House/User tutarlılığı.
2. `AdvanceChoreStatus` / `ReviewChore`: beklenen status/version, oy ve history
   tutarlılığı.

Bu pilotlardan sonra aynı pattern diğer write use-case'lerine kademeli uygulanır.
Tüm projeyi tek seferde yeniden yazma hedeflenmez.

**Çıkış kriteri:** Yük altında kapasite aşımı, kayıp update, mükerrer oy ve kısmi
yazma oluşmadığını gösteren testler.

### Faz 3 — Query iyileştirmeleri

- House details içindeki N+1 okumaları batch query veya aggregation'a dönüştür.
- Okuma hatalarını eksik başarılı response olarak gizleme.
- Büyük listeler için pagination sınırlarını belirle.
- Gerekiyorsa oyunlara özel read model tasarla; normal query'leri ayırma.

**Çıkış kriteri:** Sorgu sayısı öngörülebilir, hata davranışı açık ve response
sözleşmesi test altında.

### Faz 4 — İlk oyun tasarımı

- En basit temsilci oyunu seç.
- State machine, command'lar, event'ler ve hata durumlarını yaz.
- Persistence, reconnect ve idempotency davranışını kararlaştır.
- HTTP ile WebSocket sorumluluklarını ayır.

**Çıkış kriteri:** Teknolojiden bağımsız oyun protokolü ve kabul senaryoları.

### Faz 5 — Tek instance WebSocket MVP

- JWT ile bağlantı doğrulama.
- Odaya katılma/ayrılma ve yetki kontrolü.
- Ping/pong, idle timeout, mesaj boyutu ve rate limit.
- Slow consumer için sınırlı gönderim kuyruğu ve bağlantı kapatma politikası.
- Snapshot ile reconnect.
- Oyun command'larını mevcut command handler'lara yönlendirme.

**Çıkış kriteri:** İki veya daha fazla istemci aynı oyunu oynayabiliyor; kopan
istemci güncel state ile dönebiliyor; sunucu geçersiz/eski hamleyi reddediyor.

### Faz 6 — Ölçüme göre ölçekleme

- Birden fazla instance gerektiğinde Redis tabanlı cross-instance yayın/presence.
- Dayanıklı asenkron işler oluştuğunda outbox + RabbitMQ.
- Ancak gerçek stream gereksinimi oluşursa Kafka değerlendirmesi.

**Çıkış kriteri:** Yeni altyapı varsayımla değil, gözlenen kapasite veya
dayanıklılık ihtiyacıyla gerekçelendirilmiş olur.

## Zorunlu test senaryoları

- Aynı haneye eşzamanlı katılımlar kapasiteyi aşamaz.
- Aynı kullanıcı iki kere üye veya iki kere oy sahibi olamaz.
- İkinci database yazması başarısızsa ilk yazma kalıcı olmaz.
- İki kullanıcı aynı görev version'ını değiştirmeye çalıştığında yalnız biri
  başarılı olur.
- Retry edilen aynı `commandId` state'i ikinci kez değiştirmez.
- Toplu command ya bütünüyle uygulanır ya da API açıkça kısmi sonuç sözleşmesi
  sunar; davranış belirsiz bırakılmaz.
- WebSocket reconnect sonrası snapshot ve version sunucuyla eşleşir.
- Yavaş veya mesaj göndermeyi bırakan istemci diğer oda üyelerini bloke etmez.

## Şimdilik kapsam dışı

- Microservice'e bölünme.
- Ayrı command ve query database'leri.
- Event sourcing.
- Kafka cluster.
- Genel amaçlı distributed lock altyapısı.
- Bütün HTTP trafiğini WebSocket'e taşıma.
- Oyun kuralları belli olmadan genel bir game engine geliştirme.

## Karar özeti

1. Uygulama içi command/query ayrımı: kabul edildi; CQRS Faz 0 ve House pilotu
   uygulandı, sıradaki çalışma paketi Chore modülüdür.
2. Atomik koşullu update, transaction ve version: kabul edildi ve uygulandı.
3. MongoDB replica set zorunluluğu: kabul edildi ve uygulandı.
4. Redis ve RabbitMQ'yu ihtiyaç tetiklenene kadar erteleme: karar bekliyor.
5. Kafka'yı mevcut yol haritasına almama: karar bekliyor.
6. WebSocket'ten önce temsilci oyun protokolü: karar bekliyor.

## Uygulama durumu — 9 Eylül 2026

Concurrency temeli uygulanmıştır:

- Hane oluşturma/katılma ve çift yönlü üyelik transaction içindedir.
- Hane kapasitesi ve mükerrer üyelik yazma filtresinde korunur.
- Görev oluşturma, toplu durum geçişi ve review akışı transaction içindedir.
- Görevlerde `version` tabanlı optimistic concurrency kullanılır.
- Eşzamanlı review oyları aynı görev document'i üzerinden sıralanır.
- Login hata sayacı atomik artar ve hesap kilidi kayıp update üretmez.
- Profil değişiklik aralığı ve history kaydı transaction ile korunur.
- Duyuruların 24 saat kuralı atomik bir guard kaydıyla korunur.
- Image asset `publicId` için unique index eklenmiştir.
- Rate limiter aynı process içindeki eşzamanlı istekleri tek sayaçta toplar ve
  downstream handler çalışırken kilit tutmaz.
- Hane detayları tek snapshot içinde batch olarak okunur.
- Repository API'si context-first hale getirilmiştir; context üretmeyen eski CRUD
  metotları kaldırılmıştır.
- Concurrency ve geçici altyapı hataları standart `409`, `429` ve `503` HTTP
  cevaplarına dönüştürülür.

CQRS Faz 0 kapsamında ortak command/query handler sözleşmeleri ve application
katmanı bağımlılık testleri eklenmiştir. House pilotunda `CreateHouse`,
`JoinHouse` ve `CreateAnnouncement` command handler'lara; `GetHouseDetails` ise
query handler'a taşınmıştır. Process-içi mediator oluşturulmuş, House handler'ları
başlangıçta fail-fast kaydedilmiş ve House controller tek `Sender` sözleşmesine
indirilmiştir. Eski `HouseService` kaldırılmıştır. Sıradaki CQRS çalışma paketi
Chore modülüdür. WebSocket, Redis ve RabbitMQ eklenmemiştir. İstemci retry'larını
aynı işlem olarak tanıyacak genel `commandId` sözleşmesi de henüz yoktur; bu
sözleşme ilk oyun command'ları tasarlanırken ele alınacaktır.

## ADR-009 — Multi-instance coordination oyun domain'inden önce kurulacak

**Durum:** Kabul edildi — 19 Eylül 2026

ADR-008'deki tek-instance WebSocket MVP sırası, ürünün ilk sürümden itibaren
birden fazla API instance'ında çalışması hedeflendiği için değiştirilmiştir.
Bu karar oyun kurallarını erkenden genelleştiren bir game engine oluşturmaz;
yalnız instance'lar arasında ortak olacak teknik koordinasyon sınırını kurar.

Redis yalnız kısa ömürlü coordination verisi için kullanılır:

- instance heartbeat,
- TTL'li room owner lease ve monoton fencing token,
- connection presence,
- instance'a yönlenen command'lar,
- room event dağıtımı,
- message idempotency kaydı.

Kalıcı oyun sonucu, leaderboard, kullanıcı/hane verisi ve her frame/tick Redis'e
yazılmaz. Normal HTTP endpoint'lerinin readiness'i Redis'e bağlanmaz. Redis
kesintisinde yalnız coordination/realtime özelliği unavailable olur.

Instance command'ları kısa kesintilerde kaybolmaması ve işlendikten sonra açıkça
ACK edilebilmesi için süreli Redis Streams girdileridir. Room event'leri anlıktır
ve ileride snapshot ile telafi edileceğinden Redis Pub/Sub üzerinden dağıtılır.
Room event yayınlama işlemi lease değerini aynı Redis script'i içinde doğrular;
eski owner'ın fencing token'ıyla event yayınlamasına izin verilmez.

RabbitMQ, kalıcı asenkron business job veya outbox consumer ihtiyacı oluşana
kadar; Kafka ise replay edilebilir yüksek hacimli event stream ihtiyacı oluşana
kadar eklenmeyecektir.

## ADR-010 — GameSession ortak lifecycle aggregate'i

**Durum:** Kabul edildi — 19 Eylül 2026

Oyun türlerinden bağımsız ortak lifecycle `GameSession` aggregate'i tarafından
yönetilir:

```text
lobby -> readyWindow -> countdown -> running -> finished
   ^          |
   +----------+

Her terminal olmayan state -> cancelled
```

- `lobby`: Oyuncular katılabilir ve ready durumunu değiştirebilir.
- `readyWindow`: Minimum ready oyuncu sayısına ulaşılmıştır; yeni oyuncular
  belirlenen süre boyunca katılabilir.
- `countdown`: Katılım ve ready değişikliği kapanmıştır. Ready olmayan oyuncular
  session dışında bırakılır.
- `running`: Oyun türüne özel runtime çalışır.
- `finished` / `cancelled`: Terminal state'lerdir.

Session kuralları minimum/maksimum oyuncu, ready window süresi, countdown süresi,
oyun modu ve protokol version'ını içerir. State yalnız aggregate metotlarıyla
değişir. Her başarılı değişiklik monoton `version` ve sıralı bir domain event
üretir. Snapshot'tan restore işlemi event üretmez.

Domain event'leri publication başarısızlığında kaybolmasın diye aggregate
tarafından otomatik silinmez. Application katmanı ileride snapshot ve event/outbox
kaydını aynı transaction'da kalıcılaştırdıktan sonra pending event'leri temizler.

WebSocket connection/presence bilgisi aggregate'e eklenmez. Bu bilgi Redis
coordination katmanında kısa ömürlüdür; reconnect eden kullanıcı kalıcı connection
state'i yerine session snapshot'ını alır. Skor, fizik, elenme ve leaderboard
kuralları da ortak aggregate'e ait değildir; oyun türüne özel domain tarafından
yönetilir.

Bu faz MongoDB adapter'ı, HTTP/WebSocket endpoint'i veya realtime room runtime
eklemez. Persistence concurrency sözleşmesi ve room runtime ayrı çalışma
paketlerinde GameSession snapshot/version modeli üzerinden kurulacaktır.

## ADR-011 — GameSession optimistic concurrency ve transactional outbox

**Durum:** Kabul edildi — 19 Eylül 2026

`GameSession` aggregate snapshot'ı MongoDB'de kalıcılaştırılır. Her update,
aggregate yüklenirken görülen `expectedVersion` ile mevcut document version'ını
aynı write filtresinde karşılaştırır. Version eşleşmezse ikinci yazarın değişikliği
uygulanmaz ve application katmanına açık bir concurrency hatası döner. Bu yaklaşım
process içi mutex veya distributed lock gerektirmeden birden fazla API instance'ı
arasında kayıp update'i önler.

Aggregate snapshot'ı ile o değişiklikte oluşan domain event'leri aynı MongoDB
transaction'ında yazılır. Event'ler oyunlara özel olmayan `OutboxMessage`
koleksiyonunda saklanır. Event ID ve aggregate-version çiftleri unique index ile
korunur; böylece aynı event'in mükerrer kalıcılaştırılması engellenir. Snapshot
yazımı veya outbox insert'lerinden biri başarısız olursa transaction bütünüyle
rollback edilir.

Repository yalnız başarılı transaction sonrasında aggregate'in pending event'leri
temizlenebilecek şekilde tasarlanmıştır. Outbox publisher bu paketin kapsamında
değildir. İleride publisher eklendiğinde unpublished kayıtları claim ederek
at-least-once teslim edecek; consumer'lar `eventId` üzerinden idempotent olacaktır.
Henüz retention süresi belli olmadığı için published event'lere TTL index
eklenmemiştir.

Kalıcı snapshot ortak lifecycle verisiyle sınırlıdır. Skor, oyun fiziği, elenme,
leaderboard ve connection presence ortak GameSession document'ına eklenmez.

## ADR-012 — GameSession application orchestration ve command idempotency

**Durum:** Kabul edildi — 19 Eylül 2026

GameSession kullanım akışları transport katmanından bağımsız CQRS handler'larıyla
yürütülür. Session oluşturma, katılma, ready durumunu değiştirme, ayrılma ve iptal
etme command; yetkili snapshot okuma ise query olarak tanımlanır. Handler'lar house
üyelik ve iptal yetkisini kontrol ettikten sonra aggregate metodunu çalıştırır ve
snapshot ile pending event'leri repository üzerinden kalıcılaştırır.

Mutating command'larda `actorId + commandId` çifti idempotency anahtarıdır. Command
türü ve business payload hash'i receipt kaydında tutulur. Receipt, GameSession
snapshot'ı ve outbox event'leri aynı MongoDB transaction'ında yazılır. Aynı command
tekrar geldiğinde state ikinci kez değiştirilmez; aynı anahtar farklı command veya
payload için kullanılırsa conflict üretilir. State'i değiştirmeyen başarılı
command'lar da receipt bırakarak aynı sözleşmeye uyar.

Receipt kayıtlarına bu aşamada TTL eklenmez. GameSession saklama süresi ve replay
penceresi ürün davranışıyla birlikte belirlendiğinde session ve receipt retention
politikası beraber tanımlanacaktır.

Bu faz HTTP/WebSocket endpoint'i, realtime room runtime, oyun türüne özel command,
outbox publisher veya message broker eklemez. Aynı handler'lar sonraki fazlarda
HTTP ya da WebSocket/room runtime tarafından çağrılabilir.

## ADR-013 — Realtime room runtime ve tek-yazar oda modeli

**Durum:** Kabul edildi — 19 Eylül 2026

Her aktif GameSession odası aynı anda yalnız bir API instance'ı tarafından
işletilir. Owner seçimi Redis lease ile yapılır; lease düzenli yenilenir ve her
yeni owner monoton artan bir fencing token alır. Instance lease'i yenileyemezse
odayı işlemeyi bırakır. Eski owner'ın event yayınlaması Redis tarafında fencing
kontrolüyle reddedilir.

Runtime'ın sorumlulukları şunlardır:

- odayı yüklemek ve lease sahibi instance'ta sınırlı bir command kuyruğu açmak,
- instance'a yönlendirilmiş Redis Stream command'larını oda içinde sırayla
  mevcut CQRS handler'larına iletmek,
- başarılı command sonrasında version'lı GameSession snapshot event'i yayınlamak,
- `readyWindow` ve `countdown` deadline'larını kalıcı snapshot'tan yeniden kurmak,
- owner kapanırsa yeni owner'ın MongoDB snapshot'ından devam edebilmesini
  sağlamak.

MongoDB kalıcı ve doğrulanabilir source of truth olmaya devam eder. Redis oda
sahipliği, command taşıma ve anlık event yayını içindir. Pub/Sub event'i geçici
bir kesintide kaçabilir; istemci reconnect olduğunda MongoDB'deki güncel snapshot
ve version ile toparlanır. Command ise başarılı application işlemi sonrasında ACK
edilir. ACK edilmeyen stream girdileri çalışan process içinde de yeniden teslim
edilir; handler receipt'leri aynı command'ın state'i ikinci kez değiştirmesini
engeller.

Deadline geçişleri kullanıcıya ait sahte bir kimlikle değil,
`gameSessionRuntime` system actor'ı ve session/deadline'dan türetilen deterministik
bir command ID ile kalıcılaştırılır. Böylece failover sırasında aynı deadline iki
instance tarafından denenirse Mongo optimistic concurrency ile command receipt
birlikte sonucu tekilleştirir.

Lease süresi; renew aralığı, command timeout'u ve retry bütçesini kapsamak
zorundadır. Aksi bir runtime konfigürasyonu başlangıçta reddedilir. Bu, uzun süren
bir command yüzünden instance'ın fark etmeden lease dışına çıkmasını önler.

Kullanıcı actor kimliği command payload'ından kabul edilmez. Realtime envelope'un
`actorId` alanı ileride JWT doğrulayan transport gateway tarafından doldurulur;
runtime CQRS command'larındaki `UserID` değerini yalnız bu doğrulanmış alandan
üretir. Böylece bir house üyesi payload içinde başka bir üyenin ID'sini yazarak
onun adına ready, leave veya cancel işlemi yapamaz.

Bu paket WebSocket endpoint'i, bağlantı kimlik doğrulama, presence lifecycle,
slow-consumer politikası, Flappy Bird fiziği/tick'leri, skor, elenme veya
leaderboard içermez. Bunlar sırasıyla transport gateway ve oyuna özel runtime
paketlerinde ele alınacaktır.

## ADR-014 — Realtime protocol ve WebSocket gateway

**Durum:** Kabul edildi — 19 Eylül 2026

Mobil istemciler realtime GameSession akışına aşağıdaki endpoint üzerinden
bağlanır:

```text
GET /api/v1/game/:sessionId/realtime
Authorization: Bearer <JWT>
Upgrade: websocket
```

JWT HTTP upgrade tamamlanmadan doğrulanır. Session erişimi house membership
policy ile kontrol edilir ve kullanıcı kimliği client mesajından alınmaz. Gateway,
command envelope içindeki `actorId` değerini doğrulanmış JWT subject'inden üretir.
Browser origin'i varsa `CORS_ALLOW_ORIGINS` ile eşleşmek zorundadır; native mobil
istemcilerin Origin header'ı göndermemesi desteklenir.

Client mesaj sözleşmesi protocol version, benzersiz message ID, command type ve
oyuna/command'a ait payload'dan oluşur. Bu sürümün desteklediği ortak command'lar
`gameSession.join`, `gameSession.setReady`, `gameSession.leave` ve
`gameSession.cancel` ile sınırlıdır. Bilinmeyen üst seviye alanlar reddedilir.

Server mesajları üç temel tipe ayrılır:

- `gameSession.commandAccepted`: Mesaj gateway tarafından doğrulanmış ve kalıcı
  Redis Stream'e yönlendirilmek üzere kabul edilmiştir; business işleminin
  tamamlandığı anlamına gelmez.
- `gameSession.snapshot`: Başarılı işlemin kesin sonucudur. `sequence`, kalıcı
  GameSession version'ıdır ve `messageId` sonucu tetikleyen command ile
  korelasyon kurar.
- `gameSession.commandRejected`: Kalıcı olarak uygulanamayan command'ın hata kodu,
  argümanları ve retry bilgisidir. Yalnız command'ı gönderen connection'a iletilir.

Bağlantı kurulduğunda MongoDB'den güncel snapshot gönderilir. Gateway snapshot
okunurken oluşabilecek Pub/Sub event'lerini geçici olarak buffer'lar; initial
snapshot'tan eski version'ları eler ve daha yeni event'leri sırayla teslim eder.
Reconnect kalıcı socket state'ini geri yüklemeye çalışmaz; MongoDB snapshot ve
version üzerinden toparlanır.

Her API instance'ı oda başına tek Redis Pub/Sub subscription açar ve event'i o
instance'taki bağlantılara process içinde dağıtır. Her connection için ayrı Redis
subscription açılmaz. Connection presence Redis'te TTL ile tutulur ve periyodik
yenilenir; bağlantı kapanınca silinir.

Gateway; 16 KiB inbound mesaj sınırı, bağlantı bazlı command rate limit,
ping/pong idle timeout, tek-yazarlı socket writer ve 64 mesajlık sınırlı outbound
queue uygular. Kuyruğu dolduran slow consumer veya art arda protokol ihlali yapan
istemci policy violation ile kapatılır. Uygulama shutdown'ında önce yeni realtime
trafik durdurulur ve socket/hub'lar kapatılır, sonra room runtime ve Redis
coordinator kapatılır.

Bu protokol Flappy Bird frame/input mesajlarını, fizik tick'lerini, skor, elenme
ve leaderboard'u tanımlamaz. Bunlar ortak lifecycle command'larından ayrı,
oyuna özel protokol ve runtime paketinde ele alınacaktır.

## ADR-015 — Game catalog ve tek aktif session

**Durum:** Kabul edildi — 20 Eylül 2026

Mobil istemci oyun modu, protokol sürümü, oyuncu sayısı veya lobby sürelerini
belirleyemez. Bu değerler uygulama içindeki statik game catalog tarafından
yönetilir. İlk tanım `flappyBird` için realtime mod, protokol sürümü 1, minimum 2
oyuncu, en fazla 8 oyuncu, 30 saniyelik ready window ve 3 saniyelik countdown
olarak belirlenmiştir. Bir house'un daha düşük üye limiti varsa session oyuncu
limiti house kapasitesine düşürülür; kapasite minimum oyuncu sayısından düşükse
session oluşturulmaz.

Aktif session açma ve keşfetme sözleşmeleri şöyledir:

```text
PUT /api/v1/game/:gameKey/session
GET /api/v1/game/:gameKey/session?houseId=:houseId
```

`PUT` aynı house ve oyun için aktif session varsa onu döndürür, yoksa katalogdaki
tanımdan yeni session oluşturur. Bu nedenle aynı aktif kaynak için tekrarlanabilir
bir ensure işlemidir; command kimliği sunucu tarafından üretilir. `GET` hiçbir
state üretmeden yalnız mevcut aktif session'ı döndürür. Her iki işlem de JWT ve
house membership kontrolünden geçer.

Aynı house ve game key için tek aktif session garantisi process belleğiyle değil,
MongoDB'deki `ActiveGameSession` koleksiyonunun unique index'iyle sağlanır. Slot,
session snapshot'ı, command receipt ve outbox mesajıyla aynı transaction içinde
oluşturulur. Session `finished` veya `cancelled` olduğunda slot yine snapshot
değişikliğiyle aynı transaction içinde silinir. Böylece farklı API instance'larına
eşzamanlı gelen create istekleri iki ayrı aktif lobby üretemez.

Migration mevcut non-terminal GameSession kayıtları için slotları backfill eder.
Aynı house ve oyun için birden fazla aktif eski kayıt bulunursa sessizce seçim
yapmak yerine migration hata verir; veri kaybına yol açabilecek otomatik iptal
uygulanmaz.

## ADR-016 — Realtime protocol v1 ve `flappyBird` emekliye ayrıldı

Protocol v1'i kullanan bir istemci yoktur. `flappyBird` katalog kaydı v1 ile
çalışıyordu ancak bir runtime'ı yoktu: `running` durumuna geçen bir session hiçbir
zaman `finished` olamıyor ve house'un aktif slotunu süresiz tutuyordu.

Bu nedenle gateway yalnız `protocolVersion=2` ile upgrade kabul eder; v1 decoder,
v1 mesaj üretimi ve gateway içindeki v1 dalları kaldırılmıştır. Varsayılan katalog
yalnız etkinleştirilmiş oyunları içerir. `0043_retireFlappyBird` migration'ı kalan
non-terminal `flappyBird` session'larını `cancelled/gameRetired` yapar ve aktif
slotlarını siler. Yeni oyunlar ortak modül sözleşmesi üzerinden eklenecektir.

## ADR-017 — Oyun verisinin saklama süreleri ve outbox'ın kaldırılması

`OutboxMessage` kayıtlarını okuyan bir tüketici yoktu; result teslimi doğrudan room
event'i ve `GET /result` fallback'i ile yapılıyordu. Session ve match transaction'ları
artık outbox yazmaz; domain event'leri yalnız version tutarlılığı için doğrulanır.

Komut receipt'leri yalnız istemci komutları için yazılır ve 1 saat sonra TTL ile silinir.
Runtime'ın sistem geçişleri (`gameSessionRuntime` aktörü: deadline, reconcile, match
completion) beklenen version ve deterministik domain kurallarıyla fence'lenir; receipt
yazmaz. Terminal olmayan sistem geçişi tek koşullu yazmadır ve transaction açmaz.

Ev + oyun başına tek aktif session garantisi `GameSession.activeKey` alanına ve unique
partial index'e taşınır. Rolling deploy sırasında önceki sürüm aynı veritabanına yazmaya
devam ettiğinden geçiş iki adımlıdır: önce iki yere birden yazılır ve okuma `activeKey`
üzerinden yapılıp bulunamazsa `ActiveGameSession` pointer'ına düşülür; önceki sürüm hiçbir
instance'ta kalmadığında pointer ve `OutboxMessage` collection'ı kaldırılır.

İptal edilen session'lar ve iptal edilen maçların result'ları `endedAt + 10 dakika` sonra
TTL ile silinir; tamamlanan maçlar ve sonuçları kalıcıdır. `GameRuntimeOwner` kaydı
session terminal olduktan 1 saat sonra silinir. Bu kayıt session'a gömülmemiştir: CAS
otoritesini rolling deploy sırasında iki kayıt arasında taşımak split-brain riski taşır ve
kazanç maç başına tek küçük dokümandır. Match result yetkilendirmesi session yerine
result'taki `houseId` ile yapılır.

Redis room fence sayacı her acquire'da 24 saat TTL alır. Token yalnız eşitlikle karşılaştırılır
ve kalıcı fence Mongo generation'dır; uzun süre kullanılmayan bir odada sayacın sıfırdan
başlaması güvenlidir. Room üyelik listesi her reconcile turunda okunmaya devam eder, ancak
socket'lere `gameRuntime.access` event'i yalnız üye kümesi değiştiğinde veya room yeni
sahibinde ilk açıldığında gönderilir.

Geçişin ikinci adımı v37 (1.3.10) tüm instance'larda çalıştıktan sonra uygulanmıştır:
pointer yazımı ve pointer'a geri düşme kaldırılmış, recovery scanner `activeKey` partial
index'ini `activeKey` imleciyle sayfalar. `0045_retireGameOutbox` önce 0044
adımlarını tekrarlayarak rollout sırasında önceki sürümün yazdığı eksik alanları tamamlar,
ardından yazanı kalmamış `OutboxMessage` collection'ını düşürür. `ActiveGameSession`
bu sürümün rollout'u boyunca hâlâ önceki sürüm tarafından yazıldığından bir sonraki
release'in migration'ında düşürülür; erken düşürmek collection'ı index'siz olarak yeniden
oluştururdu.

## ADR-018 — Oyun modülü sözleşmesi ve paylaşılan multiplayer core

Yeni oyunlar core'u değiştirmeden eklenebilmelidir. Core; session lifecycle'ı, oda
sahipliğini (lease + Mongo generation), kalıcılığı, WebSocket taşımasını ve fanout'u
sahiplenir. Oyun yalnız kurallarını, wire modellerini ve simülasyonunu bilir. Sözleşme
`internal/application/game/module` paketindedir; `module.Registry` bir süreçte etkin
oyunları tutar ve core oyuna özgü her davranışı oradan çözer.

İlk hat **tick** hattıdır (`TickModule` / `TickRuntime`): sabit hızlı scheduler, geçici
gameplay girdisi, Redis checkpoint ile recovery. Control binding, epoch/generation fencing,
faz enum'u ve kontrol hataları her tick oyunu için ortaktır ve `module` paketindedir.
Client'a giden mesaj tipleri ve hata kodları oyun anahtarıyla türetilir
(`<gameKey>.snapshot`, `<gameKey>.error.stale_control`); House Rockets için bunlar önceki
değerlerin aynısıdır. Sıra tabanlı oyunlar için ikinci hat Faz 5'te eklenecektir; sözleşmesi
kullanılmadan önce tanımlanmamıştır.

Maç tamamlama (`CompleteMatchCommand`) ve sonuç sorgusu core'dadır; oyun yalnız sonucu
doğrular ve normalleştirir (`FinalizeResult`) ve kendi wire modelini döndürür
(`DecodeResult`). Somut oyun paketlerini yalnız `internal/application/game/games`
(composition root) import eder; bu kural bir mimari testle zorunludur.

Rolling deploy sırasında önceki sürümle Redis üzerinden değiş tokuş edilen formatlar
(gameplay input, frame, control grant, checkpoint) değişmemiştir ve bir uyumluluk testiyle
sabitlenmiştir. Frame başlığı hub başına bir kez decode edilir; bağlantıya özel
projeksiyonu modül yapar.

`realtime` paketi Go alt paketlerine bölünmemiştir: gateway ile oda yöneticisi birbirinin
iç durumunu kullanır ve bölmek bu durumu dışa açmayı gerektirirdi. Sorumluluklar dosya ve
tip düzeyinde ayrılmıştır (`room`, `roomLobby`, `tickHost`, `tickCheckpoint`,
`roomCompletion`, `gatewayV2`). Oda durumu `lobbyState`, `tickState` ve `completionState`
olarak gruplanmış, olay döngüsü olay başına küçük metotlara bölünmüştür. Tick oyunlarının
ihtiyaç duyduğu owner repository, gameplay bus ve checkpoint store constructor'da çözülür;
eksik bir yetenek ilk maçta değil başlatmada hata verir.

## ADR-019 — Oyun config'i `Game` collection'ında, session'a dondurulur

Bir oyunu açıp kapatmak, belirli evlere açmak veya kurallarını değiştirmek deploy
gerektirmemelidir. Her oyun için `Game` collection'ında bir doküman tutulur (`_id` oyun
anahtarı): `enabled`, `rollout.houseIds` (boş liste tüm evler demektir), `rules`,
`settings`, `version`, `updatedAt`, `updatedBy`. `rules` ve `settings` kısmi nesnelerdir;
dokümanda olmayan anahtar koddaki varsayılanı kullanır. Bilinmeyen anahtar reddedilir, böylece
yazım hatası bir varsayılanı sessizce bırakmaz.

**Ayarlanabilir / sözleşme ayrımı.** Lobby kuralları (oyuncu sayıları, ready window,
countdown) ve oyunun ağ/presence ayarları ayarlanabilir. House Rockets için bunlar snapshot
hızı, girdi ve steer limitleri, heartbeat, control timeout ve reconnect grace'tir. Fizik ve
scheduler hızı, parkur, maç süresi ve protokol sürümleri sözleşme değerleridir. Checkpoint
ve sonuç doğrulaması bu değerlere bağlı olduğundan kodda kalırlar. Sınırları modül belirler
(`module.Game.ResolveConfig`); core yalnız dokümanı varsayılanların üzerine uygular.

**Dondurma.** Session oluşturulurken çözülmüş kurallar (zaten olduğu gibi), `settings` ve
`configVersion` session dokümanına kopyalanır. Runtime, recovery, gateway rate limit'i, lobby
heartbeat'i ve `welcome.settings` session'daki kopyayı kullanır. Bu yüzden config değişikliği
çalışan ya da lobby'deki maçlara hiçbir zaman ulaşmaz. `settings` alanı olmayan session'lar
(önceki sürümün yazdıkları) varsayılanlarla çalışır. Kapatılan oyunun mevcut session'ları
erişilebilir kalır ve bitebilir; yalnız yeni session açılamaz (`game.error.unavailable`).
Kodda kayıtlı olup dokümanı olmayan oyun kapalıdır; yeni oyunlar böylece kapalı çıkar.

**Cache ve yayılma.** Her instance config'i bellekte tutar ve 30 saniyede bir yeniden okur.
Değişiklik `PUT /game/{gameKey}/config` (super admin, `expectedVersion` ile optimistic
concurrency) ile yazılır. Yazan instance kendini hemen tazeler ve Redis Pub/Sub kanalı
`houseflow:coordination:v1:gameConfig` üzerinden diğerlerine haber verir; ölçülen yayılma
saniyenin altındadır. Duyuru kaybolursa periyodik okuma yakalar. Mongo'da elle yapılan
düzenleme de en geç 30 saniyede uygulanır. Geçersiz bir doküman oyunu son geçerli config'iyle
bırakır ve log'lanır. Başlangıçta hiç okunamazsa oyunlar kapalı başlar ve okuma 2 saniyede
bir denenir.

**Kapasite env'de kalır.** Hangi oyunların çalıştığı veri, instance kapasitesi altyapıdır:
`REALTIME_MAX_OWNED_ROOMS` / `REALTIME_MAX_CONNECTIONS` production'da zorunludur, diğer
ortamlarda verilmezse sınırsızdır. `HOUSE_ROCKETS_ENABLED` kaldırıldı. Tick oyunları Redis
coordinator varsa kaydedilir.

**Rolling deploy.** Session dokümanına yalnız opsiyonel alan eklendi. Rollout sırasında
önceki sürüm bir session'ı yeniden yazarsa `settings`/`configVersion` düşer ve session
varsayılanlarla devam eder. Seed değerleri varsayılanlarla aynı olduğundan davranış değişmez.
Bu yüzden ayarlar rollout tamamlanmadan değiştirilmemelidir. Geri dönüşte önceki sürüm
`Game` collection'ını yok sayar ve `HOUSE_ROCKETS_ENABLED` secret'ını okur. Bu nedenle Fly
secret'ı Release 1 kararlı olana kadar silinmez.


## ADR-020 — Tick hattında paylaşımlı projeksiyon ve local kısa yol

Bu değişiklikle protokol değişmedi; client'a giden byte'lar öncekiyle birebir aynı ve bu bir
testle sabitlendi.

**Paylaşımlı projeksiyon.** Önceden her bağlantı yayınlanan frame'i ayrı ayrı decode ediyor,
kendi snapshot modelini kuruyor ve marshal ediyordu. Şimdi hub frame'i bir kez çözüyor
(`TickModule.PrepareFrame`). Modülün `FrameView`'u, aynı byte'ları görecek bağlantıları
`ViewKey` ile gruplar; core her anahtar için bir kez kodlar ve byte'ları paylaştırır. House
Rockets'ta bağlantıya özel tek alan kontrol eden oyuncunun `controlGeneration`'ıdır. Paylaşılan
JSON bir kez üretilir, kontrol eden bağlantının varyantı bu alanın değerinin yerine
yazılmasıyla elde edilir. Snapshot mesajının zarfı da frame başına bir kez kodlanır; payload
son alan olduğu için yeniden taranmadan araya eklenir. Frame başlığı (`FrameHeader`) da
modülün görünümünden gelir, frame hub başına tek kez decode edilir.

**Local kısa yol.** Odayı yöneten instance tüm room event'lerini kendi socket'lerine bellekten
teslim eder (`managedRoom.publish`). Hub, kendi instance'ının Redis'ten dönen kopyasını yok
sayar. Redis yalnız iki durumda atlanır: local bir bağlantıya hedeflenmiş event'lerde ve başka
instance'ta socket yokken periyodik frame'lerde. Session, sonuç ve erişim event'leri her zaman
Redis'ten de gider. Uzak socket bilgisi:
- presence'tan en fazla 1 saniyelik gecikmeyle okunur;
- bir uzak socket gameplay bus'tan girdi gönderdiğinde hemen işaretlenir. Yeni bağlanan uzak
  socket'in ilk işi snapshot istemek olduğundan frame kaçırmaz.

Okunamayan veya bilinmeyen presence "uzakta socket var" sayılır. Redis publish'i Lua ile lease'i
doğruluyordu; local teslimat aynı korumayı `leaseValid()` ile sağlar. Bu kontrol runtime'ı da
yöneten, yerelde muhafazakâr hesaplanan lease süresidir. Publish'ler oda başına sıralıdır; local
ve Redis teslimatı aynı sırayı görür.

**`CriticalKey`.** Her frame'de `fmt.Sprintf` ve outcome için `json.Marshal` yerine tek
buffer'da kurulur; anlamı aynıdır.

**Ölçüm.** Süre histogramına 60 Hz adımı çevresinde ara bucket'lar (15, 17, 25, 33 ms) ve p99
eklendi. Yük testi Redis publish ve yalnız local teslim edilen event sayısını raporlar.
`HOUSEFLOW_LOAD_PLACEMENT=colocated` bir odanın tüm oyuncularını aynı instance'a yerleştirir.

**Paylaşımlı scheduler yapılmadı.** Plan bunu yük testi sonucuna bağlıyordu:
- Fizik adımı ortalama 0,05 ms sürüyor.
- Tick aralığı 8 oda × 8 oyuncuda da ortalama 16,67 ms. p99 üst sınırı 20 ms bucket'ında,
  yani 60 Hz adımının hemen üstünde.
- Oda başına bir ticker, 8 odada saniyede ~480 uyanma demek; ölçülebilir bir maliyet değil.

Fanout iyileştirmeleri fizik goroutine'inde çalışmadığı için tick aralığını değiştirmez.

**Altyapı.** `min_machines_running` 0'da bırakıldı. Client oyuna gelmeden önce auth ve house
endpoint'lerine istek attığı için makineyi zaten başlatmış olur. Açık WebSocket'ler Fly proxy'si
için bağlantı sayıldığından maç sürerken makine otomatik durdurulmaz. Sürekli açık bir makine,
canlıya çıkıştan sonra trafik ve cold start süresi ölçülerek değerlendirilecek. Performance CPU'ya
geçilmedi.
Yük testinde bir oda × 8 oyuncu, M serisi tek çekirdeğin ~%6-7'sini kullanıyor. Fly'ın paylaşımlı
CPU'su sürekli yükte kotayla sınırlandığından production limitini (`REALTIME_MAX_OWNED_ROOMS=1`)
artırmadan önce Fly metriklerinde CPU throttling izlenmeli.

**Sonraki adım (yapılmadı).** Dağınık yerleşimde her frame Redis'ten geçmek zorunda. Bir odanın
socket'leri hangi instance'a düştüyse orada kalıyor. Upgrade isteği, odanın sahibi olan makineye
Fly'ın `fly-replay` header'ıyla yönlendirilirse bütün odalar colocated olur ve local kısa yol
frame trafiğinin tamamını Redis'ten çıkarır. Bu, Fly'a bağımlı bir routing kararı olduğu için
ayrıca değerlendirilmelidir.

## ADR-021 — Turn hattı ve pilot oyun (`ticTacToe`)

Sıra tabanlı oyunlar ikinci hat olarak eklendi (`module.TurnModule`). Tick hattının aksine
bellekte runtime yok; oyunun tüm durumu session dokümanında tutuluyor (`gameState`,
`turnDeadline`). Her değişiklik, onu doğuran komutun içinde commit ediliyor; recovery yalnızca
Mongo'dan okumaktan ibaret.

**Hamle dayanıklı bir komut.** Client `<gameKey>.<hamle>` mesajı gönderir, modül
`DecodeMove` ile katı biçimde doğrulayıp kanonik payload'a çevirir. Mesaj lobby komutlarıyla
aynı Redis stream'inden odanın sahibine gider. Oda döngüsü `ApplyTurnMoveCommand`'ı çalıştırır:
yeni durum ve komutun receipt'i aynı transaction'da yazılır, yani tekrar gönderilen hamle bir
kez uygulanır. Kurala aykırı hamle `game.error.invalid_move`, sırası olmayan oyuncunun hamlesi
`game.error.not_your_turn` ile reddedilir. Hamle geçmişi saklanmaz; oyunun ihtiyacı olan her
şey durumdadır.

**Turn motoru (`TurnEngine`).** Session'ı değiştiren her komut (advance, hamle, ayrılma, üyelik
uzlaştırma, iptal) motoru kullanır. Motor:
- maç yeni başladıysa ilk durumu kurar (`Start`);
- süresi geçmiş turları sırayla çözer (`Expire`, komut başına en fazla 32 tur);
- artık oynamayan oyuncuları düşer (`Reconcile`).

Durumlar sıralı anahtarlı kanonik JSON olarak karşılaştırılır; değişmeyen durum version
artırmaz. Bunu sözleşme testi zorunlu kılar: değişmemiş oyuncu listesinde `Reconcile` aynı
durumu döndürmelidir.

**Tur süresi mevcut deadline timer'ıyla yönetilir.** `sessionDeadline` çalışan bir turn
oyununda `turnDeadline`'ı döndürür; oda döngüsü bunu ready window ve countdown gibi
`AdvanceGameSessionCommand` ile ilerletir. Turlar kendi deadline zamanında çözülür, sonuç
belirleyicidir. Recovery tarayıcısı çalışan turn oyunlarının odasını da açar. Kimse bağlı olmasa
bile süresi dolan tur çözülür ve evin aktif slotu boşalır.

**Maç sonu.** Modülün döndürdüğü sonuç, session'ın terminal geçişi ve tetikleyen komutun
receipt'iyle tek transaction'da yazılır. Tick oyunlarının runtime owner fence'i turn oyununda
yoktur (`MatchCompletion.Owner` nil): bütün durum session'da olduğundan beklenen session
version'ı fence'tir ve sonuç dokümanının `runtimeEpoch` alanına yazılır. Çalışan bir turn
oyununun iptali, oyuncu listesinden kurulan iptal sonucuyla biter. Oda, terminal session
snapshot'ının ardından `<gameKey>.result` mesajını yayınlar.

**Gateway.** Bağlantı iki hattı da kabul eder. Turn oyununda frame, kontrol bağlama ve lobby
heartbeat'i yoktur; ping yalnızca pong döndürür. Sessiz bir oyuncuyu tur süresi çözer. Her
session snapshot'ını, o oyuncunun görebileceği durum (`<gameKey>.state`, `View`) izler; gizli
bilgi bu yüzden bağlantı başına modülde süzülür. Resync session'ı ve durumu yeniden gönderir.

**Pilot oyun.** `internal/application/game/games/ticTacToe` iki kişilik Tic-Tac-Toe:
- kazanma, beraberlik, süre aşımında hükmen yenilgi ve rakip ayrılınca kazanma kurallarını
  içerir;
- ayarlanabilir tek değeri tur süresidir (`turnTimeoutMilliseconds`, 5 sn - 5 dk).

Core'a eklenen tek satır composition root'taki kayıttır. Kod mimari testi, iki hattın sözleşme
testlerini ve gerçek gateway üzerinden uçtan uca bir maçı geçer. `Game` dokümanı olmadığı için
kapalı başlar; açmak için `PUT /game/ticTacToe/config` (`expectedVersion: 0`) yeterlidir.

Client'a yönelik mesajlar:

| Yön | Tip | Payload |
|---|---|---|
| client → server | `ticTacToe.move` | `{"cell": 0-8}`; kabulde `commandAccepted` gelir |
| server → client | `ticTacToe.state` | `board` (9 hücre, `"x"`/`"o"`/`null`), `players`, `currentPlayerId`, `turnEndsAt`, `yourMark`, `moves`, `phase` |
| server → client | `ticTacToe.result` | `winnerId`, oyuncu başına `outcome` (`won`/`lost`/`draw`), `endReason`, son tahta |

Lobby (join/ready/leave/cancel), welcome, ping ve resync, House Rockets ile aynı v2 zarfını
kullanır.

**Rolling deploy.** Turn oyunu açılmadıkça yeni alan yazılmaz. Pilot kapalı çıktığı için önceki
sürüm turn session'ı görmez. Tick oyunlarının davranışı ve wire formatları değişmedi.

## ADR-022 — WebSocket sıkıştırması ve oda kapasitesi

Gateway permessage-deflate'i destekler (`EnableCompression`). Sıkıştırma bağlantı başına
anlaşılarak açılır:
- teklif eden client sıkıştırılmış snapshot alır;
- etmeyen client eskisi gibi düz veri alır.

Bu yüzden değişiklik geriye uyumludur ve protokolü değiştirmez. Android client'ı (OkHttp 4.12)
sıkıştırmayı kendiliğinden teklif eder. iOS'taki `URLSessionWebSocketTask`'ın teklif edip
etmediği canlıda handshake'ten görülecek.

Ölçüm:
- **Gerçek gateway, 2 oyunculu maç:** ağdan okunan byte snapshot başına 1.566'dan 684'e
  iniyor (%56).
- **8 oyunculu snapshot:** 3.805'ten yaklaşık 950 byte'a iniyor (%75).
- **Sunucu maliyeti:** Sıkıştırma her bağlantı için ayrı yapılır. Bir worker'da 4 oda × 8 oyuncu
  varken CPU %15'ten %18,5'e çıkıyor (yerel ölçüm, `GOMAXPROCS=1`); ACK süreleri ve hata sayıları
  değişmiyor.

Production limitleri makine başına 1 oda / 8 bağlantıdan 4 oda / 32 bağlantıya çıkarıldı. Önceki
değerler ilk canlı test için seçilmişti ve aynı anda en fazla 2 evin oda açmasına izin veriyordu.
Yerel ölçümde bir makinenin 4 dolu odası tek M serisi çekirdeğin yaklaşık %19'unu kullanıyor.
Fly'ın paylaşımlı CPU'su daha yavaş ve sürekli yükte kotayla sınırlı olduğundan bu limit
staging'de Fly üzerinde yük testiyle doğrulanmalı. Canlıda da CPU throttling izlenmelidir.

## ADR-023 — Sosyal giriş: sağlayıcı yalnız kimlik doğrular, oturum bizimdir

**Durum:** Kabul edildi — 19 Ekim 2026 (uygulama başlamadı). Ayrıntılı plan ve fazlar:
`socialLoginPlan.md`.

Google, Apple, Facebook ve Snapchat ile kayıt ve giriş eklenir. Sağlayıcı yalnız kimliği
doğrular; kullanıcı kendi `User` collection'ımızdan okunur ve kendi JWT'miz verilir.
Sağlayıcı token'ları saklanmaz, profil sağlayıcıdan sürekli çekilmez. Tek istisna, hesap
silmede Apple'ın zorunlu tuttuğu revoke için saklanan şifreli Apple refresh token'ıdır.

Her girişte client sağlayıcıdan yeni bir token alır ve backend onu doğrular. Client'ın
gönderdiği email veya sağlayıcı ID'si kimlik kanıtı sayılmaz. Google, Apple ve Facebook
Limited Login token'ları JWKS ile offline doğrulanır. Facebook klasik ve Snapchat
token'ları tek bir server çağrısıyla doğrulanır.

Kararlar:
- **Tek uç nokta.** `POST /auth/social/:provider` kimlik varsa giriş yapar, yoksa kayıt
  açar; cevaptaki `isNewUser` ikisini ayırır.
- **Ayrı `UserIdentity` collection'ı.** `{provider, providerUserId}` unique index'i, bir
  sağlayıcı hesabının yalnız bir kullanıcıya bağlanmasını garanti eder. `User` üzerinde
  yalnız `signUpProvider` enum'u tutulur (`0` = email/şifre, `1` Google, `2` Apple,
  `3` Facebook, `4` Snapchat) ve sonradan değişmez.
- **Otomatik hesap birleştirme yok.** Sağlayıcının emaili başka yolla kayıtlı bir
  kullanıcıya aitse `409 auth.error.email_registered_with_other_method` döner. Bağlama,
  oturum açmış kullanıcı tarafından profilden yapılır. Gerekçe: doğrulanmamış emaille önceden
  açılmış hesap üzerinden ele geçirme.
- **Email zorunlu kalır.** Email vermeyen sağlayıcıda (Snapchat, bazı Facebook hesapları)
  kayıt, 10 dakikalık `pendingToken` ile kullanıcıdan email alınarak tamamlanır. Email
  doğrulanmamış sayılır ve mevcut doğrulama kodu akışıyla doğrulanır. `pendingToken`,
  access token'dan türetilmiş ayrı bir anahtarla imzalanır; böylece `AuthRequired`
  middleware'inden geçemez.
- **Şifresiz hesap.** Sosyal kullanıcının şifresi boştur. `/auth/login` bu hesaplarda
  `failedLoginAttempts` artırmadan `auth.error.use_social_login` döner. Reset akışıyla
  sonradan şifre belirlenebilir.
- **Sağlayıcı config ile açılır.** Env değişkenleri eksik olan sağlayıcı kapalıdır ve
  `auth.error.provider_not_supported` döner.

**Rolling deploy.** Yeni alan varsayılanı `0`, yeni collection'ı önceki sürüm okumaz,
sağlayıcılar kapalı çıkar. Sağlayıcı açıldıktan sonra bu değişiklikten önceki bir sürüme
dönülmemelidir: önceki sürümde şifresiz kullanıcının hatalı login denemeleri hesabı
kilitleyebilir.

## ADR-024 — Kullanıcının kendi hesabını silmesi

**Durum:** Kabul edildi — 20 Ekim 2026.

App Store Guideline 5.1.1(v), hesap açılabilen uygulamalarda hesabın uygulama içinden
silinebilmesini ister. Bu, Sign in with Apple'ın (ADR-023) yayın şartıdır. Oturumdaki
kullanıcı `DELETE /user/me` ile kendi hesabını siler. Uç, SuperAdmin'in kullandığı
`DeleteUserCommand`'ı çalıştırır; iki yol da aynı kuralı izler.

Önceden, başka üyesi olmayan bir evin sahibi silinemiyordu
(`user.error.cannot_delete_sole_house_owner`). Ev silme komutu olmadığından böyle bir
kullanıcı hesabını hiç silemiyordu. Artık tek transaction'da şunlar yapılır:
- **Başka üyesi olmayan evler** verisiyle birlikte silinir: `Chore`, `ChoreStatusHistory`,
  `ChoreReviewVote`, `Announcement`, `Notification`, `HouseInfoHistory`,
  `HouseInviteCode`, `GameSession`, `GameMatchResult`.
- **Kullanıcının sahibi olduğu paylaşılan evler** eskisi gibi kalan ilk üyeye geçer.
- **Kullanıcının diğer evlerdeki üyeliği** kaldırılır.
- **Kullanıcının kendi kayıtları:** `UserIdentity` ve `UserInfoHistory` silinir.

Commit'ten sonra Apple yetkisi revoke edilir. Revoke'un başarısız olması silmeyi geri
almaz.

Paylaşılan evlerde kullanıcının izleri kalır: atandığı chore'lar ve katıldığı maç
sonuçlarındaki kullanıcı ID'si. Bunlar evin diğer üyelerine ait kayıtlardır.

**Bilinen açık.** `AuthRequired` yalnız JWT imzasını ve süresini doğrular. Silinmiş
kullanıcının token'ı, süresi dolana kadar (en çok 72 saat) kullanıcıyı okumayan uçlarda
geçerli kalır. Bu durum admin silmesinde de vardı.

**Rolling deploy.** Şema değişmez. Önceki sürüm aynı istekte tek üyeli ev sahibini silmeyi
reddeder; yeni sürüm siler.
