# Güvenlik ve performans sertleştirmesi

Tarih: 11 Kasım 2026. Bu belge backend'de yapılan güvenlik ve performans değişikliklerini anlatır. Değişiklikler `dev` dalında ve henüz deploy edilmedi. Etkilenen testler `scripts/testAffected.sh --base HEAD` ile çalıştırıldı: 20 paket, `-race` ile, hepsi geçti.

## Özet

| Alan | Sorun | Yapılan değişiklik | Ana dosya |
|---|---|---|---|
| Güvenlik | Rate limit IP'si istemcinin gönderdiği `X-Forwarded-For` başlığından okunuyordu | Güvenilir başlık `Fly-Client-IP` yapıldı, IP doğrulaması açıldı | [main.go](../../cmd/api/main.go) |
| Güvenlik | Sıfırlama ve doğrulama kodlarında deneme sınırı yoktu | Kod başına 5 deneme sınırı, sabit zamanlı karşılaştırma, kullanılan kodun tekrar kullanılamaması | [codeAttempts.go](../../internal/application/auth/policies/codeAttempts.go) |
| Güvenlik | JWT doğrulaması HS256 ve `exp` alanını zorunlu tutmuyordu | İkisi de zorunlu yapıldı, `iat` eksikse panik riski kapatıldı | [jwt.go](../../internal/helpers/jwt.go) |
| Güvenlik | Swagger production'da herkese açıktı | Production'da e-posta + şifre (Basic Auth) ve SuperAdmin rolü gerekiyor | [swagger.go](../../cmd/api/swagger.go) |
| Güvenlik | Bazı metin ve URL alanlarında uzunluk sınırı yoktu | Duyuru, profil adı ve resim URL'lerine `max` sınırı | [announcementModels.go](../../internal/models/dtos/announcementModels.go) |
| Güvenlik | Websocket okuma sınırı sıkıştırılmış baytları sayıyordu | Açılmış mesaj da aynı sınırla okunuyor, aşan bağlantı 1009 ile kapanıyor | [gatewayV2.go](../../internal/infrastructure/realtime/gatewayV2.go) |
| Güvenlik | Bir kullanıcı instance'ın bütün realtime slotlarını alabiliyordu | Kullanıcı başına en fazla 4 bağlantı | [admission.go](../../internal/infrastructure/realtime/admission.go) |
| Güvenlik | Snapshot isteği dolu kuyrukta maçı iptal edebiliyordu | Kuyruk yarıdan fazla doluyken snapshot `busy` ile reddediliyor | [houseTanks runtime](../../internal/application/game/games/houseTanks/runtime.go) |
| Güvenlik | Abonelik kopan oda hub'ı kayıtlı kalıyordu | Hub kayıttan çıkarılıp durduruluyor | [gateway.go](../../internal/infrastructure/realtime/gateway.go) |
| Güvenlik | Bazı Mongo çağrılarında zaman aşımı yoktu | Mevcut `CommandTimeout` uygulandı | [roomManager.go](../../internal/infrastructure/realtime/roomManager.go) |
| Performans | `/auth/isAuth` sıkı auth limitine takılabiliyordu | Yalnızca genel API limiti uygulanıyor | [routes.go](../../cmd/api/routes.go) |
| Performans | Yerelleştirme önbelleği yalnızca İngilizceyi yüklüyor, eksik çeviriler her seferinde Mongo'ya gidiyordu | `en` ve `tr` yükleniyor, eksik çeviri bir kez sorgulanıp önbelleğe alınıyor | [cache.go](../../internal/infrastructure/localization/cache.go) |
| Performans | SMTP gönderimi zaman aşımsızdı, şablon her gönderimde parse ediliyordu | 10 sn bağlantı, 20 sn toplam süre; şablon bir kez parse ediliyor | [notificationService.go](../../internal/services/notificationService.go) |
| Performans | Yanıtlar sıkıştırılmıyordu | Fiber `compress` (en hızlı seviye), websocket hariç | [main.go](../../cmd/api/main.go) |
| Performans | Redis kullanılamazken her komut sonrası 750 ms bekleniyordu | Bus kullanılamaz görünürken olay 100 ms bütçeyle deneniyor | [service.go](../../internal/infrastructure/appEvents/service.go) |
| Performans | Görev deadline taraması her dakika geçmişteki tüm görevleri okuyordu | Dakikalık tur yalnızca açık görevlere bakıyor | [deadlineService.go](../../internal/application/chore/deadlineService.go) |
| Performans | Birkaç sorgu ve silme için uygun indeks yoktu | Migration 0064 ile eklendi | [0064_queryIndexes.go](../../internal/data/migrations/0064_queryIndexes.go) |
| Performans | Kurtarma tarayıcısı boştaki her lobi için Redis ve Mongo çağrısı yapıyordu | Lobiler çağrılardan önce atlanıyor | [runtimeRecovery.go](../../internal/infrastructure/realtime/runtimeRecovery.go) |

