# Sosyal giriş (Google, Apple, Facebook, Snapchat) — Backend planı

Tarih: 19 Ekim 2026.

Durum: **Faz A–E tamamlandı.** Bitenler:
- altyapı;
- Google, Apple, Facebook ve Snapchat girişleri;
- emailsiz kayıt;
- hesap bağlama ve Apple bildirimleri.

Sağlayıcılar config verilene kadar kapalı. Gerçek cihazla staging testi henüz
yapılmadı; açık işler bölüm 10'da. Mimari karar ADR-023'te (`architecture-decisions.md`) özetlenir; bu
belge uygulama ayrıntısını ve faz sırasını tutar.

## 1. Amaç ve ilke

Kullanıcı, Google, Apple, Facebook veya Snapchat hesabıyla tek dokunuşla kayıt
olabilir ve sonra yine aynı yolla giriş yapabilir.

Sağlayıcı yalnız **kimlik doğrulamak** için kullanılır. Doğrulamadan sonra kullanıcı
kendi `User` collection'ımızdan okunur ve kendi JWT'miz (`JWTService.GenerateToken`)
verilir. Sağlayıcının access/refresh token'ları saklanmaz, profil verisi sağlayıcıdan
sürekli çekilmez. Tek istisna Apple'ın hesap silmede zorunlu tuttuğu token iptalidir
(bölüm 5.2).

Her girişte client, sağlayıcının SDK'sından yeni bir token alır ve backend bunu doğrular.
Client'ın gönderdiği `email` veya sağlayıcı kullanıcı ID'si tek başına kimlik kanıtı
sayılmaz; aksi halde bu değeri bilen herkes hesaba girebilir. Bu adım çoğu zaman tek
dokunuşla veya sessizce olur, izin ekranı tekrar çıkmaz.

| Sağlayıcı | Login başına sağlayıcıya istek |
|---|---|
| Google, Apple, Facebook Limited Login | Yok. İmzalı JWT, cache'lenen JWKS ile offline doğrulanır; JWKS ara sıra yenilenir. |
| Facebook klasik, Snapchat | Var. Opak access token tek server çağrısıyla doğrulanır. |

## 2. Veri modeli

### 2.1. `User` üzerindeki değişiklik

```go
type AuthProvider int

const (
    ProviderHouseflow AuthProvider = 0 // email + password
    ProviderGoogle    AuthProvider = 1
    ProviderApple     AuthProvider = 2
    ProviderFacebook  AuthProvider = 3
    ProviderSnapchat  AuthProvider = 4
)

// User
SignUpProvider AuthProvider `bson:"signUpProvider" json:"signUpProvider"`
```

- `SignUpProvider` kullanıcının **nereden kayıt olduğunu** gösterir ve sonradan değişmez.
- Sosyal kullanıcıda `HashPassword` boştur. Kullanıcı reset akışıyla sonradan şifre
  belirleyebilir; o zaman iki yolla da girebilir.
- Yeni hesapların emaili normalize edilerek kaydedilir (`helpers.NormalizeEmail`:
  `strings.ToLower(strings.TrimSpace(...))`). Signup ve admin'in kullanıcı oluşturması
  bunu kullanır.
- Email ile yapılan bütün aramalar `helpers.EmailLookupFilter` kullanır: login, signup,
  forgot, reset, email doğrulama, `GetUserByEmail`. Filtre normalize edilmiş emaili ve
  yazıldığı hâliyle emaili birlikte arar. Böylece normalizasyondan önce büyük harfle
  kaydedilmiş hesaplar da bulunur.
- Mevcut veride değişiklik yapılmaz. Bu, geri dönüşü güvenli tutar: önceki sürüm
  emailleri hâlâ yazıldığı gibi arıyor.
- Reset ve doğrulama kodlarının HMAC'i normalize edilmiş email üzerinden hesaplanır.
- `idxUserEmailUnique` olduğu gibi kalır; her kullanıcının emaili zorunludur (bölüm 4.3).

### 2.2. Yeni collection: `UserIdentity`

Bir kullanıcının bağlı sağlayıcı hesapları ayrı collection'da tutulur. Bir kullanıcı
ileride birden fazla sağlayıcı bağlayabilir; tekillik index'i burada daha temiz kurulur ve
User DTO'ları şişmez.

```go
type UserIdentity struct {
    Id                   primitive.ObjectID `bson:"_id,omitempty"`
    UserId               string             `bson:"userId"`
    Provider             AuthProvider       `bson:"provider"`
    ProviderUserId       string             `bson:"providerUserId"`  // sub / externalId / app-scoped id
    ProviderEmail        string             `bson:"providerEmail"`
    EmailVerified        bool               `bson:"emailVerified"`   // sağlayıcının beyanı
    IsPrivateRelay       bool               `bson:"isPrivateRelay"`  // Apple gizli email
    ProviderClientId     string             `bson:"providerClientId"` // token'ın verildiği client ID
    AppleRefreshTokenEnc string             `bson:"appleRefreshTokenEnc,omitempty"`
    CreatedOn            time.Time          `bson:"createdOn"`
    LastLoginOn          time.Time          `bson:"lastLoginOn"`
}
```

| Index | Amaç |
|---|---|
| `{provider: 1, providerUserId: 1}` unique (`idxUserIdentityProviderUnique`) | Aynı sağlayıcı hesabı iki kullanıcıya bağlanamaz. |
| `{userId: 1, provider: 1}` unique (`idxUserIdentityUserProviderUnique`) | Bir kullanıcıya her sağlayıcıdan en fazla bir hesap bağlanır; kullanıcıya göre aramalarda da kullanılır. |

