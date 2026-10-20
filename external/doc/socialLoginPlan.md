# Sosyal giriş (Google, Apple, Facebook, Snapchat) — Backend planı

Tarih: 19 Ekim 2026.

Durum: **Faz A (altyapı) ve Faz B (Google + Apple) tamamlandı.** Sağlayıcılar config
verilene kadar kapalı. Gerçek cihazla staging testi henüz yapılmadı. Sıradaki: Faz C
(Facebook). Mimari karar ADR-023'te (`architecture-decisions.md`) özetlenir; bu
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
| `{userId: 1}` (`idxUserIdentityUser`) | Kullanıcı silme, bağlı sağlayıcıları listeleme. |

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
| Sağlayıcı email vermedi | Faz D'de: `200 {"needsEmail": true, "pendingToken": "…"}`. O zamana kadar `400 auth.error.social_email_required` |

### 3.2. `POST /auth/social/complete`

Sağlayıcı email vermediğinde kaydı tamamlar.

```json
{ "pendingToken": "…", "email": "user@example.com", "firstName": "…", "lastName": "…" }
```

Cevap `201 {"token": "…", "isNewUser": true}`. Kullanıcı `IsVerifyEmail=false` ile
oluşur. Client ardından mevcut `GET /auth/validate-email` + `POST /auth/validate-email`
akışını çalıştırır.

### 3.3. Hata anahtarları

| Anahtar | HTTP | Ne zaman |
|---|---|---|
| `auth.error.provider_not_supported` | 400 | Bilinmeyen sağlayıcı veya config'i olmadığı için kapalı sağlayıcı |
| `auth.error.invalid_social_token` | 401 | İmza, `iss`, `aud`, `exp`, nonce veya app_id kontrolü başarısız |
| `auth.error.email_registered_with_other_method` | 409 | Email başka bir yolla kayıtlı (bölüm 4.2) |
| `auth.error.social_signup_expired` | 401 | `pendingToken` geçersiz veya süresi dolmuş |
| `auth.error.use_social_login` | 400 | Şifresiz hesaba `/auth/login` ile girilmeye çalışıldı |
| `auth.error.provider_already_linked` | 409 | Faz E: sağlayıcı hesabı başka kullanıcıya bağlı |
| `auth.error.provider_unavailable` | 503 | Sağlayıcının key set'ine veya API'sine ulaşılamıyor, cache'te de kopya yok |
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
7. **Yarış:** Aynı kimlikle iki istek aynı anda kayıt açmaya çalışırsa unique index
   duplicate key hatası verir. Bu durumda kimlik tekrar okunur ve 4. adıma düşülür.
   Email index'inde duplicate key gelirse `email_registered_with_other_method` döner.