## HTTP ve kimlik doğrulama

### Rate limit IP'sinin sahte başlıkla belirlenmesi

Uygulama `ProxyHeader` olarak `X-Forwarded-For` kullanıyordu ve bu başlık doğrulanmadan okunuyordu. Yani `c.IP()` istemcinin kendi gönderdiği değer olabiliyordu. Her istekte farklı bir değer gönderen biri her istekte yeni bir rate limit anahtarı elde ederdi. Böylece login, şifre sıfırlama, forget ve sosyal kayıt tamamlama gibi IP'ye bağlı bütün sınırlar atlanabiliyordu. Bu arada sınırlayıcının bellekteki haritaları sınırsız büyüyordu; makineler 256 MB olduğu için bu ayrıca bellek riski demekti.

Şimdi `ProxyHeader: "Fly-Client-IP"` ve `EnableIPValidation: true` kullanılıyor. Bu başlığı Fly'ın proxy'si kendisi ayarlar; istemcinin gönderdiği değerin üzerine yazdığı staging'de doğrulanmalıdır (bkz. Deploy notları). Başlık yoksa (yerel geliştirme, Docker) uzak adres kullanılır. `/localization` grubundaki ikinci `IPRateLimit` gereksiz olduğu için kaldırıldı; genel API limiti zaten geçerli.

Dosyalar: [main.go](../../cmd/api/main.go), [routes.go](../../cmd/api/routes.go)

### Sıfırlama ve doğrulama kodlarına kaba kuvvet

E-posta ile gönderilen 6 karakterlik kod, e-posta adresi ve zaman penceresinden üretilen deterministik bir HMAC'tir ve mevcut ile önceki pencerede geçerlidir. Eski haliyle deneme sayısı, tek kullanım ve sabit zamanlı olmayan bir karşılaştırma yoktu. Bu da kodun çevrimiçi tahmin edilebilmesi demekti.