### 2.3. Migration'lar

Hepsi yalnız ekleme yapar. Önceki sürüm yeni alanı ve collection'ı okumaz.

| Sürüm | İçerik |
|---|---|
| 0048 | `User.signUpProvider = 0`, alanı olmayan dokümanlar için (0017 deseni). |
| 0049 | `UserIdentity` collection'ı ve iki index. |
| 0050 | Yeni hata/mesaj lokalizasyonları (0032 deseni, bölüm 3.3). |

`UserIdentity` şeması `provider >= 1` ister; email/şifre (`0`) bir kimlik kaydı değildir.

## 3. API sözleşmesi

Uçlar mevcut `/auth` grubuna girer; `StrictRateLimit` orada zaten uygulanıyor.

### 3.1. `POST /auth/social/:provider`

Kayıt ve giriş aynı uçtadır: "Continue with X" düğmesi her iki durumda da bu isteği atar.
`:provider` değerleri `google`, `apple`, `facebook`, `snapchat`. Faz B itibarıyla yalnız
`google` ve `apple` çalışır; diğerleri `provider_not_supported` döner.

```json
{
  "idToken": "…",            // Google, Apple, Facebook Limited Login
  "accessToken": "…",        // Facebook klasik, Snapchat
  "nonce": "…",              // ham nonce; Apple ve Limited Login'de zorunlu, Google'da opsiyonel
  "authorizationCode": "…",  // Apple; yalnız ilk yetkilendirmede gelir
  "firstName": "…",          // Apple ilk yetkilendirmede; diğerlerinde opsiyonel
  "lastName": "…",
  "language": "tr"           // opsiyonel; yoksa "en"
}
```

| Durum | Cevap |
|---|---|
| Kayıtlı kimlik, giriş | `200 {"token": "…", "isNewUser": false}` |
| Yeni kullanıcı oluşturuldu | `201 {"token": "…", "isNewUser": true}` |
| Sağlayıcı email vermedi | `200 {"isNewUser": false, "needsEmail": true, "pendingToken": "…"}` (`token` yok) |
| Apple email vermedi | `400 auth.error.social_email_required` (bölüm 4.3) |

### 3.2. Emailsiz kaydın tamamlanması

Sağlayıcı email vermediğinde kayıt iki istekle tamamlanır. Hesap ancak email
doğrulandıktan sonra oluşur.

1. `POST /auth/social/complete/code`: kullanıcının girdiği emaile 6 karakterlik kod
   gönderir. Email başka bir hesapta kayıtlıysa `409 email_registered_with_other_method` döner.

   ```json
   { "pendingToken": "…", "email": "user@example.com" }
   ```

   Cevap `200 {"success": true}`.
2. `POST /auth/social/complete`: kodu kontrol eder ve hesabı `IsVerifyEmail=true` ile açar.

   ```json
   { "pendingToken": "…", "email": "user@example.com", "code": "ABCD12",
     "firstName": "…", "lastName": "…", "language": "tr" }
   ```

   Cevap `201 {"token": "…", "isNewUser": true}`. Aynı sağlayıcı hesabı bu arada kaydını
   tamamlamışsa `200` ile o hesaba giriş yapılır. Yanlış ya da süresi dolmuş kodda
   `400 auth.error.invalid_or_expired_email_verification_code` döner.

Kod geçerliliği `RESET_CODE_VALIDITY_MINUTES` ile aynıdır. Kod hem pending kimliğe hem
emaile bağlıdır (`social-signup|provider|providerUserId|email` üzerinden HMAC). Bu yüzden:
- Başka bir emaille ya da başka bir pending kimlikle kullanılamaz.
- Şifre sıfırlama ve email doğrulama kodlarıyla karışmaz.

### 3.3. Hata anahtarları

| Anahtar | HTTP | Ne zaman |
|---|---|---|
| `auth.error.provider_not_supported` | 400 | Bilinmeyen sağlayıcı veya config'i olmadığı için kapalı sağlayıcı |
| `auth.error.invalid_social_token` | 401 | İmza, `iss`, `aud`, `exp`, nonce veya app_id kontrolü başarısız |
| `auth.error.email_registered_with_other_method` | 409 | Email başka bir yolla kayıtlı (bölüm 4.2) |
| `auth.error.social_signup_expired` | 401 | `pendingToken` geçersiz veya süresi dolmuş |
| `auth.error.use_social_login` | 400 | Şifresiz hesaba `/auth/login` ile girilmeye çalışıldı |
| `auth.error.provider_already_linked` | 409 | Bağlanmak istenen sağlayıcı hesabı başka bir kullanıcıya ait |
| `auth.error.provider_already_connected` | 409 | Kullanıcıya bu sağlayıcıdan başka bir hesap zaten bağlı |
| `auth.error.provider_not_linked` | 404 | Kaldırılmak istenen sağlayıcı bağlı değil |
| `auth.error.last_sign_in_method` | 409 | Şifresi olmayan kullanıcının son sağlayıcısı kaldırılamaz |
| `auth.error.provider_unavailable` | 503 | Sağlayıcının key set'ine veya API'sine ulaşılamıyor, cache'te de kopya yok |
| `auth.error.invalid_signed_request` | 401 | Facebook data deletion isteğinin imzası geçersiz |
| `auth.error.data_deletion_not_found` | 404 | Bilinmeyen onay kodu |
| `auth.error.account_locked` | mevcut | `isActive=false` |