İsim yoksa (Snapchat, Apple'ın ikinci girişi): `firstName` için sağlayıcının display
name'i, o da yoksa boş değer kullanılır. Kullanıcı profilden düzenler.

### 4.2. Email çakışma politikası

Kimlik kaydı yokken sağlayıcının emailiyle bir kullanıcı zaten varsa hesaplar **otomatik
birleştirilmez** ve `409 email_registered_with_other_method` döner. Kullanıcı mevcut
yöntemiyle girip sağlayıcıyı profilden bağlar (Faz E).

Gerekçe, önceden hesap ele geçirme riski: Saldırgan kurbanın emailiyle doğrulanmamış,
şifreli bir hesap açar. Kurban sonra Google ile girer, hesaplar birleşir ve saldırganın
şifresi çalışmaya devam eder. Doğrulanmış emaillerde otomatik birleştirme sonradan
eklenebilir; ama o zaman birleştirmede eski şifrenin silinmesi ve oturumların düşürülmesi
gerekir.

### 4.3. Email vermeyen sağlayıcı

Her kullanıcının emaili zorunlu kalır; böylece email unique index'i, JWT `Issuer` alanı
ve forgot/reset akışı değişmez. Sağlayıcı email vermezse (Snapchat, telefonla açılmış
veya email izni reddedilmiş Facebook hesapları) kayıt `pendingToken` ile ikiye bölünür.
Kullanıcının girdiği email doğrulanmamış sayılır ve mevcut doğrulama koduyla doğrulanır.

### 4.4. `pendingToken`

- İçeriği: `provider`, `providerUserId`, sağlayıcıdan gelen isim. Süresi 10 dakika.
- **Access token'dan farklı bir anahtarla imzalanır.** Anahtar
  `HMAC-SHA256(JWT_SECRET, "social-pending")` ile türetilir. `JWTService.ValidateToken`
  aynı secret'la imzalanmış her token'ı kabul ettiği için, aynı anahtar kullanılırsa
  `pendingToken` `AuthRequired` middleware'inden geçerdi.
- `CompleteSocialSignUpCommand` token'ı doğrular. Kimlik bu arada başka bir istekle
  oluşmuşsa o kullanıcıya giriş yapılır; yoksa 4.1'in 5. adımı email ve
  `EmailVerified=false` ile çalışır.

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

- **Klasik login (Android, ATT izni verilmiş iOS):** opak access token gelir.
  - `GET https://graph.facebook.com/debug_token?input_token=…&access_token=APP_ID|APP_SECRET`
    çağrılır. `is_valid=true`, `app_id == FACEBOOK_APP_ID` ve `user_id` kontrol edilir.
    `app_id` kontrolü başka bir uygulamaya verilmiş token'ın kabul edilmesini engeller.
  - Profil `GET /me?fields=id,email,first_name,last_name` ve `appsecret_proof` ile okunur.
- **Limited Login (ATT izni olmayan iOS):** JWT gelir.
  - JWKS: `https://limited.facebook.com/.well-known/oauth/openid/jwks/`.
  - `iss`: `https://www.facebook.com`; `aud`: `FACEBOOK_APP_ID`; nonce kontrolü yapılır.
- Kimlik: uygulamaya özel (app-scoped) kullanıcı ID'si, iki yolda da aynıdır. Email
  gelmeyebilir; o durumda bölüm 4.3 akışı çalışır. Facebook emaili doğrulanmış kabul edilir.
- App Review için data deletion callback URL'i gerekir (Faz C).

### 5.4. Snapchat

- Token: Login Kit access token.
- Doğrulama ve profil: `https://kit.snapchat.com/v1/me` GraphQL sorgusu
  (`{me{externalId displayName}}`). Kimlik `externalId`'dir.
- Varsayılan izinlerde email yoktur. Kayıt büyük ihtimalle her zaman bölüm 4.3 akışından
  geçer.
- **Faz D başında doğrulanacak:**
  - Uygulamamıza email izni verilip verilmediği.
  - Token'ın bizim client ID'mize ait olduğunun nasıl kontrol edileceği (Snap'in OIDC
    `id_token` desteği varsa JWKS ile doğrulama tercih edilir).

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
| `FACEBOOK_APP_ID`, `FACEBOOK_APP_SECRET` | `debug_token`, `appsecret_proof`, Limited Login `aud` |
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

## 8. Platform tarafında yapılacaklar (kod dışı)

- **Google Cloud:** iOS, Android ve web OAuth client ID'leri.
- **Apple Developer:**
  - Sign in with Apple capability ve Services ID.
  - `.p8` anahtarı.
  - Gizli relay'e mail gönderebilmek için gönderici domain'inin ve adresinin
    "Email Sources"a kaydı (SPF gerekir).
- **Meta:** App ID/secret, `email` izni, data deletion callback URL'i, App Review.
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
| **C. Facebook** | Klasik + Limited Login, data deletion callback | Staging'de iki login türüyle kayıt ve giriş |
| **D. Snapchat + email tamamlama** | Snap verifier, `pendingToken`, `/auth/social/complete` | Emailsiz kayıt, doğrulama koduyla tamamlanır |
| **E. Hesap bağlama** | `POST/DELETE /auth/social/:provider/link` (AuthRequired), Apple server-to-server bildirimleri (`consent-revoked`, `account-delete`, `email-disabled`) | Şifreli kullanıcı sağlayıcı bağlar; son giriş yöntemi kaldırılamaz |

Faz D'deki `pendingToken` akışı Facebook'un emailsiz hesapları için de gerekir. Faz C,
Faz D'den önce çıkarsa emailsiz Facebook kaydı geçici olarak
`auth.error.social_email_required` ile reddedilir.

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
  Facebook Limited Login'in nonce sözleşmesi Faz C'de netleşecek.
- Google'da nonce opsiyoneldir. Kullanılırsa SDK'ya ve backend'e aynı değer gönderilir;
  token'da nonce varsa backend'e gönderilmesi zorunludur.
- Apple'da her girişte `idToken` ve `nonce` gönderilir.
- Apple ilk yetkilendirmede `firstName`, `lastName` ve `authorizationCode` mutlaka
  gönderilir. Bu çağrı başarısız olursa isim bir daha alınamaz; client bunları istek
  başarılı olana kadar saklamalıdır.
- `needsEmail=true` gelirse email ekranı açılır, `/auth/social/complete` çağrılır, sonra
  doğrulama kodu akışı başlar.
- `409 email_registered_with_other_method` gelirse kullanıcıya mevcut yöntemiyle giriş
  yapması söylenir.
- `/auth/login` `use_social_login` dönerse ilgili sağlayıcı düğmesi gösterilir.