- Karşılaştırma `crypto/subtle` ile yapılıyor. `MatchResetCode` her iki pencereyi de sabit sürede karşılaştırır.
- Yeni `CodeAttempts` politikası her denemeyi tek bir atomik `FindOneAndUpdate` (upsert) ile sayar. Sayaç anahtarı amaç, e-posta ve pencereden oluşur. Pencere başına en fazla 5 deneme vardır; 6. deneme doğru kodla bile reddedilir.
- Sınır aşıldığında ayrı bir mesaj döner: `auth.error.too_many_code_attempts` ("Çok fazla deneme yapıldı. Lütfen birkaç dakika bekleyip yeni bir kod isteyin."). HTTP durumu aynıdır (400). Yanlış tahminlerle tükenmiş bir pencerede kod hiç karşılaştırılmaz, yani yanıt kodun doğru olup olmadığını belli etmez. İstemciler sunucu metnini aynen gösterdiği için uygulama güncellemesi gerekmez. Metinler migration 0068 ile eklenir.
- Yanıtı kaybolan başarılı bir şifre sıfırlama tekrar denendiğinde başarı döner: kod tüketilmişse, gönderilen kod gerçek kodsa ve gönderilen şifre zaten kayıtlı şifreyse hiçbir şey değiştirilmeden başarı yanıtı verilir. Bu yol yalnızca başarılı bir sıfırlamadan sonra açılır (`consumed` bayrağı), gerçek kodu şart koşar ve pencere başına en fazla 5 kez çalışır (bir kod iki pencerede geçerli olduğundan kod başına en fazla 10); yanlış tahminlerle tükenmiş bir pencerede devreye girmez. Rolling deploy sırasında eski bir instance'ta yapılan sıfırlamanın tekrarı bu kolaylıktan yararlanamaz, reddedilir.
- Amaçlar ayrı sayılır: `reset-password`, `verify-email`, `social-signup`. Böylece sıfırlama, aynı kodu gönderen doğrulama akışının hakkını tüketmez.
- Şifre sıfırlama başarılı olduğunda kod tüketilir. Kodun ait olabileceği mevcut ve önceki pencerelerin sayaçları tavana çekilir; kod bu pencerede de bir sonrakinde de tekrar kullanılamaz. Tüketme şifre değişmeden önce yapılır. Bu adımda hata olursa eski şifre kalır ve kod yine kullanılamaz.
- `AuthCodeAttempt` dokümanları `expiresAt` üzerindeki TTL indeksiyle kendiliğinden silinir (migration 0065).

Dosyalar: [codeAttempts.go](../../internal/application/auth/policies/codeAttempts.go), [authCodeAttempt.go](../../internal/data/entities/authCodeAttempt.go), [codeGenerator.go](../../internal/helpers/codeGenerator.go), [resetPassword.go](../../internal/application/auth/commands/resetPassword.go), [validateEmail.go](../../internal/application/auth/commands/validateEmail.go), [completeSocialSignUp.go](../../internal/application/auth/commands/completeSocialSignUp.go)

### JWT doğrulaması

Token doğrulaması algoritmayı ve `exp` alanını zorunlu tutmuyordu. Ayrıca `iat` alanı yoksa `IssuedAt` işaretçisi nil olabiliyor ve okuma panik yaratabiliyordu. Bilinen bir istismar yolu yoktu; bu bir savunma katmanıdır.

Doğrulama artık `jwt.WithValidMethods([]string{"HS256"})` ve `jwt.WithExpirationRequired()` ile yapılıyor. `iat` yoksa `IssuedAt` sıfır zaman olarak doldurulur.

Dosya: [jwt.go](../../internal/helpers/jwt.go)

### Swagger'ın production'da korunması

Swagger UI production'da da herkese açıktı ve admin rotalarının dokümantasyonunu gösteriyordu.

Production dışında (`APP_ENV != "production"`) eskisi gibi açık. Production'da HTTP Basic Auth ister: tarayıcı e-posta ve şifre sorar, yalnızca rolü 2 (`SuperAdmin`) olan hesap girebilir. Diğer herkes 401 alır.

- Doğrulama mevcut login komutundan geçer. Bu yüzden yanlış şifreler hesap kilidi sayacına işler (10 yanlış denemede hesap kilitlenir) ve kilitli ya da şifresiz (yalnızca sosyal girişli) hesaplar giremez.
- Rota ayrıca IP başına 50 istek / dakika limitinin arkasındadır.
- Tarayıcı kimlik bilgisini her istekte gönderir; her istek şifreyi yeniden doğrular. Kimlik bilgisi sunucuda saklanmaz.

Dosyalar: [swagger.go](../../cmd/api/swagger.go), [swaggerAuthMiddleware.go](../../internal/infrastructure/middleware/swaggerAuthMiddleware.go)

### Girdi uzunluk sınırları

Sınırlar şöyle: duyuru başlığı en fazla 200, açıklaması 2000 karakter; profil adı ve soyadı 50; `imageUrl` ve `houseProfileImage` 2048 karakter. Ad alanlarına minimum uzunluk eklenmedi, çünkü sosyal girişle açılan hesaplarda ad kısa ya da boş olabilir.