401 için `helpers.NewUnauthorizedError` (`ErrorKindUnauthorized`) eklendi ve
`RespondLocalizedError` bunu 401'e eşliyor.

### 3.4. `AuthUserResultModel` (`/auth/isAuth`)

Ekleme yapılır, mevcut client'lar etkilenmez:
- `signUpProvider` (int)
- `linkedProviders` ([]int)
- `hasPassword` (bool): client "şifre belirle" seçeneğini gösterebilsin diye.

## 4. Handler akışları

### 4.1. `SocialAuthCommand`

1. `:provider` ile verifier seçilir; yoksa veya kapalıysa `provider_not_supported` döner.
2. `verifier.Verify(ctx, credential)` → `VerifiedIdentity{Provider, ProviderUserId, Email,
   EmailVerified, IsPrivateRelay, FirstName, LastName}`.
3. `UserIdentity` içinde `(provider, providerUserId)` aranır.
4. **Kimlik bulundu:**
   - User okunur. `isActive=false` ise `account_locked` döner.
   - `User.lastLogin` ve `UserIdentity.lastLoginOn` güncellenir. Login'deki gibi
     `{_id, isActive: true}` filtresi kullanılır.
   - Token üretilir, `isNewUser=false`.
5. **Kimlik yok, email var:**
   - Email normalize edilir ve User'da **büyük/küçük harfe duyarsız** aranır (`en`
     collation, strength 2). Bulunursa `email_registered_with_other_method` döner
     (bölüm 4.2). Bu arama email index'ini kullanamaz; kayıt seyrek olduğu için kabul
     edildi. Böylece eskiden büyük harfle kaydedilmiş hesaplar da yakalanır.
   - Bulunmazsa tek `WithinTransaction` içinde şunlar yazılır:
     - `User`: `SignUpProvider`, `IsVerifyEmail = EmailVerified`, boş şifre, `Role: Normal`,
       `HouseIds: []`.
     - `UserIdentity`.
     - `UserInfoHistory`: `signUp.go`'daki firstName/lastName/role kayıtları.
   - Apple'da transaction'dan önce `authorizationCode` refresh token'a çevrilir
     (bölüm 5.2). Başarısız olursa kayıt yapılmaz.
   - Token üretilir, `isNewUser=true`.
6. **Kimlik yok, email yok:** `pendingToken` üretilir (bölüm 4.4), `needsEmail=true` döner.
   Hesap açma kuralları (`SignIn`, `EnsureEmailAvailable`, `Create`) `auth/policies.SocialAccounts`
   içinde; sosyal giriş ve emailsiz kaydın tamamlanması aynı kodu kullanır.
7. **Yarış:** Aynı kimlikle iki istek aynı anda kayıt açmaya çalışırsa unique index
   duplicate key hatası verir. Bu durumda kimlik tekrar okunur ve 4. adıma düşülür.
   Email index'inde duplicate key gelirse `email_registered_with_other_method` döner.

