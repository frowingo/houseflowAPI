# Abonelik (Premium) ve İşletme evleri — RevenueCat analizi ve backend planı

Tarih: 21 Kasım 2026.

Durum: **Analiz / taslak.** Kod yazılmadı. Bölüm 14'teki açık kararlar
netleşince fazlara (bölüm 13) geçilir. Mimari karar uygulama başladığında
ADR-033 olarak `architecture-decisions.md`'ye özetlenir.

Güncelleme (21 Kasım 2026): İndirimler, kodlar ve süreli hediye premium
eklendi (bölüm 16). Plana etkisi bölüm 16.9'da özetlenir.

## 1. Amaç

- Ücretsiz kullanıcı uygulamanın tüm işlevlerini kullanır; ekran aralarında
  reklam görür, yalnız 2 temel tema ve ücretsiz görsel listesini kullanır.
- Premium kullanıcı hiç reklam görmez, ek temaları ve premium görsel
  listelerini (user profil, house profil) kullanır.
- İki plan vardır:
  - **Kişisel (Personal):** Satın alanın kendisi her yerde premium olur.
  - **İşletme (Business):** Satın alan, sahibi olduğu **İşletme tipindeki
    evi** "sponsorlar". O evin bütün üyeleri, **yalnız o ev bağlamındayken**
    premium olur.
- Satın alma ve mağaza işleri RevenueCat ile yapılır. Backend, **etkin
  premium**un tek doğruluk kaynağıdır; client'ın "ben premiumum" beyanına
  güvenilmez.
- Test için ve seçilen kullanıcılara süreli indirim tanımlanabilir: %100
  (hediye/test), %50, %10 gibi (bölüm 16).

## 2. Mevcut durumdan çıkan bulgular

Plana doğrudan etki eden noktalar:

| # | Bulgu | Yer | Etkisi |
|---|---|---|---|
| B1 | `UserRole` (Normal/Admin/SuperAdmin) sistem yetki rolü; JWT'ye gömülü ve 72 saat geçerli. `Admin=1` hiçbir yerde kullanılmıyor. | [user.go](../../internal/data/entities/user.go), [jwt.go](../../internal/helpers/jwt.go) | Abonelik bu role **karıştırılmamalı** (bölüm 3). |
| B2 | `HouseType` = 1 Student, 2 Shared, 3 Dorm. DTO doğrulaması `oneof=1 2 3`. Mongo validator yalnız `int` istiyor, enum yok. | [house.go](../../internal/data/entities/house.go), [houseModels.go](../../internal/models/dtos/houseModels.go) | `4 = Business` eklenebilir; DB migration gerekmez. Eski client'lar 4'ü çözemeyebilir (bölüm 12.2). |
| B3 | Ev tipi, sahibin `PUT /house/profile` ile serbestçe değiştirebildiği bir alan. | [updateHouseProfile.go](../../internal/application/house/commands/updateHouseProfile.go) | İşletme tipine geçiş/çıkış bu yoldan **kapatılmalı**. |
| B4 | Sahip evden çıkınca sahiplik otomatik olarak ilk üyeye geçer. Hesap silmede de aynısı olur. | [exitHouse.go](../../internal/application/house/commands/exitHouse.go), [accountRemoval.go](../../internal/application/user/policies/accountRemoval.go) | Sponsorluğun sahiplikle **otomatik devredilmemesi** gerekir (bölüm 6.3). |
| B5 | `User.ImageURL` serbest string (`max=2048`). `House.ProfileImage` serbest URL. Hiçbiri görsel kataloğuna karşı doğrulanmıyor. | [updateProfile.go](../../internal/application/user/commands/updateProfile.go), [houseModels.go](../../internal/models/dtos/houseModels.go) | Premium görseller şu an istemci tarafında bile atlatılabilir; **katalog doğrulaması şart** (bölüm 9). |
| B6 | `ImageAsset` = `category`, `fileUrl`, `publicId`, `isActive`. Kademe (tier) bilgisi yok; liste bellek içi cache'li. | [imageAsset.go](../../internal/data/entities/imageAsset.go), [getImagesByCategory.go](../../internal/application/imageAsset/queries/getImagesByCategory.go) | `tier` alanı eklenir, cache anahtarı aynı kalır. |
| B7 | Tema kavramı backend'de yok. | — | Tema tamamen client'ta kalır; backend yalnız hakkı söyler (bölüm 10). |
| B8 | `MaxMemberCount` `1..8`. | [houseModels.go](../../internal/models/dtos/houseModels.go) | İşletme evleri için daha yüksek limit bir premium avantajı olabilir (açık karar K6). |
| B9 | Asynq tabanlı iş kuyruğu (ADR-032), FCM push ve realtime gateway hazır. | `internal/application/jobs`, `internal/infrastructure/push` | Webhook işleme ve "premium değişti" bildirimi mevcut altyapıyla yapılır. |
| B10 | App sürümüne göre davranış (min version) yok. | — | `HouseType=4`'ü eski client'lara göstermek riskli (bölüm 12.2). |

## 3. Terminoloji — "rol" değil "hak"

İstenen "abonelik rolü" `UserRole`'e eklenmemeli:

- `UserRole` yetkilendirme içindir (SuperAdmin endpoint'leri). Abonelik ise
  zamana bağlı, mağazadan gelen ve her an değişebilen bir durumdur.
- JWT 72 saat geçerli. Premium JWT'ye yazılırsa iptal, iade ya da süre bitimi
  token yenilenene kadar görünmez.
- İşletme premium'u **bağlama göre** değişir: aynı kullanıcı A evinde premium,
  B evinde değildir. Tek bir rol alanı bunu ifade edemez.

Bu yüzden şu kavramlar kullanılır:

| Kavram | Anlamı |
|---|---|
| **Plan** | Satın alınan ürün ailesi: `personal` veya `business`. |
| **Entitlement** | RevenueCat'teki hak: `premium_personal`, `premium_business`. |
| **Sponsor** | Aktif `premium_business` hakkına sahip ve bir İşletme evine bağlanmış kullanıcı. |
| **İşletme admini** | İşletme evinin sahibi (`house.ownerId`). Yeni bir `UserRole` değil; ev üzerindeki sahiplik. |
| **Etkin premium** | `(kullanıcı, ev bağlamı)` için hesaplanan sonuç ve kaynağı: `personal`, `businessOwner`, `businessMember` ya da yok. |

## 4. İş kuralları

**R1. Kişisel premium.** `premium_personal` aktifse kullanıcı her evde ve ev
bağlamı dışında (ör. profil ekranı) premium.

**R2. İşletme sahibinin kendisi.** İşletme ürünleri RevenueCat'te **hem
`premium_business` hem `premium_personal`** entitlement'ına bağlanır. Böylece
işletme admini kendi kişisel evlerinde de premium olur. (Önerilen; açık karar
K1.)

**R3. Sponsorlu ev.** Bir ev şu üç koşul birlikteyken "premium ev" sayılır:

1. `house.type == Business (4)`;
2. `house.ownerId` kullanıcısının `premium_business` hakkı aktif;
3. o kullanıcının abonelik kaydında `businessHouseId == house.id`.

**R4. Üye.** Premium bir evin üyesi, **bağlam o ev olduğunda** premium
(`businessMember`). Başka evdeyken kendi kişisel durumuna döner.

**R5. Bir abonelik, bir ev.** Bir işletme aboneliği yalnız tek bir İşletme
evini sponsorlar. Aksi halde tek abonelikle sınırsız ev açılıp paylaşılabilir.
(Önerilen; açık karar K2.)

**R6. Sponsorluk sahipliğe bağlıdır ama devredilmez.** Sahiplik değişirse
koşul 2/3 kendiliğinden bozulur ve ev premium olmaktan çıkar. Yeni sahip
kendi aboneliğiyle yeniden sponsor olabilir.

**R7. Süre bitimi tembel değerlendirilir.** Etkin premium her okumada
`expiresAt > now` ile hesaplanır. Süre bitimi için cron veya toplu güncelleme
gerekmez; webhook yalnız kaydı tazeler.

**R8. Kozmetik seçimlerin akıbeti.** Premium biterse önceden seçilmiş premium
görsel **yerinde kalır**, ama yeni premium görsel seçilemez (grandfather).
(Önerilen; açık karar K4.)

### 4.1. Senaryo tablosu

| Kullanıcı | Bağlam | Sonuç | Kaynak |
|---|---|---|---|
| Kişisel premium | Herhangi bir ev | Premium | `personal` |
| İşletme admini (aktif, ev bağlı) | Kendi İşletme evi | Premium | `businessOwner` |
| İşletme admini (aktif) | Kendi kişisel evi | Premium | `personal` (R2 sayesinde) |
| İşletme evinin üyesi, ücretsiz | İşletme evi | Premium | `businessMember` |
| İşletme evinin üyesi, ücretsiz | Başka bir ev | Ücretsiz | — |
| İşletme evinin üyesi, kişisel premium | Herhangi bir ev | Premium | `personal` |
| İşletme evi, sahibin aboneliği bitti | İşletme evi | Ücretsiz | — |
| İşletme evi, sahip çıktı, yeni sahip abone değil | İşletme evi | Ücretsiz | — |

## 5. RevenueCat yapılandırması

### 5.1. Ürünler, entitlement'lar, offering'ler

| Mağaza ürünü (örnek id) | Entitlement'lar |
|---|---|
| `fauscrew_personal_monthly`, `fauscrew_personal_yearly` | `premium_personal` |
| `fauscrew_business_monthly`, `fauscrew_business_yearly` | `premium_business`, `premium_personal` |

- **App Store:** Dört ürün **aynı subscription group**'ta olmalı. İşletme
  ürünleri daha yüksek seviyede tutulur, böylece kişiselden işletmeye geçiş
  "upgrade" olur ve kullanıcı iki kez ödemez.
- **Google Play:** `personal` ve `business` iki subscription; her biri
  aylık/yıllık base plan. Plan değişimi client'ta `replacement mode` ile
  yapılır.
- **Offering'ler:** `default` (kişisel paketler) ve `business` (işletme
  paketleri). Client, bağlama göre hangisini göstereceğini seçer (İşletme evi
  oluşturma akışında `business`). İstenirse RevenueCat Paywalls
  kullanılabilir.
- **Aile Paylaşımı (Family Sharing):** İşletme ürünlerinde kapalı tutulması
  önerilir. Paylaşılmış alımlar webhook'ta `FAMILY_SHARED` sahiplik tipiyle
  gelir; sponsorluk yalnız satın alana (`PURCHASED`) verilir.

### 5.2. App User ID

- RevenueCat `app_user_id` = bizim `User.Id` (Mongo hex). Client girişte
  `Purchases.logIn(userId)`, çıkışta `Purchases.logOut()` çağırır.
- Uygulama zaten girişe zorunlu, bu yüzden paywall girişten önce
  gösterilmemeli. Anonim (`$RCAnonymousID`) alım olmaz.
- Backend, webhook'ta `app_user_id` bizim bir kullanıcımız değilse
  (anonim ya da bilinmeyen id) olayı kaydeder ama hak vermez.

### 5.3. Restore / transfer davranışı

Projede **"Transfer if there are no active subscriptions"** önerilir:

- Aynı Apple/Google hesabıyla aktif bir abonelik ikinci bir FausCrew
  hesabına taşınamaz. Bir aboneliği birden çok hesapta kullanma engellenir.
- Süresi bitmiş alımlar taşınabilir; kullanıcı yeni hesapta yeniden abone
  olabilir.
- Bilinen iOS kısıtı: Aynı Apple hesabı başka bir FausCrew hesabında yeniden
  satın alırsa alım eski app user id'ye bağlanabilir. Client bu durumda
  "Bu Apple hesabındaki abonelik başka bir FausCrew hesabına bağlı" mesajını
  göstermeli.
- Her durumda `TRANSFER` webhook'u işlenir (bölüm 7.3).

### 5.4. Webhook

- URL: `POST /api/webhooks/revenuecat`.
- Dashboard'da **Authorization header** tanımlanır (ör.
  `Bearer <REVENUECAT_WEBHOOK_SECRET>`). Backend her istekte sabit zamanlı
  karşılaştırma yapar; header yoksa ya da yanlışsa `401`.
- RevenueCat 200 dışındaki yanıtları birkaç kez üstel geri çekilmeyle tekrar
  dener ve 60 saniyeyi aşan yanıtı başarısız sayar. Bu yüzden handler olayı
  kaydedip kuyruğa atar ve **hemen 200** döner.
- Ayrı ortamlar: staging ve production için ayrı webhook. Olaydaki
  `environment` (`SANDBOX`/`PRODUCTION`) alanı bölüm 7.4'teki kurala göre
  süzülür.

### 5.5. REST API

- Webhook olayının içeriğine tek başına güvenilmez; olaylar sırasız ve
  tekrarlı gelebilir. Kanonik durum her seferinde RevenueCat'ten çekilir:
  - API v1: `GET /v1/subscribers/{app_user_id}` (entitlement'ları,
    `expires_date` ve `grace_period_expires_date` ile birlikte döner), ya da
  - API v2: `GET /v2/projects/{project_id}/customers/{customer_id}/active_entitlements`
    (+ abonelik ayrıntısı için `.../subscriptions`).
- Uygulamada hangisinin kullanılacağına implementasyon sırasında güncel
  dokümana bakılarak karar verilir. Adaptör arayüzü (bölüm 11) ikisini de
  saklar.
- Secret API key (`sk_...`) yalnız backend'de durur.

## 6. Veri modeli

### 6.1. Yeni collection: `UserSubscription` (kullanıcı başına bir doküman)

```go
type SubscriptionEntitlement struct {
	Key            string     `bson:"key"`            // premium_personal | premium_business
	ProductId      string     `bson:"productId"`
	Store          string     `bson:"store"`          // app_store | play_store | ...
	OwnershipType  string     `bson:"ownershipType"`  // PURCHASED | FAMILY_SHARED
	PeriodType     string     `bson:"periodType"`     // normal | trial | intro
	ExpiresAt      *time.Time `bson:"expiresAt"`      // grace period dahil; nil = süresiz
	WillRenew      bool       `bson:"willRenew"`
	BillingIssueAt *time.Time `bson:"billingIssueAt,omitempty"`
	IsSandbox      bool       `bson:"isSandbox"`
}

type UserSubscription struct {
	Id                   primitive.ObjectID        `bson:"_id,omitempty"`
	UserId               string                    `bson:"userId"`        // unique
	Entitlements         []SubscriptionEntitlement `bson:"entitlements"`
	BusinessHouseId      string                    `bson:"businessHouseId,omitempty"`
	BusinessHouseBoundAt *time.Time                `bson:"businessHouseBoundAt,omitempty"`
	ManagementURL        string                    `bson:"managementUrl,omitempty"`
	LastEventAt          time.Time                 `bson:"lastEventAt"`
	LastSyncedAt         time.Time                 `bson:"lastSyncedAt"`
	Version              int64                     `bson:"version"`
	CreatedOn            time.Time                 `bson:"createdOn"`
	UpdatedOn            time.Time                 `bson:"updatedOn"`
}
```