Dosyalar: [announcementModels.go](../../internal/models/dtos/announcementModels.go), [houseModels.go](../../internal/models/dtos/houseModels.go), [userModels.go](../../internal/models/dtos/userModels.go)

### `/auth/isAuth` limiti

Uygulama açılışta ve her `user.changed` olayında `/auth/isAuth` çağırıyor. Bu uç sıkı auth limitinin (10 istek / 15 dakika, IP başına) altındaydı. Aynı IP'yi paylaşan ev arkadaşları 429 alabiliyordu.

Rota artık `/auth` grubunun önünde kayıtlı; yalnızca genel API limiti (IP başına 50 istek / dakika) uygulanıyor. Yanıt değişmedi.

Dosya: [routes.go](../../cmd/api/routes.go)

### Yerelleştirme önbelleği

Başlangıçta yalnızca İngilizce yükleniyordu. İngilizce dışındaki her önbellek kaçırması, hata yanıtlarında (429, 401, doğrulama) mesaj parçası başına Mongo'da önbelleksiz bir sorgu demekti. `de-de` gibi desteklenmeyen diller de bu yolu tetikliyordu.

Şimdi başlangıçta `en` ve `tr` yükleniyor. Mongo yalnızca desteklenen dillerde sorgulanıyor. Bir çeviri hiç yoksa boş değer olarak önbelleğe alınır, böylece her anahtar bir kez sorgulanır. Kayıtlı boş bir çeviri İngilizceye düşer.

Dosyalar: [cache.go](../../internal/infrastructure/localization/cache.go), [localization.go](../../internal/helpers/localization.go)

### SMTP gönderimi

Doğrulama ve sıfırlama kodu e-postası istek içinde gönderiliyordu; bağlantı ve I/O için zaman aşımı yoktu. HTML şablonu her gönderimde yeniden parse ediliyordu.

Artık bağlantı kurulumu 10 saniye, bağlantının toplam süresi 20 saniye ile sınırlı. `smtp.SendMail` yerine aynı adımları (STARTTLS, AUTH, MAIL, RCPT, DATA) izleyen `sendMail` kullanılıyor. Şablon `sync.OnceValues` ile bir kez parse ediliyor.

Dosya: [notificationService.go](../../internal/services/notificationService.go)

### Yanıt sıkıştırma

Fiber `compress` middleware'i en hızlı seviyede (`LevelBestSpeed`) eklendi. Websocket yükseltmeleri atlanır. Sıkıştırma yalnızca istemci `Accept-Encoding` gönderdiğinde uygulanır. `go.mod` değişmedi.

Dosya: [main.go](../../cmd/api/main.go)

### App event'leri

`HouseChanged` ve `UsersChanged`, Redis bus'ı kullanılamazken bile her mutasyon komutundan sonra bir ev sorgusu yapıyor ve 750 ms publish zaman aşımını bekliyordu.

Bus `Available()` değilken olaylar yine denenir, ama 750 ms yerine 100 ms'lik bütçeyle (`publishContext`). Tamamen atlamak yanlıştı: bu bayrak herhangi bir Redis çağrısı başarısız olduğunda düşer ve başka trafik yoksa ancak 5 saniyelik heartbeat ile düzelir; yani tek bir geçici hata, Redis sağlıklıyken bile olayları saniyelerce düşürürdü. Şimdi geçici hatada olay kaybolmaz. Bilinen sınır: 100 ms bütçesi yalnızca bağlantı havuzu beklemesini, yeni bağlantı kurmayı ve yeniden denemeyi sınırlar. Redis istemcisinde `ContextTimeoutEnabled` açık olmadığı için, açık bir bağlantı üzerinde yanıt vermeyen (asılı kalan) bir Redis'te tek bir publish 3 saniyelik okuma zaman aşımına kadar bekleyebilir. Redis bağlantıyı reddediyorsa çağrı hemen döner. Ayrıca bu modda ev sorgusu da aynı 100 ms bütçeyi paylaşır.