İsim yoksa (Snapchat, Apple'ın ikinci girişi): `firstName` için sağlayıcının display
name'i, o da yoksa boş değer kullanılır. Kullanıcı profilden düzenler.

### 4.2. Email çakışma politikası

Kimlik kaydı yokken sağlayıcının emailiyle bir kullanıcı zaten varsa hesaplar **otomatik
birleştirilmez** ve `409 email_registered_with_other_method` döner. Kullanıcı mevcut
yöntemiyle girip sağlayıcıyı profilden bağlar (bölüm 3.5).

Gerekçe, önceden hesap ele geçirme riski: Saldırgan kurbanın emailiyle doğrulanmamış,
şifreli bir hesap açar. Kurban sonra Google ile girer, hesaplar birleşir ve saldırganın
şifresi çalışmaya devam eder. Doğrulanmış emaillerde otomatik birleştirme sonradan
eklenebilir; ama o zaman birleştirmede eski şifrenin silinmesi ve oturumların düşürülmesi
gerekir.

### 4.3. Email vermeyen sağlayıcı

Her kullanıcının emaili zorunlu kalır; böylece email unique index'i, JWT `Issuer` alanı
ve forgot/reset akışı değişmez. Sağlayıcı email vermezse (Snapchat, telefonla açılmış
veya email izni reddedilmiş Facebook hesapları) kayıt `pendingToken` ile bölünür
(bölüm 3.2).

**Planla farkı:** Plan, hesabı önce doğrulanmamış emaille açıp sonra doğrulamayı
öngörüyordu. Uygulamada hesap, email kodla doğrulanmadan **açılmıyor**. Gerekçesi şu senaryo:
1. Saldırgan kendi Snapchat hesabıyla kurbanın emailini yazarak kayıt olur.
2. Kurban o emaille kayıt olamaz.
3. Kurban "Şifremi unuttum" ile hesaba girse bile saldırganın Snapchat girişi hesapta kalır.

Önce doğrulama bu senaryoyu kapatıyor.

Apple bu akışa girmez: Apple'ın tek kullanımlık `authorizationCode`'u email adımında
süresini doldurabilir. Apple email scope'u istendiğinde emaili her zaman verdiği için,
emailsiz Apple girişi `social_email_required` ile reddedilir.

### 4.4. `pendingToken`

- İçeriği: `provider`, `providerUserId`, sağlayıcıdan gelen isim. Süresi 10 dakika.
- **Access token'dan farklı bir anahtarla imzalanır.** Anahtar
  `HMAC-SHA256(JWT_SECRET, "social-pending")` ile türetilir. `JWTService.ValidateToken`
  aynı secret'la imzalanmış her token'ı kabul ettiği için, aynı anahtar kullanılırsa
  `pendingToken` `AuthRequired` middleware'inden geçerdi.
- `typ: socialPendingSignUp` claim'i taşır. Geçersiz, süresi dolmuş ya da başka anahtarla
  imzalanmış token `401 auth.error.social_signup_expired` döner. Test, pending token'ın
  access token olarak kabul edilmediğini de kontrol ediyor.
- `helpers.SocialPendingTokenService` içinde; anahtar config'deki JWT secret'tan türetilir,
  yeni secret gerekmez.

### 4.5. Mevcut akışlara etkiler

- **`LoginHandler`:** `HashPassword == ""` ise sayaç **artırılmadan**
  `auth.error.use_social_login` döner. Bugünkü kodda bu durumda `CheckPasswordHash` false
  dönüyor ve `failedLoginAttempts` artıyor; 10 denemede sosyal hesap kilitlenir.
- **`ForgotPassword` / `ResetPassword`:** Açık kalır. Email sahipliğini kanıtlayan sosyal
  kullanıcı şifre belirleyebilir. Apple gizli relay adreslerine mail ulaşması için
  gönderici domain'i Apple'a kayıtlı olmalı (bölüm 8).
- **`DeleteUser`:** Kullanıcının `UserIdentity` kayıtları, user ile aynı transaction'da
  silinir. Commit'ten sonra Apple kimliği için revoke çağrılır. Revoke başarısız olursa
  silme engellenmez; hata adaptörde loglanır.
- **Kimliği kalmış silinmiş kullanıcı:** Kimlik kaydı var ama user yoksa kimlik silinir ve
  kayıt baştan yapılır.
- **`ValidateAuth`:** DTO alanları için kimlikler okunur (bölüm 3.4).
- **Hesap silme ve App Store:** Guideline 5.1.1(v) için `DELETE /user/me` eklendi
  (20 Ekim 2026). Oturumdaki kullanıcı kendi hesabını siler ve `204` döner. Kural ADR-024'te:
  - Başka üyesi olmayan evler tüm verisiyle silinir.
  - Paylaşılan evlerin sahipliği kalan ilk üyeye geçer.
  - `UserInfoHistory` silinir ve Apple yetkisi revoke edilir.
- **JWT:** Değişmez. Sosyal kullanıcının token'ı da 72 saattir; süre dolunca client
  sağlayıcıyla sessizce yeniden giriş yapar.

## 5. Sağlayıcı ayrıntıları

### 5.1. Google

- Token: Google Sign-In SDK'sının `idToken`'ı (JWT).
- JWKS: `https://www.googleapis.com/oauth2/v3/certs`.
- `iss`: `accounts.google.com` veya `https://accounts.google.com`.
- `aud`: `GOOGLE_CLIENT_IDS` listesinden biri. iOS'ta iOS client ID'si, Android'de
  Credential Manager'a verilen `serverClientId` (web client ID) gelir.
- Kimlik: `sub`. Email `email` claim'inden, doğrulanma durumu `email_verified`'den okunur.

### 5.2. Apple

- Token: `identityToken` (JWT) + `authorizationCode`.
- JWKS: `https://appleid.apple.com/auth/keys`; `iss`: `https://appleid.apple.com`.
- `aud`: `APPLE_CLIENT_IDS` (iOS bundle ID; Android/web için Services ID).
- Nonce zorunlu. Client ham nonce üretir ve Apple'a SHA-256 digest'inin **küçük harf
  hex** hâlini verir. Backend'e ham nonce gönderilir; backend onu hash'leyip token'daki
  `nonce` claim'iyle sabit zamanlı karşılaştırır.
- `ProviderClientId`, token'ın `aud`'undan (eşleşen client ID) alınır. Code exchange ve
  revoke bu client ID ile yapılır.
- Kimlik: `sub`. Email token'daki `email` claim'inden okunur; client'ın gönderdiği emaile
  güvenilmez. `is_private_email` claim'i `IsPrivateRelay`'e yazılır. Apple emaili
  doğrulanmış kabul eder.
- İsim token'da yoktur ve SDK yalnız **ilk** yetkilendirmede verir. Client ilk çağrıda
  `firstName`/`lastName`'i göndermelidir.
- **Revoke (App Store Guideline 5.1.1(v)):**
  - Yeni hesapta `authorizationCode` zorunludur; yoksa `invalid_social_token` döner.
    Var olan hesabın girişinde kullanılmaz, Apple'a istek gitmez.
  - Kayıtta `authorizationCode`, `POST https://appleid.apple.com/auth/token` ile refresh
    token'a çevrilir. `client_secret`, `.p8` anahtarıyla imzalanmış ES256 JWT'dir
    (`iss=APPLE_TEAM_ID`, `kid=APPLE_KEY_ID`, `sub=client_id`). Her istekte 5 dakika
    ömürle yeniden üretilir.
  - Apple'ın cevabındaki `id_token`'ın `sub`'ı, doğrulanan kimlikle aynı olmalıdır; başka
    bir hesabın code'u kabul edilmez. Apple `400`/`401` dönerse (kullanılmış ya da süresi
    dolmuş code) `invalid_social_token`, diğer hatalarda `provider_unavailable` döner.
  - Refresh token, `SOCIAL_TOKEN_ENC_KEY` ile AES-GCM şifrelenip
    `AppleRefreshTokenEnc`'e yazılır.
  - Hesap silmede `POST https://appleid.apple.com/auth/revoke` çağrılır.
