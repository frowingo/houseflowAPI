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