App socket'leri de aynı bayrağa bakıyordu: 5 saniyelik kontrolde bayrak bir kez false görülürse instance'taki tüm app socket'leri koparılıyordu. Artık art arda 3 kontrol (`healthFailureThreshold`) gerekir; arada bir başarılı okuma sayacı sıfırlar. Abonelik hatası veya olay kanalının kapanması yine anında koparır.

Dosya: [service.go](../../internal/infrastructure/appEvents/service.go)

## Veritabanı

### Eksik indeksler (migration 0064)

Migration yalnızca ekleme yapar; mevcut hiçbir indeks silinmedi.

- `Chore {status, dueDate, _id}` (`idxChoreStatusDueDate`): görev deadline taraması için.
- `ChoreStatusHistory {choreId, dateTime, _id}` (`idxChoreStatusHistoryChoreIdDateTime`): `/house/details` geçmişleri bellekte değil indeksten sıralayabilsin diye.
- `ChoreReviewVote {houseId}` ve `GameMatchResult {houseId}`: hesap silme, bir transaction içinde bu koleksiyonlarda `DeleteMany {houseId: $in}` çalıştırıyordu ve kullanılabilir bir indeks yoktu.

Dosya: [0064_queryIndexes.go](../../internal/data/migrations/0064_queryIndexes.go), [registry.go](../../internal/data/migrations/registry.go)

### Görev deadline taraması

Her 60 saniyede her instance `{dueDate}` üzerinden çalışıyordu. Yalnızca bu alan indekslendiği için geçmişte biten görevler dahil geçmişteki bütün görevler okunup bellekte sıralanıyordu.

Şimdi dakikalık tur yalnızca açık görevleri (Draft, Progress, InTest) yeni indeksle tarıyor. Tam eski filtre, yani son tarihten sonra tamamlanmış görevler, süreç başında bir kez çalışıyor. Mevcut kod bu durumu üretemez; yalnızca eski veri için gerekli. Her periyodik tur 50 saniyelik bir zaman aşımına sahip. Herkese açık `Reconcile` fonksiyonu eski davranışını koruyor.

Dosya: [deadlineService.go](../../internal/application/chore/deadlineService.go)

## Realtime ve oyunlar

### Decompression bomb

Websocket okuma sınırı (16 KiB) sıkıştırılmış baytları sayıyordu. `permessage-deflate` ile tek bir frame bellekte yaklaşık 17 MB'a açılabiliyordu; 256 MB makinelerde bu ciddi bir risk.

`readMessage` açılmış mesajı da aynı sınırla okur. Sınırı aşan mesaj 1009 koduyla bağlantıyı kapatır.

Dosya: [gatewayV2.go](../../internal/infrastructure/realtime/gatewayV2.go)

### Kullanıcı başına bağlantı sınırı

Bağlantı sınırı yalnızca instance genelindeydi; tek bir hesap bütün realtime slotlarını doldurabiliyordu.

Artık bir kullanıcı bir instance'da en fazla 4 bağlantı tutabilir. Bekleyen yükseltmeler de sayılır. Aşan istek mevcut 503 ve `Retry-After` kapasite yanıtını alır. App events socket'indeki sınır da aynı değerdeydi.

Dosyalar: [admission.go](../../internal/infrastructure/realtime/admission.go), [gateway.go](../../internal/infrastructure/realtime/gateway.go)

### Snapshot isteğiyle maçın iptali

Snapshot istekleri izleyiciler de dahil herhangi bir socket'ten gelebilir. Bu istekler, olay kuyruğu (64 slot) dolduğunda maçı iptal eden yoldan geçiyordu. Yani bir izleyici kuyruğu doldurarak maçı durdurabilirdi.