- Apple, iOS'ta başka üçüncü parti girişler sunulduğunda zorunludur (Guideline 4.8). Bu
  yüzden Google ile aynı fazda gelir.

### 5.3. Facebook

İstekte `accessToken` varsa klasik login, `idToken` varsa Limited Login olarak işlenir.

- **Klasik login (Android, ATT izni verilmiş iOS):** opak access token gelir.
  - `GET https://graph.facebook.com/{version}/debug_token?input_token=…&access_token=APP_ID|APP_SECRET`
    çağrılır. `is_valid=true`, `app_id == FACEBOOK_APP_ID` ve `user_id` kontrol edilir.
    `app_id` kontrolü başka bir uygulamaya verilmiş token'ın kabul edilmesini engeller.
  - Profil `GET /{version}/me?fields=id,email,first_name,last_name` ve `appsecret_proof`
    (`HMAC-SHA256(app_secret, access_token)`) ile okunur. Profilin `id`'si `debug_token`'daki
    `user_id` ile aynı olmalıdır.
  - Graph API sürümü `FACEBOOK_GRAPH_VERSION` ile sabitlenir; verilmezse `v23.0`.
  - Graph'ın 4xx cevapları (429 hariç) `invalid_social_token`, diğer hatalar
    `provider_unavailable` olur.
- **Limited Login (ATT izni olmayan iOS):** JWT gelir; Graph'a istek gitmez.
  - JWKS: `https://limited.facebook.com/.well-known/oauth/openid/jwks/`.
  - `iss`: `https://www.facebook.com` (ya da `https://limited.facebook.com`);
    `aud`: `FACEBOOK_APP_ID`.
  - Nonce zorunlu. Facebook SDK'ya verilen nonce token'da **olduğu gibi** döner; Apple'daki
    gibi hash'lenmez. Backend'e aynı değer gönderilir.
- Kimlik: uygulamaya özel (app-scoped) kullanıcı ID'si. İki yolda aynı ID'nin geldiği
  staging'de doğrulanmalı. Aynı değilse ATT ayarını değiştiren kullanıcı ikinci kez kayıt
  olmaya çalışır ve email çakışmasıyla `409` alır.
- Email gelmeyebilir; bu durumda bölüm 3.2'deki email adımı çalışır. Facebook
  yalnız onaylanmış birincil emaili paylaştığı için gelen email doğrulanmış kabul edilir.

#### Data deletion callback

- `POST /auth/facebook/data-deletion`: form alanı `signed_request`. App Dashboard'da
  "Data Deletion Request URL" olarak bu adres girilir.
- İmza: `base64url(HMAC-SHA256(payload, app_secret)).base64url(payload)`. Payload'da
  `algorithm: HMAC-SHA256` ve `user_id` olmalıdır.
- Bağlı hesap, başka giriş yolu (şifre, Google, Apple) olsa bile **her zaman silinir**
  (ADR-025). Silme `DELETE /user/me` ile aynı kuralla (ADR-024) yapılır.
- Cevap: `{"url": ".../api/v1/auth/facebook/data-deletion/{code}", "confirmation_code": "{code}"}`.
  URL'in şeması proxy'nin `X-Forwarded-Proto` başlığından gelir.
- Silme istek anında yapılır. Onay kodu `DataDeletionRequest` collection'ına yazılır:
  - Kayıtta sağlayıcı kullanıcı ID'si tutulmaz.
  - Kayıt 90 gün sonra TTL ile silinir (migration 0051).
- `GET /auth/facebook/data-deletion/{code}`: `{confirmationCode, status: "completed", requestedOn}`
  döner. Bilinmeyen kodda `404` döner.
- Facebook aynı isteği tekrar gönderebilir; ikinci istek de yeni bir onay koduyla başarılı olur.

### 5.4. Snapchat

- Token: Login Kit access token. İstekte `accessToken` gönderilir.
- Doğrulama ve profil: `GET https://kit.snapchat.com/v1/me?query={me{externalId displayName}}`,
  `Authorization: Bearer <token>`. Kimlik `externalId`'dir; `displayName` ad olarak kullanılır.
- 4xx (429 hariç) `invalid_social_token`, diğer hatalar `provider_unavailable` olur.
- **Faz D başında kontrol edilenler (21 Ekim 2026, Snap Login Kit dokümanı):**
  - Login Kit'in izinleri yalnız `user.display_name`, `user.bitmoji.avatar` ve
    `user.external_id`; **email izni yok**. Snapchat kaydı her zaman bölüm 3.2'deki email
    adımından geçer.
  - Token'ın hangi uygulamaya verildiğini soran dokümante bir uç yok. SDK'da bir OIDC ID
    token seçeneği var ama issuer ve JWKS adresi dokümante edilmemiş, o yüzden kullanılmadı.
  - Bunun yerine `externalId`'nin uygulamaya özel olmasına güveniliyor: başka bir
    uygulamaya verilmiş token, kullanıcının o uygulamadaki ID'sini döndürür, bizdeki
    hesabınkini değil.
  - Snap, OIDC'yi dokümante ederse JWKS ile doğrulamaya geçilmeli.