İndeksler: `userId` unique; `businessHouseId` unique + sparse (R5: bir ev tek
sponsor).

`User` dokümanına alan **eklenmez**. Abonelik ayrı collection'da kalır:
kullanıcı okumaları değişmez, rollback güvenli olur.

### 6.2. Yeni collection: `SubscriptionEvent`

Ham webhook olayları (denetim ve tekrar işleme için):

- `eventId` (RevenueCat `event.id`) unique → idempotency;
- `type`, `appUserId`, `environment`, `eventAt`, `receivedAt`,
  `processedAt`, `payload` (ham JSON);
- `receivedAt` üzerinde TTL (ör. 180 gün).

### 6.3. `House`

- `HouseType` için `Business HouseType = 4`.
- Yeni alan yok. Sponsorluk `UserSubscription.businessHouseId`'den okunur.
  Ev listelerinde performans gerekirse ileride `house.premiumSponsorId`
  denormalize edilebilir. İlk sürümde gerek yok: kullanıcının en fazla
  birkaç evi olur ve sahiplerin abonelikleri tek `$in` sorgusuyla okunur.

### 6.4. `ImageAsset`

- Yeni alan: `tier string` (`free` | `premium`). Migration mevcut kayıtlara
  `free` yazar.
- `fileUrl` üzerine indeks (bölüm 9'daki doğrulama için).
- `ImageAssetResultModel`'e `isPremium bool` eklenir. Eski client'lar alanı
  yok sayar.

### 6.5. Migration'lar (sıra numarası uygulama anında kesinleşir)

| No | İçerik |
|---|---|
| 0071 | `UserSubscription` collection + indeksler |
| 0072 | `SubscriptionEvent` collection + `eventId` unique + TTL |
| 0073 | `ImageAsset.tier = "free"` backfill + `fileUrl` indeksi |
| 0074 | Lokalizasyonlar (`subscription.error.*`, `house.error.business_*`, push metinleri) |
| 0075 | `PremiumGrant` collection + `{userId, endsAt}` indeksi (bölüm 16.3) |
| 0076 | `DiscountCampaign`, `DiscountAssignment`, `AppleOfferCode` collection'ları + unique indeksler (16.4) |
| 0077 | `DiscountCode` (`codeDigest` unique) + indirim/kod lokalizasyonları (16.5) |

Hepsi ekleyici (additive). Eski backend sürümü yeni alanları/collection'ları
yok sayar; rollback güvenlidir.

## 7. Senkronizasyon akışı

### 7.1. Webhook → kuyruk → senkron

```
RevenueCat ──POST──▶ /api/webhooks/revenuecat
                      1. Authorization doğrula (401)
                      2. SubscriptionEvent insert (eventId duplicate → 200, bitti)
                      3. Enqueue fauscrew:subscription:sync {userId}
                         TaskID = "subscription-sync:<userId>" (kısa süreli dedupe)
                      4. 200
Worker ──▶ SyncUserSubscription(userId)
            a. RevenueCat REST'ten müşteri durumunu çek
            b. Entitlement'ları normalize et, UserSubscription upsert (version++)
            c. Önceki/sonraki etkin durumu karşılaştır
            d. İşletme hakkı değiştiyse: sponsorlu evin üyelerine
               realtime "house.premiumChanged" + sessiz push
            e. SubscriptionEvent.processedAt = now
```

- Kuyruk: `QueueCritical`. REST hatası tekrar denenir; bilinmeyen kullanıcı
  `ErrPermanent`.
- Yarış koşulu: İki sync aynı anda koşarsa ikisi de aynı kanonik durumu
  yazar. Sonuç her zaman RevenueCat'in o anki durumudur, olay sırasına
  bağlı değildir.

### 7.2. İstemci tetiklemeli senkron

Satın alma ya da restore'dan hemen sonra webhook birkaç saniye gecikebilir.
Client, `POST /api/subscription/sync` çağırır. Backend aynı
`SyncUserSubscription`'ı **senkron** çalıştırır ve güncel durumu döner.
Rate limit: kullanıcı başına dakikada birkaç istek.

### 7.3. Olay tiplerinin ele alınışı

Tüm tipler aynı sync'i tetikler; tip yalnız yan etki için okunur:

| Olay | Ek davranış |
|---|---|
| `INITIAL_PURCHASE`, `RENEWAL`, `UNCANCELLATION`, `PRODUCT_CHANGE` | Business hakkı yeni geldiyse ve kullanıcının tek İşletme evi varsa otomatik bağla (bölüm 8.3). |
| `CANCELLATION` | Hak `expiresAt`'e kadar sürer; yalnız `willRenew=false` olur. |
| `BILLING_ISSUE` | `billingIssueAt` yazılır; client "ödeme sorunu" uyarısı gösterir. |
| `EXPIRATION` | Hak düşer. Sponsor bağı **silinmez**: yeniden abone olunca ev kendiliğinden geri gelir. |
| `TRANSFER` | Olayda `transferred_from` / `transferred_to` id'leri gelir; **iki kullanıcı için de** sync kuyruğa atılır. |
| `SUBSCRIPTION_PAUSED` (Android) | Sync yeterli. |
| `TEST` | Loglanır, işlenmez. |

### 7.4. Sandbox / production

- TestFlight ve sandbox alımları `SANDBOX` olarak gelir.
- Staging backend: hepsini kabul eder.
- Production backend: `SANDBOX` olaylarını yalnız
  `REVENUECAT_SANDBOX_ALLOWED_USER_IDS` listesindeki kullanıcılar için işler,
  diğerlerini kaydedip yok sayar. Böylece mağaza incelemesi ve iç test
  canlıda çalışır, sandbox alımı gerçek kullanıcıya hak vermez.

## 8. Etkin premium hesaplama ve API

### 8.1. `EntitlementResolver` (application katmanı)

```go
type PremiumSource string // "", "personal", "businessOwner", "businessMember"

// ResolveForUser: kullanıcının kişisel durumu + her evi için etkin durum.
func (r *EntitlementResolver) ResolveForUser(ctx, userID) (*dtos.SubscriptionStatusModel, error)

// ResolveInHouse: tek bir (kullanıcı, ev) için; görsel doğrulaması kullanır.
func (r *EntitlementResolver) ResolveInHouse(ctx, userID, house *entities.House) (PremiumSource, error)
```

Algoritma (`ResolveInHouse`):

1. Kullanıcının `premium_personal` hakkı aktifse → `personal`.
2. `house.Type != Business` → yok.
3. `sub := UserSubscription(house.OwnerId)`. Hak aktif değilse ya da
   `sub.BusinessHouseId != house.Id` → yok.
4. `userID == house.OwnerId` → `businessOwner`, değilse → `businessMember`.

"Aktif" = `ExpiresAt == nil || ExpiresAt > now` ve
`OwnershipType == PURCHASED` (business için). Aktif `PremiumGrant`'lar da
hak sayılır; ayrıntı bölüm 16.8'de.

### 8.2. Endpoint'ler

**`GET /api/subscription/me`** — client'ın tek kaynağı:

```json
{
  "personal": {
    "isActive": true,
    "plan": "business",
    "productId": "fauscrew_business_yearly",
    "expiresAt": "2027-11-21T10:00:00Z",
    "willRenew": true,
    "hasBillingIssue": false,
    "store": "app_store",
    "managementUrl": "https://apps.apple.com/account/subscriptions"
  },
  "business": {
    "isActive": true,
    "houseId": "6650…",
    "canRebindAt": "2026-12-21T10:00:00Z"
  },
  "houses": [
    { "houseId": "6650…", "isPremium": true, "source": "businessOwner" },
    { "houseId": "6651…", "isPremium": true, "source": "personal" }
  ],
  "syncedAt": "2026-11-21T10:00:00Z"
}
```

**`POST /api/subscription/sync`** — bölüm 7.2. Yanıt `GET /me` ile aynı.

**`PUT /api/subscription/business-house`** `{ "houseId": "…" }`:

- İstek sahibi evin sahibi olmalı (`RequireOwner`), ev `Business` tipinde
  olmalı ve kullanıcının aktif `premium_business` hakkı bulunmalı.
- Yeniden bağlamada bekleme süresi (ör. 30 gün) uygulanır. Bu süre,
  aboneliği evden eve gezdirmeyi engeller.
- `businessHouseId` unique indeksi, aynı evin iki sponsoru olmasını engeller.

**`DELETE /api/subscription/business-house`** — bağı kaldırır (sahip evden
çıkmadan önce gerekir, bölüm 8.3).

**Ek alanlar (geriye uyumlu):**

- `GET /house/details` ve `GET /house/infos` yanıtına
  `premium: { "isActive": bool, "source": "…" }`.
- `GET /user/getImages` öğelerine `isPremium`.

### 8.3. Mevcut akışlara etkiler

| Akış | Değişiklik |
|---|---|
| `POST /house/create` | `type` doğrulaması `oneof=1 2 3 4`. Kullanıcının business hakkı varsa ve bağlı evi yoksa yeni İşletme evi otomatik bağlanır. Abonelik olmadan da İşletme evi açılabilir (açık karar K3). |
| `PUT /house/profile` | `houseType` hâlâ `oneof=1 2 3`. Ev `Business` ise tip değiştirilemez (`house.error.business_type_locked`). Normal ev İşletme'ye bu yoldan çevrilemez; gerekirse ileride ayrı bir "convert" komutu yazılır. |
| `POST /house/exit` (sahibin kendisi) | Ev `Business` ve sahibin `businessHouseId`'si bu evse → `409 house.error.business_sponsor_must_release`. Sahip önce bağı kaldırır ya da aboneliği başka eve taşır. Böylece premium sessizce kaybolmaz. |
| Sahibin üye çıkarması | Değişmez. Çıkarılan üye o evin premium'unu anında kaybeder (hesap tembel olduğu için ek iş yok). |
| Hesap silme | `UserSubscription.businessHouseId` temizlenir, doküman silinir. RevenueCat'te müşteri silinir (`DELETE /v1/subscribers/{id}`, KVKK/GDPR). **Mağaza aboneliği silinmez:** client, silme onayında "aktif aboneliğinizi App Store / Google Play'den iptal edin" uyarısını göstermeli (Apple hesap silme yönergesi). |
| `PUT /user/profile/:id` (`imageUrl`) | Bölüm 9. |
| `PUT /house/profile` (`houseProfileImage`) | Bölüm 9. |
| `MaxMemberCount` | İşletme evleri için üst sınır açık karar K6. |

## 9. Premium görsel listeleri

### 9.1. Kurallar

| Alan | Kim değiştirir | Premium görsel seçme şartı |
|---|---|---|
| House profil görseli | Ev sahibi (`RequireOwner`) | `ResolveInHouse(owner, house) != ""`. Kişisel premium sahip ya da sponsorlu İşletme evi. |
| User profil görseli | Kullanıcının kendisi | Yalnız **kişisel** premium (`premium_personal`, işletme admini dahil). `businessMember` yetmez, çünkü profil görseli her evde görünür. (Önerilen; açık karar K5.) |

### 9.2. Doğrulama

1. Gelen URL `ImageAsset.fileUrl` ile eşleştirilir (`isActive=true`). Client
   sözleşmesi değişmez, yine URL gönderir.
2. `tier == premium` ise 9.1'deki şart aranır; yoksa
   `403 subscription.error.premium_required`.
3. **Katalog dışı URL:** Bugün serbest. Kapatmak eski verisi olan
   kullanıcıları kırmaz, çünkü yalnız **yeni yazma** doğrulanır. Önerilen
   kural: katalog dışı URL reddedilir
   (`image_asset.error.not_in_catalog`). İstisna: değer değişmiyorsa (aynı URL
   tekrar gönderiliyorsa) kabul edilir.
4. Premium bittiğinde mevcut seçim kalır (R8). Masking (okurken varsayılan
   görsele düşürme) istenirse resolver'a eklenebilir; tüm üye listelerinde ek
   sorgu getirdiği için ilk sürümde önerilmez.

## 10. Reklam ve temalar (client tarafı)

Backend bunları zorlayamaz, yalnız hakkı bildirir:

- **Reklam:** Client, aktif ev bağlamı için `houses[].isPremium` (bağlam
  yoksa `personal.isActive`) false ise interstitial gösterir. AdMob + UMP
  (GDPR onayı) + iOS ATT client işidir. Reklam sıklığı ileride remote
  config'e taşınabilir.
- **Tema:** Temalar uygulama içi varlıklardır. Premium tema seçimi aynı
  kurala bağlıdır. `businessMember` kaynağıyla seçilen tema, kullanıcı
  premium olmayan bir eve geçince **varsayılan temaya döner**, İşletme evine
  dönünce geri gelir. Client seçimi yerelde saklar; hak olmadığı sürece
  uygulamaz.
- Temanın cihazlar arası senkronu istenirse ileride `User.preferredTheme`
  eklenir; şimdilik gerekmez.
- Client, RevenueCat SDK'nın `CustomerInfo`'sunu yalnız satın alma anındaki
  arayüz için kullanır (paywall'ı kapatmak gibi). Kalıcı karar her zaman
  `GET /subscription/me`'dendir, çünkü `businessMember` durumunu yalnız
  backend bilir.

## 11. Kod yerleşimi (mevcut CQRS-lite düzenine göre)

```
internal/data/entities/userSubscription.go, subscriptionEvent.go
internal/data/migrations/0071…0074
internal/application/subscription/
  abstract/revenueCatClient.go        // GetCustomer, DeleteCustomer (port)
  entitlementResolver.go              // bölüm 8.1
  commands/handleRevenueCatWebhook.go // doğrula + kaydet + enqueue
  commands/syncUserSubscription.go    // REST → upsert → bildirim
  commands/bindBusinessHouse.go, releaseBusinessHouse.go
  queries/getSubscriptionStatus.go
  jobs.go                             // fauscrew:subscription:sync handler
internal/infrastructure/revenueCat/client.go  // HTTP adaptörü (timeout, retry)
internal/adaptors/revenueCat.go               // config'ten kurulum, yoksa kapalı
internal/controllers/subscriptionController.go
internal/models/dtos/subscriptionModels.go
```

Değişen dosyalar: `house.go` (`Business`), `houseModels.go`,
`createHouse.go`, `updateHouseProfile.go`, `exitHouse.go`,
`accountRemoval.go`, `updateProfile.go`, `imageAsset.go`,
`imageAssetModels.go`, `routes.go`.

## 12. Güvenlik, uyumluluk, canlıya çıkış

### 12.1. Config ve secret'lar

| Env | Anlamı |
|---|---|
| `REVENUECAT_SECRET_API_KEY` | REST (sk_). Yoksa mağaza tarafı kapalıdır; yalnız `PremiumGrant`'lar geçerli olur (16.9). |
| `DISCOUNT_CODE_SECRET` | Android açık kodlarının HMAC digest anahtarı (16.5). |
| `OFFER_CODE_ENC_KEY` | Apple tek kullanımlık kodlarının şifreleme anahtarı (16.4). |
| `REVENUECAT_PROJECT_ID` | API v2 kullanılırsa. |
| `REVENUECAT_WEBHOOK_SECRET` | Authorization header değeri. |
| `REVENUECAT_SANDBOX_ALLOWED_USER_IDS` | Production'da sandbox kabul edilecek kullanıcılar. |
| `SUBSCRIPTION_BUSINESS_HOUSES_ENABLED` | İşletme evi oluşturmayı açan bayrak (12.2). |

Ek güvenlik kuralları:

- Webhook route'u JWT'siz ama IP rate limit'li olur. Gövde boyutu
  sınırlanır, strict JSON değil (RevenueCat alan ekleyebilir).
- Ham payload loglanmaz, yalnız `SubscriptionEvent`'e yazılır.

### 12.2. Eski client'lar ve `HouseType = 4`

Eski iOS/Android sürümleri `type: 4`'ü katı enum olarak çözüyorsa İşletme
evine katılan eski sürümlü kullanıcıda ev listesi bozulur. Öneri:

1. Önce bilinmeyen tipe dayanıklı ve abonelik arayüzünü içeren client
   sürümü yayınlanır.
2. Yeterli benimsemeden sonra `SUBSCRIPTION_BUSINESS_HOUSES_ENABLED=true`.
3. Gerekirse bir "minimum app version" mekanizması eklenir (şu an yok, B10).

Alternatif: tip 1–3'te kalır, ayrı bir `isBusiness` bayrağı eklenir. Bu daha
uyumludur ama "İşletme" ile "Öğrenci evi" gibi tipler birbirini dışlamadığı
için anlam karışır. Karar K7.

### 12.3. Rolling / rollback

- Bütün migration'lar ekleyicidir.
- Yeni endpoint'ler eski sürümde 404 döner; client buna dayanıklı olmalı
  (404 → ücretsiz kabul et).
- Rollback sonrası `type=4` evler eski backend'de okunur (DB validator
  yalnız `int`). Yalnız güncelleme doğrulaması 4'ü reddeder, bu kabul
  edilebilir.
- RevenueCat webhook'u rollback süresince 404 alır ve olayı tekrar dener.
  Kaçan olay olursa client'ın `POST /sync` çağrısı ve günlük uzlaştırma işi
  (13. bölüm, Faz S5) durumu düzeltir.

### 12.4. Mağaza yönergeleri (kontrol listesi)

- Dijital avantajlar (reklamsız kullanım, tema, görsel) IAP ile satılır.
  Harici ödeme bağlantısı yok.
- Paywall'da fiyat, süre, otomatik yenileme, Gizlilik ve Kullanım Koşulları
  bağlantıları ve "Restore Purchases" düğmesi bulunur.
- İşletme planının metni, avantajın **ev üyelerine o ev içinde**
  verildiğini açıkça yazar.
- Hesap silmede abonelik iptal uyarısı gösterilir (8.3).

## 13. Fazlar

| Faz | İçerik | Bitti sayılır |
|---|---|---|
| S0 | Açık kararlar (bölüm 14), mağaza ürünleri **ve teklif kataloğu** (16.4), RevenueCat projesi, entitlement/offering'ler | Dashboard hazır, sandbox'ta test alımı görünüyor |
| S1 | Entity'ler, migration 0071–0072 ve 0075, `EntitlementResolver` (mağaza + grant), **G: admin grant endpoint'leri**, RevenueCat adaptörü (+ fake), `SyncUserSubscription`, `GET /me`, `POST /sync` | Grant verilen kullanıcı ve sandbox alımı `GET /me`'de premium görünüyor |
| S2 | Webhook endpoint, `SubscriptionEvent`, Asynq job, transfer/sandbox kuralları | Webhook'tan gelen yenileme/iptal/bitiş doğru yansıyor |
| S3 | `HouseType=4`, bind/release, `ResolveInHouse`, ev yanıtlarına `premium`, exit/hesap silme kuralları, "premiumChanged" bildirimi | İşletme evi üyeleri yalnız o evde premium |
| S4 | `ImageAsset.tier` (0073), görsel doğrulaması, `isPremium`, lokalizasyonlar (0074) | Premium görsel yalnız hak sahibince seçilebiliyor |
| S5 | Gece uzlaştırma işi (aktif hakkı olan ve son 24 saatte sync'lenmemiş kullanıcıları RevenueCat'ten tazele), metrikler (webhook hata oranı, sync gecikmesi), runbook, ADR-033 | Alarm ve runbook hazır |
| S6 | Client entegrasyon notu (bu belgedeki 8.2, 10, 12.2 ve 16.6 bölümleri) | Mobil ekibe teslim |
| S7 | T + P: migration 0076–0077, katalog/atama/Apple kod havuzu admin endpoint'leri, `offers` alanı, Android `redeem-code`, sync'te kullanıma düşme, atama push'u | Seçilen kullanıcı iOS'ta kodla, Android'de offer ile indirimli abone olabiliyor |

Deploy, her fazda değil, birikmiş ve rollback güvenli olarak yapılır.
`SUBSCRIPTION_BUSINESS_HOUSES_ENABLED` client hazır olana kadar kapalı kalır.

## 14. Açık kararlar

| # | Soru | Öneri |
|---|---|---|
| K1 | İşletme admini kendi kişisel evlerinde de premium mu? | **Evet.** İşletme ürünü `premium_personal`'ı da verir. Daha pahalı planı alan kişinin kendi evinde reklam görmesi tutarsız olur. |
| K2 | Bir işletme aboneliği kaç evi sponsorlar? | **1.** Daha fazlası için ileride "Business+" ürünü ya da ev başına ek ürün eklenebilir. |
| K3 | Abonelik olmadan İşletme evi açılabilir mi? | **Evet.** Önce ev kurulup üyeler davet edilir, sonra abone olunur (dönüşüm hunisi). Ev, abonelik gelene kadar normal ev gibi çalışır. |
| K4 | Premium bitince seçili premium görseller? | **Kalır** (grandfather), yenisi seçilemez. |
| K5 | İşletme üyesi (ücretsiz) premium **user** profil görseli seçebilir mi? | **Hayır.** Profil görseli her evde görünür. Üye, house görseli kuralıyla değil kişisel kuralla değerlendirilir. |
| K6 | İşletme evinde üye limiti 8'den yüksek olmalı mı? | Ürün kararı. Öneri: business sponsorlu evde 25–30. Bu, planın en somut avantajı olur. |
| K7 | `HouseType=4` mü, ayrı `isBusiness` bayrağı mı? | `HouseType=4` + client'ın önce hazırlanması (12.2). |
| K8 | Deneme süresi (free trial) olacak mı? | Mağaza tarafında intro offer olarak tanımlanır; backend `periodType=trial`'ı yalnız gösterim için saklar. Ek iş yok. |
| K9 | Kişisel planı olan biri İşletme'ye geçerse? | Aynı subscription group'ta upgrade. Kalan süre mağazaca orantılanır; backend ek iş yapmaz. |
| K10 | Web/Stripe satışı olacak mı? | Şimdilik hayır. RevenueCat ileride Stripe/Web Billing'i aynı entitlement'larla bağlayabilir; model değişmez. |
| K11 | %100 hak verme nerede tutulsun: kendi DB'miz mi, RevenueCat promotional entitlement mı? | **Kendi DB'miz** (`PremiumGrant`). Grant'lar tek tek revoke edilebilir, RevenueCat'in 2 saat kuralına takılmaz. |
| K12 | Teklif kataloğunda hangi oranlar ve süreler olsun? | Ürün başına %10, %25, %50 ve "1 ay ücretsiz". Apple'ın 10 aktif offer sınırının altında kalır. |
| K13 | Android'de uygulama içi kod alanı olsun mu? | Evet, ama yalnız Play offer uygunluğu açar; Play ödeme politikası yayından önce kontrol edilir. iOS'ta yok. |
| K14 | Grant için azami süre? Süresiz (lifetime) olsun mu? | Azami 365 gün, süresiz yok. Uzatma yeni grant ile yapılır. |
| K15 | iOS hedefli indirim: offer code mu, promotional offer mı? | Varsayılan **offer code** (yeni kullanıcıda da çalışır). Promotional offer, eski/iptal etmiş aboneleri geri kazanma kampanyaları için; güncel uygunluk kuralı uygulamadan önce doğrulanır. |

## 15. Testler (`tests/subscription`, yalnız public API)

- Resolver tablo testleri: bölüm 4.1'deki her satır.
- Webhook: yanlış/eksik Authorization → 401; aynı `eventId` iki kez → tek
  kayıt, tek job; bilinmeyen kullanıcı → 200 + hak yok; production'da
  izinsiz sandbox → yok sayılır.
- Sync: fake RevenueCat client ile aktif / süresi geçmiş / grace period /
  family shared / transfer (iki kullanıcı da tazelenir).
- Ev akışları: business ev oluşturma + otomatik bağlama; tip değiştirme
  yasağı; bağlı sponsorun çıkışı 409; bağ kaldırıldıktan sonra çıkış ve
  premium'un düşmesi; üye çıkarılınca premium'un düşmesi; hesap silmede bağın
  temizlenmesi.
- Görsel: ücretsiz kullanıcı premium görsel → 403; business üyesi house
  görseli seçemez (sahip değil) ve premium user görseli seçemez; katalog dışı
  URL reddi; aynı değerin tekrar gönderilmesi kabul.
- Bind: sahip olmayan → 403; tip Business değil → 409; bekleme süresi; iki
  kullanıcının aynı evi bağlaması → unique çakışma.
- Grant: SuperAdmin olmayan → 403; `endsAt` yok ya da azami süreyi aşıyor →
  400; aktif grant'la premium, süre bitince ya da revoke'ta ücretsiz;
  business grant + bağlı ev → üyeler premium; mağaza hakkı ile grant
  birlikteyken biri bitince diğeri sürer.
- Teklifler: atama `offers`'ta görünür, süresi geçince görünmez; Apple kod
  havuzu boşken atama → 409; aynı kodun iki kullanıcıya ayrılmaması
  (eşzamanlı atama testi); sync'te offer eşleşince `redeemed`.
- Android açık kod: yanlış kod → 404 + deneme sayacı; limit dolu → 409; aynı
  kullanıcı ikinci kez → 409; süresi geçmiş kod → 410.

## 16. İndirimler, kodlar ve hediye premium

### 16.1. İstek ve mağaza gerçekleri

İstek: Hem test amaçlı hem seçilen kullanıcılara, belirli süreli ve farklı
oranlarda (%100, %50, %10) indirim tanımlamak.

Tasarımı belirleyen kısıtlar:

1. **Ücretli indirimi (%50, %10) yalnız mağaza uygulayabilir.** Ödeme App
   Store / Google Play'den geçtiği için fiyatı backend düşüremez. Her indirim
   mağazada **önceden tanımlanmış bir offer**'dır. Bu yüzden oranlar serbest
   girilen bir sayı değil, önceden hazırlanmış bir **teklif kataloğudur**
   (16.4).
2. **Apple offer'ları yüzde değil, bölge bazında fiyat ile tanımlanır.**
   "%50" için o fiyatın yarısına en yakın fiyat basamağı seçilir. Abonelik
   başına en fazla 10 aktif offer olabilir. Tek kullanımlık kodlar en fazla 6
   ay geçerlidir.
3. **Google Play promo kodları abonelikte yalnız ücretsiz deneme verir**
   (3–90 gün). Kullanıcının ödeme yöntemi eklemesi gerekir ve süre sonunda
   ücretli yenilenir. Yüzde indirim, uygunluğu "developer determined" olan bir
   offer ile yapılır; teklifi kime göstereceğine uygulama karar verir.
4. **App Store Yönergesi 3.1.1:** Uygulama, kendi kod/lisans mekanizmasıyla
   özellik kilidi açamaz. iOS'ta "kodunu gir, premium ol" alanı reddedilme
   riski taşır. iOS'ta kodla dağıtım **Apple Offer Codes** ile yapılmalıdır.
5. **"%100" aslında iki farklı şeydir:**
   - **(a) Hak verme:** Ödeme yok, süre bitince biter, yenilenmez. Test ve
     hediye için doğru olan budur.
   - **(b) Mağaza offer'ı:** "İlk N ay ücretsiz, sonra ücretli yenilenir".
     Kullanıcı aboneliği başlatır, ödeme yöntemi gerekir. Pazarlama içindir.

### 16.2. Üç mekanizma

| Kod | Mekanizma | Oran | Kime | Ödeme | iOS | Android |
|---|---|---|---|---|---|---|
| **G** | Hak verme (grant) | %100 | SuperAdmin'in seçtiği kullanıcılar: testçiler, hediye, destek telafisi, App Review demo hesabı | Yok, yenilenmez | Backend (kod girişi yok) | Backend (kod girişi yok) |
| **T** | Hedefli indirim | Katalogdaki oranlar (%10, %25, %50, "N ay ücretsiz") | Seçili kullanıcılar | Mağaza | Havuzdan atanan tek kullanımlık offer code ya da promotional offer | Developer-determined offer |
| **P** | Açık kampanya kodu | Katalogdaki oranlar | Kodu bilen herkes, kullanım limitli | Mağaza | Apple custom offer code (ör. `FAUS50`), App Store'un kod ekranında | Uygulamada kod alanı → backend doğrular → Play offer |

Öneri: **Önce G** yapılır. Mağaza hazır olmadan test edilebilir ve
RevenueCat'e bağlı değildir. T ve P mağaza ürünleri onaylandıktan sonra
gelir.

### 16.3. G — Hak verme (%100, süreli)

**Kaynak: kendi DB'miz (öneri, K11).** RevenueCat'in promotional entitlement
API'si de var, ama:

- revoke, o entitlement'taki **tüm** promosyonel hakları birlikte siler;
- aktif bir hakkın bitişine 2 saatten yakın uzatma isteği çift istek sayılıp
  yok sayılır;
- birden fazla grant'ı (test + hediye gibi) ayrı ayrı yönetmek zorlaşır.

Client kararı zaten `GET /subscription/me`'den aldığı için grant'ın yalnız
bizde durması yeterlidir. Görünürlük için RevenueCat müşteri kaydına
`fauscrew_grant_until` attribute'u yazılabilir (opsiyonel).

```go
type PremiumGrant struct {
	Id          primitive.ObjectID `bson:"_id,omitempty"`
	UserId      string             `bson:"userId"`
	Entitlement string             `bson:"entitlement"` // premium_personal | premium_business
	StartsAt    time.Time          `bson:"startsAt"`
	EndsAt      time.Time          `bson:"endsAt"`      // zorunlu
	Purpose     string             `bson:"purpose"`     // test | gift | support | review | campaign
	CampaignId  string             `bson:"campaignId,omitempty"`
	Note        string             `bson:"note"`
	CreatedBy   string             `bson:"createdBy"`
	CreatedOn   time.Time          `bson:"createdOn"`
	RevokedAt   *time.Time         `bson:"revokedAt,omitempty"`
	RevokedBy   string             `bson:"revokedBy,omitempty"`
}
```

İndeks: `{userId, endsAt}`.

Kurallar:

- `endsAt` zorunlu. Süresiz hak verilmez; azami süre K14'e göre belirlenir
  (öneri 365 gün).
- Production'da `purpose` ve `note` zorunlu. Her işlem `createdBy` /
  `revokedBy` ile denetlenebilir.
- Revoke anında etkilidir (`revokedAt`); kayıt silinmez.
- `premium_business` grant'ı, satın alınmış business hakkı gibi davranır
  (R2: kişisel hakkı da kapsar). Sponsorluk için ev yine bağlanmalıdır (8.2).
- `purpose=test` olan grant'lar gelir ve dönüşüm metriklerinden ayrı tutulur.
- Kullanıcıya isteğe bağlı push gider ("Hesabına 30 gün premium tanımlandı").
- Kullanıcı kod girmediği için 3.1.1 kapsamına girmez. Yine de iOS'ta
  "hediye kodu" diye pazarlanmamalı.

### 16.4. T — Hedefli indirim

**Teklif kataloğu (`DiscountCampaign`):**

```go
type DiscountCampaign struct {
	Id              primitive.ObjectID `bson:"_id,omitempty"`
	Key             string             `bson:"key"`        // unique, ör. personal_yearly_50_first_year
	Plan            string             `bson:"plan"`       // personal | business
	Percent         int                `bson:"percent"`    // 10, 25, 50, 100 (gösterim + rapor)
	DiscountPeriods int                `bson:"discountPeriods"` // kaç fatura dönemi indirimli
	Mechanism       string             `bson:"mechanism"`  // targeted | public
	AppStore        *AppStoreOfferRef  `bson:"appStore,omitempty"`  // productId + offerCode adı ya da promotionalOfferId
	PlayStore       *PlayStoreOfferRef `bson:"playStore,omitempty"` // productId + basePlanId + offerId
	ValidFrom       time.Time          `bson:"validFrom"`
	ValidUntil      time.Time          `bson:"validUntil"`
	MaxAssignments  int                `bson:"maxAssignments"` // 0 = sınırsız
	Purpose         string             `bson:"purpose"`    // test | campaign | retention
	IsActive        bool               `bson:"isActive"`
	CreatedBy       string             `bson:"createdBy"`
	CreatedOn       time.Time          `bson:"createdOn"`
}
```

Her katalog satırı, mağazada önceden oluşturulmuş offer'lara karşılık gelir.
Apple'ın 10 offer sınırı nedeniyle küçük bir katalog önerilir (K12). Örnek:
ürün başına %10, %25, %50 ve "1 ay ücretsiz".

**Atama (`DiscountAssignment`):**

```go
type DiscountAssignment struct {
	Id           primitive.ObjectID `bson:"_id,omitempty"`
	CampaignId   string             `bson:"campaignId"`
	UserId       string             `bson:"userId"`
	Status       string             `bson:"status"` // assigned | presented | redeemed | expired | revoked
	AppleCodeEnc string             `bson:"appleCodeEnc,omitempty"` // havuzdan ayrılan tek kullanımlık kod, şifreli
	AssignedAt   time.Time          `bson:"assignedAt"`
	ExpiresAt    time.Time          `bson:"expiresAt"`
	RedeemedAt   *time.Time         `bson:"redeemedAt,omitempty"`
	AssignedBy   string             `bson:"assignedBy"` // SuperAdmin ya da "public-code"
}
```

İndeks: `{campaignId, userId}` unique. Öneri: bir kullanıcının aynı anda tek
aktif ataması olur.

**iOS:**

- **Tek kullanımlık offer code (yeni kullanıcı dahil herkes için):**
  1. App Store Connect'te offer için toplu kod üretilir.
  2. CSV, SuperAdmin endpoint'iyle içe aktarılır (`AppleOfferCode` havuzu,
     kodlar şifreli saklanır).
  3. Atamada havuzdan atomik olarak bir kod ayrılır (`findOneAndUpdate`).
  4. Client kodu "Sana özel %50" kartında gösterir ve App Store'un kod
     kullanma ekranını açar (RevenueCat `presentCodeRedemptionSheet` ya da
     redeem URL'i).
- **Promotional offer (kodsuz, uygulama içinde doğrudan indirimli satın
  alma):** Offer imzasını RevenueCat SDK'sı atar (In-App Purchase key
  RevenueCat'e yüklenir). Apple bu teklifleri tarihsel olarak yalnız mevcut
  ya da eski abonelere açıyor; yeni kullanıcılar için offer code kullanılır.
  Güncel kural uygulama öncesi doğrulanır (K15).

**Android:** Base plan'a "developer determined" uygunluklu, yüzde indirimli
offer tanımlanır. Client, RevenueCat SDK'nın `subscriptionOptions`
listesinden yalnız backend'in atamada döndürdüğü `offerId`'yi gösterir ve
satın alır.

**Kullanıma düşme:** Sync sırasında RevenueCat'ten gelen işlemin offer
bilgisi (offer code adı / offer id) atamanın teklifiyle eşleşirse atama
`redeemed` olur. Kullanılmayan atama `expiresAt`'te `expired` olur. Bu yalnız
raporlama içindir; erişimi etkilemez, erişim her zaman mağaza aboneliğinden
gelir.

### 16.5. P — Açık kampanya kodu

- **iOS:** Apple custom offer code (kod adını biz seçeriz, kullanım limiti
  koyarız). Uygulamadaki "Kodum var" düğmesi App Store'un kod ekranını açar.
  Backend'de iş yok; sync yalnız kullanımı raporlar.
- **Android:** Play'de yüzde indirimli kod olmadığı için kodu biz
  doğrularız:

  ```go
  type DiscountCode struct {
  	Id             primitive.ObjectID `bson:"_id,omitempty"`
  	CodeDigest     string             `bson:"codeDigest"` // HMAC, house invite code deseni
  	CampaignId     string             `bson:"campaignId"`
  	MaxRedemptions int                `bson:"maxRedemptions"`
  	RedeemedCount  int                `bson:"redeemedCount"`
  	ValidUntil     time.Time          `bson:"validUntil"`
  	IsActive       bool               `bson:"isActive"`
  }
  ```

  Akış:
  1. `POST /api/subscription/redeem-code` kodu doğrular.
  2. Kullanıcı başına tek kullanım kontrolü yapılır, `redeemedCount`
     koşullu olarak artırılır.
  3. Bir `DiscountAssignment` oluşturulur ve Play offer'ı döner.

  Ödeme yine Play'den geçer; kod yalnız teklife uygunluğu açar. Play
  politikasıyla uyumu ayrıca kontrol edilir (K13). Deneme sayısı mevcut
  `AuthCodeAttempt` deseniyle sınırlanır.
- Bu endpoint iOS client'ta kullanılmaz (3.1.1). Arayüzde platform farkı
  açıkça yazılır.

### 16.6. API eklentileri

`GET /api/subscription/me` yanıtına eklenenler:

```json
{
  "personal": { "isActive": true, "via": "grant", "expiresAt": "2026-12-21T00:00:00Z" },
  "grants": [
    { "entitlement": "premium_personal", "endsAt": "2026-12-21T00:00:00Z", "purpose": "gift" }
  ],
  "offers": [
    {
      "assignmentId": "6660…",
      "plan": "personal",
      "percent": 50,
      "discountPeriods": 12,
      "expiresAt": "2026-12-31T23:59:59Z",
      "ios": { "type": "offerCode", "code": "ABCD1234EFGH" },
      "android": { "productId": "fauscrew_personal", "basePlanId": "yearly", "offerId": "discount50-first-year" }
    }
  ]
}
```

`via`: `store` | `grant`. `grants[].purpose`, `test` dışındaki değerler için
client'a gösterilebilir.

Yeni endpoint'ler:

| Endpoint | Yetki | İş |
|---|---|---|
| `POST /api/admin/premium-grants` | SuperAdmin | `{userIds \| emails, entitlement, endsAt \| durationDays, purpose, note}` |
| `DELETE /api/admin/premium-grants/:id` | SuperAdmin | Revoke |
| `GET /api/admin/premium-grants?userId=` | SuperAdmin | Liste |
| `POST /api/admin/discount-campaigns` / `PUT …/:id` | SuperAdmin | Katalog |
| `POST /api/admin/discount-campaigns/:id/assignments` | SuperAdmin | `{userIds}` → atama + push |
| `POST /api/admin/discount-campaigns/:id/apple-codes` | SuperAdmin | Tek kullanımlık kod CSV içe aktarma |
| `POST /api/admin/discount-codes` | SuperAdmin | Android açık kod |
| `POST /api/subscription/offers/:assignmentId/presented` | Kullanıcı | Gösterim (rapor) |
| `POST /api/subscription/redeem-code` | Kullanıcı | Android açık kod (16.5) |

Admin endpoint'leri mevcut `RequireRoleWithLocalizer(SuperAdmin)` ile
korunur. Toplu atamalar Asynq job olarak işlenir.

### 16.7. Test için kullanım

| Ortam | Yöntem |
|---|---|
| Yerel geliştirme | Xcode StoreKit Configuration dosyası (offer code ve promotional offer yerelde test edilir). Android'de license tester hesapları. |
| TestFlight / staging | Sandbox alımları zaten ücretsizdir. Offer code'ların sandbox desteği sınırlıdır; kod akışı StoreKit Testing ile doğrulanır. |
| Production iç test | G: `purpose=test` ve kısa süreli (ör. 7 gün) grant. İşletme akışı için `premium_business` grant'ı. |
| App Review | Demo hesabına `purpose=review` grant'ı; inceleme notunda belirtilir. |

### 16.8. Resolver değişikliği

Bölüm 8.1'deki "aktif hak" tanımı genişler:

- **`premium_personal` aktif:** mağaza hakkı aktif, ya da aktif bir
  `premium_personal` / `premium_business` grant'ı var.
- **`premium_business` aktif:** mağaza hakkı aktif (`PURCHASED`), ya da aktif
  bir `premium_business` grant'ı var.
- **Aktif grant:** `revokedAt == nil && startsAt <= now < endsAt`.

Resolver iki kaynağı (`UserSubscription` + `PremiumGrant`) tek sorgu turunda
okur. Ev listesinde sahiplerin grant'ları `$in` ile birlikte çekilir.

### 16.9. Yeniden değerlendirme — plana etkisi

1. **G, S1'e çekilir.** Premium arayüzü, mağaza ürünleri ve RevenueCat hazır
   olmadan test edilebilir. RevenueCat config'i yoksa modül yalnız grant'larla
   çalışır; 12.1'deki "config yoksa herkes ücretsiz" kuralı "config yoksa
   yalnız grant" olarak güncellenir.
2. **Teklif kataloğu S0'da ürünlerle birlikte tasarlanır.** Apple'ın 10 aktif
   offer sınırı ve fiyat basamakları ürün kararını etkiler.
3. **Yeni faz S7** (T ve P, bölüm 13).
4. **Platform farkı bilinçli bir karardır:** iOS'ta kendi kod alanımız yok,
   Android'de var. İki platformda da hedefli teklif kartı aynı görünür.
5. **Raporlama:** RevenueCat grafikleri bizim grant'larımızı görmez. Grant ve
   atama raporu backend'den alınır (S5 metriklerine eklenir).
6. **Kötüye kullanım:** Kod digest'le saklanır, Apple kodları şifrelenir,
   redeem endpoint'i rate limit'lidir, kullanıcı başına tek kullanım
   uygulanır, admin işlemleri denetim kaydı tutar.

## Kaynaklar

- [RevenueCat — Webhooks](https://www.revenuecat.com/docs/integrations/webhooks)
- [RevenueCat — Restore Behavior](https://www.revenuecat.com/docs/projects/restore-behavior)
- [RevenueCat — API v2 Customer Resources](https://www.revenuecat.com/docs/api-v2/customer/resources)
- [RevenueCat — Entitlements (promotional grant, API v1)](https://www.revenuecat.com/docs/api-v1/entitlements)
- [RevenueCat — Google Play offers](https://www.revenuecat.com/docs/subscription-guidance/subscription-offers/google-play-offers)
- [Apple — Set up offer codes](https://developer.apple.com/help/app-store-connect/manage-subscriptions/set-up-subscription-offer-codes/)
- [Android — Promo codes](https://developer.android.com/google/play/billing/promo)