Artık `houseTanks`, `houseRockets` ve `fausSwitch` runtime'larında kuyruk yarıdan fazla doluyken snapshot isteği `ErrRuntimeBusy` ile reddediliyor. Bu yanıt yeniden denenebilir. Snapshot hiçbir durumda maçı iptal etmez. Simülasyon olayları ve sonuçları değişmedi; `fausSwitch` tarafındaki Swift bit eşleşmesi korunuyor.

İstemcinin kendi gönderdiği `<oyun>.resync` isteği bu durumda reddedilmez: gateway, bağlantının zaten elinde olan son frame'i (normalde en fazla bir snapshot periyodu eski) isteğin `messageId`'siyle yanıt olarak gönderir. Sebep, mobil istemcinin reddedilen bir resync'te socket'i kapatıp yeniden bağlanması; yeniden bağlanmanın bind olayı da aynı dolu kuyruğa yük bindirir. Oda başka bir instance'ta çalışıyorsa sahibin reddi olay olarak gelir ve aynı şekilde karşılanır. Bağlantının elinde henüz frame yoksa eski davranış (yeniden denenebilir ret) geçerlidir.

Dosyalar: [gatewayV2.go](../../internal/infrastructure/realtime/gatewayV2.go), [houseTanks runtime](../../internal/application/game/games/houseTanks/runtime.go), [houseRockets runtime](../../internal/application/game/games/houseRockets/runtime.go), [fausSwitch runtime](../../internal/application/game/games/fausSwitch/runtime.go)

### Kopuk oda hub'ı

Bir oda hub'ının Redis aboneliği koptuğunda hub kayıtlı kalıyordu. Yeniden bağlanan istemciler bu ölü hub'a giriyor ve hiçbir olay almıyordu. Abonelik bağlantısı da sızıyordu.

Artık hub önce kayıttan çıkarılır, sonra durdurulur, ardından istemciler bağlantıdan düşürülür. Yeniden bağlanan istemci taze bir hub alır.

Dosya: [gateway.go](../../internal/infrastructure/realtime/gateway.go)

### Kurtarma tarayıcısı

Her turda boştaki her lobi için önce Redis, sonra Mongo çağrısı yapılıp oturum atlanıyordu.

Artık tarama oturum durumunu da döndürüyor. Lobi durumundaki oturumlar Redis ve Mongo çağrılarından önce atlanır. Lobiden yeni çıkmış bir oturum bir sonraki turda yakalanır. Çalışan tur tabanlı oyunlar ise kimse bağlı olmasa da turlarını işletmek için bir sahibe ihtiyaç duyar; bu yüzden onlar atlanmaz.

Dosyalar: [runtimeRecovery.go](../../internal/infrastructure/realtime/runtimeRecovery.go), [activeGameSessionScanner.go (data)](../../internal/data/database/activeGameSessionScanner.go), [activeGameSessionScanner.go (application)](../../internal/application/game/abstract/activeGameSessionScanner.go)

### Zaman aşımsız Mongo çağrıları

Komut yönlendirme döngüsü ve oda deadline'ında durum yeniden yükleme çağrıları zaman aşımsızdı. Bir yavaş işlem bütün odaların komutlarını durdurabilirdi.

Artık ikisi de mevcut `CommandTimeout` ile çalışıyor. Süre dolan yönlendirme başarısız sayılır ve teslimat ack edilmediği için normal yeniden teslim yoluna girer.

Dosyalar: [roomManager.go](../../internal/infrastructure/realtime/roomManager.go), [room.go](../../internal/infrastructure/realtime/room.go)

## İstemci ve operasyon tarafında fark edilebilecekler