- `SNAPCHAT_CLIENT_ID` sağlayıcıyı açar ve `ProviderClientId` olarak saklanır; doğrulamada
  kullanılmaz.
- `/v1/me` adresi resmi dokümanda REST olarak yer almıyor; SDK'ların arkasındaki uç bu.
  Staging'de gerçek token'la doğrulanmalı.

## 6. Kod yerleşimi

| Yer | İçerik |
|---|---|
| `internal/data/entities/user.go` | `AuthProvider` enum'u, `SignUpProvider` alanı |
| `internal/data/entities/userIdentity.go` | `UserIdentity` |
| `internal/data/migrations/0048…0050` | Bölüm 2.3 |
| `internal/application/auth/abstract/socialIdentityVerifier.go` | `SocialIdentityVerifier` arayüzü, `SocialCredential`, `VerifiedIdentity` (`emailSender.go` gibi) |
| `internal/infrastructure/socialAuth/jwks.go` | Ortak JWKS cache (RSA ve EC anahtarlar, yeni bağımlılık yok) |
| `internal/infrastructure/socialAuth/idToken.go` | `IDTokenVerifier`: imza, algoritma, `iss`, `aud`, `exp`, `iat`, `sub` |
| `internal/infrastructure/socialAuth/{google,apple,facebook,snapchat}.go` | Sağlayıcı verifier'ları; Apple client_secret ve revoke |
| `internal/infrastructure/socialAuth/tokenCipher.go` | Apple refresh token için AES-GCM |
| `internal/application/auth/commands/socialAuth.go` | `SocialAuthCommand` |
| `internal/application/auth/commands/completeSocialSignUp.go` | `CompleteSocialSignUpCommand` |
| `internal/application/auth/commands/{link,unlink}SocialProvider.go` | Faz E |
| `internal/controllers/authController.go`, `cmd/api/routes.go` | Uçlar ve handler kaydı |
| `internal/models/dtos/authModels.go` | Request/response modelleri, DTO alanları |
| `internal/config/config.go` | `ConfigSocialAuth` ve env okuma (bölüm 7) |

Adaptörler `internal/infrastructure` altında: `internal/services` mimari testle yalnız
`notificationService.go`'ya ayrılmış durumda.

**JWKS cache davranışı.**
- Anahtarlar ilk kullanımda ve 6 saatlik TTL dolunca çekilir.
- Bilinmeyen bir `kid` erken yenilemeyi tetikler, ama en fazla dakikada bir. Böylece
  anahtar rotasyonu yakalanır ve sahte `kid`'ler sağlayıcıya yük bindiremez.
- Yenileme başarısız olursa eski anahtarlar kullanılmaya devam eder.
- İlk çekim başarısızsa `provider_unavailable` döner ve sonraki istek hemen tekrar dener.
- `IDTokenVerifier` 1 dakikalık saat kaymasına izin verir. Nonce ve `email_verified` gibi
  sağlayıcıya özel claim'ler verifier'ın kendisine bırakılır.

Handler'lar `cqrsLiteConventions.md` kurallarına uyar. Transaction sınırı command
handler'dadır. Sağlayıcı çağrıları transaction'ın dışında, ondan önce yapılır.
Verifier'lar handler'a `map[AuthProvider]SocialIdentityVerifier` olarak inject edilir;
config'i olmayan sağlayıcı map'e girmez.

## 7. Config ve secret'lar

| Env | Kullanım |
|---|---|
| `GOOGLE_CLIENT_IDS` | Virgülle ayrılmış kabul edilen `aud` listesi |
| `APPLE_CLIENT_IDS` | Bundle ID ve Services ID |
| `APPLE_TEAM_ID`, `APPLE_KEY_ID`, `APPLE_PRIVATE_KEY` | `client_secret` üretimi (`.p8` içeriği) |
| `FACEBOOK_APP_ID`, `FACEBOOK_APP_SECRET` | `debug_token`, `appsecret_proof`, Limited Login `aud`, data deletion imzası |
| `FACEBOOK_GRAPH_VERSION` | Opsiyonel Graph API sürümü (varsayılan `v23.0`) |
| `SNAPCHAT_CLIENT_ID` | Login Kit |
| `SOCIAL_TOKEN_ENC_KEY` | 32 byte, base64. Apple refresh token şifrelemesi. Apple açıksa zorunlu. |

Config, `config.json`'da `external.socialAuth` altında da verilebilir; env değerleri
dosyadakileri ezer. `APPLE_PRIVATE_KEY` tek satır secret olarak `\n` içerebilir; bu
gerçek satır sonuna çevrilir.

Bir sağlayıcının değişkenlerinin hiçbiri yoksa o sağlayıcı kapalıdır ve
`provider_not_supported` döner. Yarım yapılandırılmış sağlayıcı (örneğin
`APPLE_CLIENT_IDS` var ama anahtar yok, ya da Facebook ID'si var ama secret'ı yok)
`config.ValidateSocialAuth` tarafından başlangıçta reddedilir; böylece bir sağlayıcı
yanlışlıkla kapalı kalmaz. Kod canlıya çıkabilir; sağlayıcılar Fly secret'ı
eklenerek, deploy gerekmeden tek tek açılır.