- Swagger production'da e-posta ve şifre sorar; yalnızca SuperAdmin hesabı açabilir. Swagger dokümanı yeni `max` etiketleri için yeniden üretilmedi.
- Bir e-posta ve amaç için aynı pencere içinde 5'ten fazla kod denemesi reddedilir; doğru kod da dahil. E-posta adresini bilen biri, o kullanıcının sıfırlama veya doğrulamasını pencere dolana kadar geçici olarak kilitleyebilir. Bu, hesap ele geçirmeye karşı kabul edilen bir takastır.
- Aynı kullanıcının aynı instance'a 5. eşzamanlı oyun socket'i 503 ve `Retry-After` alır.
- Sınırları aşan duyuru, ad veya resim URL'si artık doğrulama hatası döner.
- IP bazlı rate limit'ler ve loglardaki IP artık gerçek istemci IP'sidir. Önceki dönemle karşılaştırma yapılırken bu fark göz önünde bulundurulmalı.
- İstemci `Accept-Encoding` gönderiyorsa yanıtlar sıkıştırılır.
- Kayıtlı boş bir çeviri İngilizceye düşer. Başka bir instance'ta eklenen çeviri, bu instance bir sonraki önbellek yüklemesinde görünür.

## Deploy notları

- Migration'lar 0064, 0065 ve 0068 mevcut Fly release komutu ile çalışır. Üçü de yalnızca ekleme yapar. Eski instance'lar etkilenmez; geri alınırsa fazla indeksler zararsız olarak kalır.
- Rolling deploy sırasında eski instance'lar kod deneme sınırını ve yeni kapasite sınırlarını uygulamaz. Bu korumalar tüm instance'lar değiştirilene kadar tam değildir.
- Staging'de tek doğrulanacak şey: istemcinin gönderdiği `Fly-Client-IP` başlığının Fly proxy'si tarafından üzerine yazıldığı. Sabit bir IP'den her istekte farklı bir sahte `Fly-Client-IP` değeriyle istek atın ve `X-RateLimit-Remaining` değerinin her seferinde düştüğünü izleyin. Değer her istekte yeniden dolu geliyorsa başlık güvenilmiyor demektir.
- İstek ve yanıt şemaları değişmedi. İstemcinin görebileceği iki davranış farkı var: yeni hata anahtarı `auth.error.too_many_code_attempts` (aynı 400 durumu) ve meşgul runtime'da resync'in ret yerine snapshot ile yanıtlanması.

## Bilerek yapılmayanlar ve karar bekleyenler

| Konu | Neden yapılmadı | Gereken karar |
|---|---|---|
| Git geçmişindeki JWT secret ve Appwrite anahtarları | Eski `config.json` (Nisan 2026'da takipten çıkarıldı) geçmişte duruyor. Kod değişikliğiyle çözülmez. Secret değeri bu belgeye yazılmadı. | Production'ın farklı bir secret kullandığı doğrulanacak; değilse secret döndürülüp eski anahtar iptal edilecek. |
| Oturum iptali: 72 saatlik token'lar şifre sıfırlamayla geçersiz olmuyor | Her istekte veritabanı okuması gerekir; bu, belgelenmiş "yalnızca JWT" middleware kararına aykırı. | Bu kararın değişip değişmeyeceği ve riskin kabulü. |
| Hesap kilidi: 10 yanlış şifre hesabı şifre sıfırlamaya kadar pasifleştiriyor | E-posta adresini bilen herkes hesabı kilitleyebiliyor. Geçici kilide çevirmek ürün davranışını değiştirir. | Kilit süresi ve davranışı. |
| Doğrulanmamış e-postalı hesaba sosyal bağlama (ele geçirme yolu) | Düzeltme istemciye yeni bir hata döndürür. | Yeni hata kodunun istemci tarafında kabul edilmesi. |
| Görev düzenleme kuralları: her üye her durumda atanan kişiyi ve son tarihi değiştirebiliyor | Ürün kuralı ve yeni hata gerektirir. | Hangi üyelerin, hangi durumlarda değiştirebileceği. |
| Hesap numaralandırma: login ve forget mesajları farklı | Metinler değişince uygulamanın gösterdiği mesajlar da değişir. | Mesajların tek tip yapılması kabul edilecek mi. |
| `/house/details` tüm görevleri, geçmişleri ve oyları sayfalamadan döndürüyor | Mobil istemci sözleşmesinde değişiklik gerektirir. | Sayfalama için mobil sürüm planı. |
| houseTanks power-up tohumu herkese açık session ID'den türetiliyor | İki aşamalı bir deploy olmadan rolling-safe değil. | İki aşamalı deploy planının onaylanması. |
| Oyun girdileri için instance'lar arası owner önbelleği | Sahiplik değiştikten sonra yaklaşık bir saniye boyunca girdiler sessizce düşebilir. | Şimdilik yapılmıyor; gerekirse ayrı bir çalışma. |
| Küçük maddeler: Docker non-root kullanıcı ve sabit base image, flyctl action'ının commit'e sabitlenmesi, komut yeniden teslim sınırı, houseTanks frame başı checkpoint kodlaması, ev başına görev sınırı | Düşük değer, düşük olmayan risk. | Ayrı bir iş olarak sıraya alınıp alınmayacağı. |

## Testler

Yeni eklenen veya genişletilen testler:

- [authCodeAttemptsIntegration_test.go](../../tests/account/authCodeAttemptsIntegration_test.go): sıfırlama, doğrulama ve sosyal kayıt için deneme sınırı, sıfırlamadan sonra kodun tekrar kullanılamaması, paralel denemelerin limiti aşamaması.
- [localizationCache_test.go](../../tests/architecture/localizationCache_test.go): eksik bir çevirinin bir kez sorgulanması.
- Architecture testleri: [rateLimitMiddleware_test.go](../../tests/architecture/rateLimitMiddleware_test.go) (rate limit ve isAuth yerleşimi), [security_contracts_test.go](../../tests/architecture/security_contracts_test.go) (JWT seçenekleri, DTO `max` etiketleri), [code_generator_contracts_test.go](../../tests/architecture/code_generator_contracts_test.go) (kod eşleştirme).
- Realtime testleri: [realtimeCompressionIntegration_test.go](../../tests/realtime/realtimeCompressionIntegration_test.go) (açılmış mesaj sınırı), [realtimeCapacityIntegration_test.go](../../tests/realtime/realtimeCapacityIntegration_test.go) (kullanıcı başına sınır), [roomHub_test.go](../../internal/infrastructure/realtime/roomHub_test.go) (abonelik hatasında hub'ın kaldırılması ve durdurulması).
- Snapshot taşması testleri: [houseTanksRuntime_test.go](../../tests/games/houseTanks/houseTanksRuntime_test.go), [houseRocketsRuntime_test.go](../../tests/games/houseRockets/houseRocketsRuntime_test.go), [fausSwitch runtime_test.go](../../internal/application/game/games/fausSwitch/runtime_test.go).
- [swaggerAuth_test.go](../../tests/architecture/swaggerAuth_test.go): Swagger'a yalnızca gerekli rolün girebilmesi.
- [service_test.go](../../internal/infrastructure/appEvents/service_test.go): bus kullanılamaz görünürken olayların yine denenmesi, kesintide kısa bütçe ve app socket'lerinin ancak art arda 3 başarısız kontrolde koparılması.
- [gameSessionPersistenceIntegration_test.go](../../tests/gamecore/gameSessionPersistenceIntegration_test.go): kurtarma tarayıcısının oturum durumu.

Çalıştırma:

```bash
scripts/testAffected.sh --base HEAD
```

- Varsayılan karşılaştırma `master` dalıdır. Bu değişiklikleri kapsamak için `--base HEAD` gerekir; bu komut commit'lenmemiş ve izlenmeyen dosyaları da alır.
- `--dry-run` ile hangi paketlerin çalışacağı, neden seçildiği çalıştırmadan görülebilir.
- Entegrasyon testleri MongoDB replica set ve Redis ister. `HOUSEFLOW_TEST_MONGO_URI` tanımlı değilse script geçici `mongod` ve `redis-server` süreçleri başlatır (Homebrew). `--no-services` ile bu atlanır ve entegrasyon testleri kendiliğinden atlanır.
- Tam test paketi PR aşamasında CI'da çalışır.