### 3.5. Hesap bağlama (Faz E)

Oturum açmış kullanıcı sağlayıcı ekleyip çıkarabilir; iki uç da `AuthRequired`:
- `POST /auth/social/{provider}/link`: gövde `/auth/social/{provider}` ile aynıdır.
  - Apple'da `authorizationCode` da gerekir; hesap silinince revoke için saklanır.
  - Cevap: `200 {"linkedProviders": [...]}`.
  - Aynı sağlayıcı hesabı zaten bu kullanıcıdaysa istek bir şey değiştirmeden başarılı olur.
  - Sağlayıcı hesabı başka bir kullanıcıya bağlıysa `provider_already_linked` döner.
  - Kullanıcıya bu sağlayıcıdan başka bir hesap bağlıysa `provider_already_connected` döner.
  - Hesabın emaili değişmez; sağlayıcının emaili `UserIdentity.providerEmail`'de durur.
- `DELETE /auth/social/{provider}/link`: bağlantıyı kaldırır ve güncel listeyi döner.
  - Şifresi olmayan kullanıcının son sağlayıcısı kaldırılamaz (`last_sign_in_method`).
    Kontrol transaction içinde yapılır ve user dokümanına da yazılır; böylece aynı anda
    gelen iki kaldırma isteği kullanıcıyı girişsiz bırakamaz.
  - Kaldırılan Apple bağlantısı Apple'da revoke edilir.

Bu iki uç, emaili başka yolla kayıtlı kullanıcının `409 email_registered_with_other_method`
aldıktan sonra izleyeceği yoldur: önce mevcut yöntemiyle giriş yapar, sonra sağlayıcıyı
bağlar.

**Bilinen risk.** Bağlama yalnız oturum token'ı ister, yeniden kimlik doğrulama istemez.
Çalınmış bir token ile saldırgan kendi sağlayıcı hesabını bağlayıp kalıcı giriş yolu
edinebilir. Bağlama sonrası kullanıcıya bildirim maili gönderilmesi ya da şifreli
hesaplarda şifre onayı istenmesi sonraki bir iş olarak değerlendirilebilir.

### 3.6. Apple server-to-server bildirimleri (Faz E)

- `POST /auth/apple/notifications`, gövde `{"payload": "<JWT>"}`.
  - Apple Developer'da "Server-to-Server Notification Endpoint" olarak girilir.
- `payload` Apple'ın identity anahtarlarıyla imzalıdır ve şu kontroller yapılır:
  - `iss` `https://appleid.apple.com` olmalı.
  - `aud` bizim client ID'lerimizden biri olmalı.
  - `events` claim'i string olarak kodlanmış JSON'dur; içinde `type` ve `sub` vardır.
- Event'lere göre davranış:

  | Event | Davranış |
  |---|---|
  | `consent-revoked` | Apple bağlantısı silinir, hesap kalır. Apple'da yetki zaten kalktığı için revoke edilmez. |
  | `account-delete` / `account-deleted` | Hesap tamamen silinir (ADR-024 kuralı). Facebook'taki karar (ADR-025) ile tutarlı. |
  | `email-disabled` / `email-enabled` / bilinmeyen | `200` ile onaylanır, bir şey değişmez. |

- Apple aynı event'i tekrar gönderebilir; bütün adımlar idempotent ve bilinmeyen `sub`
  için `200` döner. Geçersiz imzada `401` döner.

## 8. Platform tarafında yapılacaklar (kod dışı)

- **Google Cloud:** iOS, Android ve web OAuth client ID'leri.
- **Apple Developer:**
  - Sign in with Apple capability ve Services ID.
  - `.p8` anahtarı.
  - Gizli relay'e mail gönderebilmek için gönderici domain'inin ve adresinin
    "Email Sources"a kaydı (SPF gerekir).
  - Server-to-Server Notification Endpoint: `https://<api>/api/v1/auth/apple/notifications`.
- **Meta:** App ID/secret, `email` izni, App Review.
  - Data deletion callback URL'i: `https://<api>/api/v1/auth/facebook/data-deletion`.
- **Snap:** Login Kit uygulaması, client ID, izin kapsamları.

## 9. Testler

- **Verifier'lar:** `httptest` ile sahte JWKS ve Graph/Snap sunucusu, test sırasında
  üretilen RSA/EC anahtarlar. Senaryolar:
  - Geçerli token.
  - Yanlış `aud` / `iss`, süresi dolmuş token, bilinmeyen `kid`.
  - Yanlış nonce.
  - Başka `app_id`'ye ait Facebook token'ı.
  - JWKS anahtar rotasyonu (bilinmeyen `kid` gelince cache yenilenir).
- **Handler'lar:** Sahte verifier kullanan `tests/authSocialCqrs_test.go`:
  - Yeni kullanıcı; tekrar giriş.
  - Email çakışması → 409.
  - Email yok → `pendingToken` → complete.
  - Süresi dolmuş veya access token anahtarıyla imzalanmış `pendingToken` reddedilir.
  - `pendingToken`, `AuthRequired` middleware'inden geçmez.
  - Pasif hesap → `account_locked`.
  - Eşzamanlı iki kayıt → tek User, tek UserIdentity.
  - Kapalı sağlayıcı → `provider_not_supported`.
- **Regresyon:**
  - Şifresiz kullanıcı `/auth/login` → `use_social_login` ve `failedLoginAttempts`
    değişmez.
  - Email normalizasyonu: büyük harfli email ile signup/login.
  - `DeleteUser` kimlikleri siler; Apple revoke sahte sunucuya gider.

## 10. Fazlar

| Faz | Kapsam | Bitiş kriteri |
|---|---|---|
| **A. Altyapı** ✅ | Enum, `UserIdentity`, 0048–0050, email normalizasyonu, config, verifier arayüzü, JWKS cache, `NewUnauthorizedError`, şifresiz kullanıcı login düzeltmesi | Tamamlandı (19 Ekim 2026). Bütün `./...` testleri Mongo replica set ile geçti; hiçbir sağlayıcı açık değil |
| **B. Google + Apple** ✅ | `/auth/social/:provider`, `SocialAuthCommand`, Apple nonce, client_secret, refresh token saklama, `DeleteUser` revoke, DTO alanları | Kod ve testler tamamlandı (20 Ekim 2026). Açık: staging'de iOS ve Android ile gerçek Google/Apple kaydı ve girişi, hesap silmede Apple revoke |
| **C. Facebook** ✅ | Klasik + Limited Login, data deletion callback ve durum ucu | Kod ve testler tamamlandı (20 Ekim 2026). Açık: staging'de iki login türüyle kayıt ve giriş, iki yolda aynı kullanıcı ID'sinin geldiğinin kontrolü, App Dashboard'a callback URL'inin girilmesi |
| **D. Snapchat + email tamamlama** ✅ | Snap verifier, `pendingToken`, `/auth/social/complete/code`, `/auth/social/complete` | Kod ve testler tamamlandı (21 Ekim 2026). Açık: staging'de gerçek Snapchat token'ıyla `/v1/me` doğrulaması |
| **E. Hesap bağlama** ✅ | `POST/DELETE /auth/social/:provider/link` (AuthRequired), Apple server-to-server bildirimleri (`consent-revoked`, `account-delete`, `email-disabled`) | Kod ve testler tamamlandı (21 Ekim 2026). Açık: staging'de Apple'dan gerçek bildirim alınması |

Faz D'deki `pendingToken` akışı Facebook'un emailsiz hesapları için de çalışır. Faz C ve
D aynı sürümde çıkacağı için ara durum yok.

## 11. Canlıya çıkış ve geri dönüş

- Değişiklikler yalnız ekleme yapar: yeni alan varsayılanı 0, yeni collection ve yeni uçlar.
  Önceki sürüm bunları okumaz.
- Sağlayıcılar config ile kapalı çıkar. Client'lar hazır olunca secret eklenerek açılır.
  Ayrı bir deploy gerekmez; fazlar diğer işlerle aynı sürümde birikebilir.
- **Geri dönüş riski:** Önceki sürümde, şifresiz bir sosyal kullanıcı `/auth/login`
  denerse `failedLoginAttempts` artar ve hesap kilitlenebilir. Sağlayıcılar kapalı
  çıktığı sürece böyle kullanıcı oluşmaz. Sağlayıcı açıldıktan sonra Faz A'dan önceki bir
  sürüme dönülmemelidir.
- Email normalizasyonu mevcut veriye dokunmaz (bölüm 2.1). Büyük harfle kaydedilmiş eski
  hesaplar, emaillerini kayıttaki hâliyle yazdıkları sürece eskisi gibi giriş yapar.
  İleride bu kayıtlar küçültülmek istenirse önce canlıda `email != toLower(email)` olan
  kayıtlar ve olası çakışmalar sayılmalı. Böyle bir veri migration'ı geri dönüşü
  bozacağı için ancak önceki sürüme dönme ihtiyacı kalmadığında yapılmalı.

## 12. Mobil client notları

- Apple için client kriptografik olarak rastgele bir ham nonce üretir. Apple isteğine
  SHA-256 digest'inin küçük harf hex hâlini verir, backend'e ham hâlini gönderir.
- Facebook klasik login'de backend'e `accessToken` gönderilir. iOS Limited Login'de
  `idToken` ve SDK'ya verilen nonce (hash'lenmeden) gönderilir. `email` izni istenmelidir.
- Google'da nonce opsiyoneldir. Kullanılırsa SDK'ya ve backend'e aynı değer gönderilir;
  token'da nonce varsa backend'e gönderilmesi zorunludur.
- Apple'da her girişte `idToken` ve `nonce` gönderilir.
- Apple ilk yetkilendirmede `firstName`, `lastName` ve `authorizationCode` mutlaka
  gönderilir. Bu çağrı başarısız olursa isim bir daha alınamaz; client bunları istek
  başarılı olana kadar saklamalıdır.
- `needsEmail=true` gelirse:
  1. Email ekranı açılır.
  2. `/auth/social/complete/code` çağrılır.
  3. Kullanıcı mailine gelen kodu girer.
  4. `/auth/social/complete` çağrılır.

  `pendingToken` 10 dakika geçerlidir; süresi dolarsa (`social_signup_expired`) sağlayıcı
  girişi baştan yapılır.
- Snapchat'te Login Kit access token'ı `accessToken` olarak gönderilir; `external_id`
  izni istenmelidir.
- `409 email_registered_with_other_method` gelirse kullanıcıya mevcut yöntemiyle giriş
  yapması söylenir.
- `/auth/login` `use_social_login` dönerse ilgili sağlayıcı düğmesi gösterilir.
