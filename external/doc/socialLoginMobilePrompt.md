# Görev: HouseFlow mobil uygulamasına sosyal giriş entegrasyonu

Sen HouseFlow'un iOS ve Android uygulamalarında çalışan mobil geliştiricisin. Backend ekibi
Google, Apple, Facebook ve Snapchat ile kayıt/girişi, hesaba sağlayıcı bağlamayı ve uygulama
içinden hesap silmeyi tamamladı. Görevin bu API'leri mobil uygulamaya doğru şekilde bağlamak.

Backend'i değiştirme. Bu belgedeki sözleşmeyle backend'in davranışı uyuşmazsa ya da bir akış
belgede yoksa, kendi çözümünü uydurma: durumu, isteği ve cevabı not edip bana raporla.

## 1. Temel ilke

- Sağlayıcı (Google, Apple, Facebook, Snapchat) **yalnız kimliği kanıtlar**. Oturum, backend'in
  verdiği **kendi JWT'mizdir**; sağlayıcı token'larını saklamayın, başka bir yere göndermeyin,
  loglamayın.
- Her girişte sağlayıcının SDK'sından **yeni** bir token alıp backend'e gönderirsiniz. Backend'e
  asla yalnız email veya sağlayıcı kullanıcı ID'si göndermeyin; backend bunlara güvenmez.
- Kayıt ve giriş **aynı uçtadır**. "X ile devam et" düğmesi her iki durumda da aynı isteği atar;
  hesabın yeni açılıp açılmadığını cevap söyler.

## 2. Platform kapsamı

| Sağlayıcı | iOS | Android |
|---|---|---|
| Google | Var | Var |
| Apple | Var (zorunlu) | **Yok**: backend Apple'ın Android/web akışını desteklemiyor, düğmeyi göstermeyin |
| Facebook | Var (klasik login ya da Limited Login) | Var (klasik login) |
| Snapchat | Var | Var |

App Store kuralları:
- **Guideline 4.8:** iOS'ta herhangi bir üçüncü parti giriş varsa Sign in with Apple da sunulmalı.
- **Guideline 5.1.1(v):** Kullanıcı hesabını uygulama içinden silebilmeli. Bunun için bölüm 8'deki
  `DELETE /user/me` kullanılır.

Bir sağlayıcı backend'de açılmamışsa (config verilmemişse) o uç `400` döner. Hangi
sağlayıcıların gösterileceğini uygulamanın kendi ayarı (build flag ya da remote config) belirlesin;
backend'de açık olmayan bir sağlayıcının düğmesini yayına çıkarmayın.

## 3. Genel sözleşme

- **Base URL:** `{API}/api/v1`. Aşağıdaki yollar buna göredir.
- **Oturum:** Başarılı girişte gelen `token` bizim JWT'mizdir, 72 saat geçerlidir. Keychain /
  EncryptedSharedPreferences'ta saklayın ve korumalı isteklerde `Authorization: Bearer <token>`
  gönderin.
- **Dil:** Her istekte `Accept-Language: tr` ya da `en` gönderin. Hata mesajları bu dilde döner.
- **Hata gövdesi:** `{"success": false, "error": "<kullanıcıya gösterilecek mesaj>"}`. Bazı
  doğrulama hatalarında yalnız `{"error": "..."}` gelir. `error` alanı **hata kodu değil,
  yerelleştirilmiş metindir**.
  - Akışta dallanmayı **yalnız HTTP durum koduna ve uca göre** yapın.
  - `error` metnini parse etmeyin, olduğu gibi kullanıcıya gösterin.
- **Rate limit:** `/auth/*` uçlarının hepsi **IP başına 15 dakikada 10 istekle** sınırlı. Aşılırsa
  `429` döner. Otomatik tekrar denemeyin, mesajı gösterin. `/user/*` uçlarında sınır kullanıcı
  başına dakikada 20 istek.
- **Sağlayıcı numaraları:** API'de sağlayıcılar sayıyla döner:
  `0` email/şifre, `1` Google, `2` Apple, `3` Facebook, `4` Snapchat.

## 4. Giriş / kayıt: `POST /auth/social/{provider}`

`{provider}`: `google`, `apple`, `facebook`, `snapchat`.

İstek gövdesi (sağlayıcıya göre gereken alanları doldurun, diğerlerini göndermeyin):

```json
{
  "idToken": "…",
  "accessToken": "…",
  "nonce": "…",
  "authorizationCode": "…",
  "firstName": "…",
  "lastName": "…",
  "language": "tr"
}
```

- `firstName`, `lastName`: en fazla 50 karakter. Yalnız hesap **ilk açılırken** kullanılır.
- `language`: `tr` ya da `en`. Yeni hesabın dili olur; verilmezse `en`.

Cevaplar:

| Durum | HTTP | Gövde | Uygulama ne yapar |
|---|---|---|---|
| Var olan hesaba giriş | `200` | `{"token": "…", "isNewUser": false}` | Token'ı sakla, ana ekrana geç |
| Yeni hesap açıldı | `201` | `{"token": "…", "isNewUser": true}` | Token'ı sakla, mevcut kayıt sonrası akışa (ev oluştur/katıl) geç |
| Sağlayıcı email vermedi | `200` | `{"isNewUser": false, "needsEmail": true, "pendingToken": "…"}` | `token` yok. Bölüm 6'daki email akışını başlat |

Hatalar:

| HTTP | Anlamı | Uygulama ne yapar |
|---|---|---|
| `400` | Sağlayıcı kapalı, gövde geçersiz, hesap kilitli ya da Apple email vermedi | `error` mesajını göster |
| `401` | Sağlayıcı token'ı doğrulanamadı (süresi dolmuş, yanlış nonce, başka uygulamanın token'ı) | Mesajı göster; sağlayıcı SDK'sından çıkış yapıp tekrar denetin |
| `409` | Bu email başka bir yöntemle (şifre ya da başka sağlayıcı) zaten kayıtlı | Mesajı göster. Kullanıcıyı email/şifre girişine (gerekirse "Şifremi unuttum"a) yönlendir; girişten sonra sağlayıcıyı ayarlardan bağlayabilir (bölüm 7). Hesaplar **otomatik birleştirilmez**. |
| `429` | Rate limit | Mesajı göster |
| `503` | Sağlayıcıya ulaşılamıyor | "Daha sonra tekrar deneyin" gibi mesajı göster |

## 5. Sağlayıcı bazında client adımları

### Google (iOS ve Android)
- **iOS:** GoogleSignIn SDK ile giriş yapın ve `user.idToken.tokenString` değerini `idToken` olarak
  gönderin. iOS client ID'si ve URL scheme'i projede tanımlı olmalı.
- **Android:** Credential Manager (`GetGoogleIdOption` ya da `GetSignInWithGoogleOption`)
  kullanın. `setServerClientId(...)`'e **web client ID**'sini verin; Android client ID'si
  değil. `GoogleIdTokenCredential.idToken` değerini `idToken` olarak gönderin.
- **Nonce:** Opsiyonel. Kullanırsanız SDK'ya verdiğiniz değerin **aynısını** `nonce` olarak
  gönderin. Token'da nonce varsa göndermek zorunludur.

### Apple (yalnız iOS)
1. Her denemede kriptografik olarak rastgele bir ham nonce üretin.
2. `ASAuthorizationAppleIDRequest` için `requestedScopes = [.fullName, .email]` verin.
3. `request.nonce` alanına ham nonce'un **SHA-256 özetinin küçük harf hex** hâlini koyun.
4. Backend'e gönderilecekler:
   - `idToken`: `credential.identityToken`'ın UTF-8 string hâli.
   - `nonce`: **ham** nonce, hash'lenmemiş hâli.
   - `authorizationCode`: `credential.authorizationCode`'un UTF-8 string hâli. Her girişte gönderin;
     yeni hesap açılırken zorunlu.
   - `firstName` / `lastName`: `credential.fullName`'den.

İsimle ilgili kritik nokta: Apple adı **yalnız ilk yetkilendirmede** verir; token'da da yoktur.
İlk istek başarısız olursa isim bir daha alınamaz. Bu yüzden `givenName` / `familyName`'i, istek
başarılı olana kadar yerelde saklayın ve tekrar denemelerde gönderin.

Kullanıcı email'ini gizlerse `@privaterelay.appleid.com` adresi gelir. Bu normaldir, hesap bu
adresle açılır.

### Facebook
- **Android (klasik login):** `public_profile` ve `email` izinleriyle giriş yapın.
  `AccessToken.getCurrentAccessToken().token` değerini `accessToken` olarak gönderin.
- **iOS:** `LoginConfiguration(permissions: ["public_profile", "email"], tracking: …, nonce: rawNonce)`
  ile giriş yapın.
  - `AccessToken.current` varsa (klasik login): `accessToken` gönderin.
  - Yoksa (Limited Login, ATT izni verilmemiş): `AuthenticationToken.current.tokenString` değerini
    `idToken`, SDK'ya verdiğiniz nonce'u `nonce` olarak gönderin. Facebook'ta nonce
    **hash'lenmez**; SDK'ya ne verdiyseniz o gönderilir.
- Kullanıcı email iznini reddederse ya da hesabı telefonla açılmışsa `needsEmail` akışı gelir
  (bölüm 6).

### Snapchat (iOS ve Android)
- Login Kit SDK ile giriş yapın. İstenecek izinler: display name ve **external ID**
  (`https://auth.snapchat.com/oauth2/api/user.external_id`). Bitmoji gerekmez.
- Login Kit'in verdiği **access token**'ı `accessToken` olarak gönderin. Metot adı SDK
  sürümüne göre değişir.
- Snapchat email paylaşmaz. Yeni kullanıcıda cevap her zaman `needsEmail` olur (bölüm 6).
- Snap incelemesinden geçene kadar yalnız Snap Kit portalına eklenmiş **Demo User** hesapları giriş
  yapabilir.

## 6. Emailsiz kayıt akışı (`needsEmail`)

Sağlayıcı email vermediğinde hesap, kullanıcının girdiği email doğrulanana kadar **açılmaz**.
`pendingToken` 10 dakika geçerlidir; ekranlar arasında bellekte tutun, kalıcı saklamayın.

1. **Email ekranı:** Kullanıcıdan email alın. İsteği atın:
   ```json
   POST /auth/social/complete/code
   { "pendingToken": "…", "email": "user@example.com" }
   ```
   - `200 {"success": true}`: koda geçin.
   - `409`: bu email başka bir hesapta kayıtlı; mesajı gösterin, başka email isteyin ya da
     girişe yönlendirin.
   - `401`: `pendingToken`'ın süresi doldu; sağlayıcı girişini baştan başlatın.
2. **Kod ekranı:** Emaile 6 karakterlik bir kod gelir. Kod birkaç dakika geçerlidir; "Tekrar
   gönder" düğmesi 1. adımdaki isteği tekrarlar. Sonra şu isteği atın:
   ```json
   POST /auth/social/complete
   { "pendingToken": "…", "email": "user@example.com", "code": "ABCD12",
     "firstName": "…", "lastName": "…", "language": "tr" }
   ```
   - `201 {"token": "…", "isNewUser": true}`: hesap açıldı, email doğrulanmış durumda.
   - `200 {"token": "…", "isNewUser": false}`: aynı sağlayıcı hesabı bu arada kaydını
     tamamlamış; giriş yapılmıştır.
   - `400`: kod yanlış ya da süresi doldu; mesajı gösterin, tekrar gönderme sunun.
   - `401`: `pendingToken` süresi doldu, baştan başlatın.
   - `409`: email bu arada başkası tarafından alındı.
3. `firstName` / `lastName` opsiyoneldir. Sağlayıcının verdiği ad (örneğin Snapchat display name)
   varsayılan olarak kullanılır, ama kullanıcı bu ekranda düzeltebilir (en fazla 50 karakter).
4. Kayıttan sonra emaili tekrar doğrulatmayın; hesap doğrulanmış emaille açılır.

## 7. Hesap ayarları: bağlı giriş yöntemleri

`GET /auth/isAuth` (Bearer) cevabındaki `data` nesnesine üç alan eklendi:

```json
{ "signUpProvider": 2, "linkedProviders": [1, 2], "hasPassword": false }
```

- `signUpProvider`: hesabın açıldığı yöntem; değişmez.
- `linkedProviders`: şu an bağlı sağlayıcılar.
- `hasPassword`: hesabın şifresi var mı.

Ayarlarda bir "Giriş yöntemleri" ekranı yapın. Her sağlayıcı için bağlı/bağlı değil durumu ve
düğmesi olsun; `hasPassword` false ise "Şifre belirle" seçeneği sunun.

- **Bağla:** `POST /auth/social/{provider}/link` (Bearer). Gövde bölüm 4'teki giriş gövdesiyle
  aynıdır; sağlayıcı SDK'sından yeni token alın. Apple'da `idToken`, ham `nonce` ve
  `authorizationCode` zorunludur.
  - `200 {"linkedProviders": [...]}`: listeyi güncelleyin.
  - `409`: bu sağlayıcı hesabı başka bir kullanıcıya bağlı ya da hesabınıza bu sağlayıcıdan başka
    bir hesap zaten bağlı; mesajı gösterin.
  - `401`: oturum geçersiz **ya da** sağlayıcı token'ı doğrulanamadı. İkisi aynı kodu döner;
    kullanıcıyı otomatik çıkış yaptırmadan önce `GET /auth/isAuth` ile oturumu kontrol edin.
- **Kaldır:** `DELETE /auth/social/{provider}/link` (Bearer).
  - `200 {"linkedProviders": [...]}`: listeyi güncelleyin.
  - `409`: bu, kullanıcının tek giriş yolu (şifresi yok ve başka sağlayıcı bağlı değil). Önce
    şifre belirlemesini ya da başka sağlayıcı bağlamasını önerin.
  - `404`: zaten bağlı değil; listeyi yenileyin.
- **Şifre belirle:** Sosyal hesapların şifresi yoktur. Mevcut "Şifremi unuttum" akışı şifre
  belirlemek için kullanılır: `POST /auth/forget {email}`, ardından
  `POST /auth/reset {email, code, newPassword}`.

## 8. Hesap silme: `DELETE /user/me`

- Bearer ile çağrılır, `204` döner. Ayarlarda görünür bir "Hesabı sil" seçeneği olmalı (Apple
  zorunluluğu).
- Kullanıcıdan uygulama içinde açık bir onay alın ve şu sonuçları yazın:
  - Hesap kalıcı olarak silinir.
  - Kullanıcının **tek üye olduğu evler** tüm görevleri, duyuruları ve geçmişiyle silinir.
  - Paylaşılan evler kalan ilk üyeye geçer.
- `204` sonrası:
  - Yerel token'ı ve kullanıcı verisini temizleyin.
  - Sağlayıcı SDK oturumlarını kapatın: Google `signOut`, Facebook `LoginManager.logOut`,
    Snapchat token temizleme.
  - Giriş ekranına dönün.

Apple bağlantısı backend tarafından Apple'da iptal edilir; client'ın ayrıca yapacağı bir şey yok.

## 9. Mevcut ekranlarda değişenler

- **Email/şifre girişi (`POST /auth/login`):** Sosyal yöntemle açılmış, şifresi olmayan bir
  hesapla şifre denenirse `400` döner. Mesaj, kullanıcıya kayıt olduğu yöntemle girmesini ya da
  şifre belirlemesini söyler; olduğu gibi gösterin.
- Emailler artık küçük harfe çevrilip kaydediliyor ve karşılaştırılıyor. Client'ta ayrıca bir şey
  yapmanız gerekmiyor; emaili kullanıcının yazdığı gibi gönderin.
- **Oturum süresi:** Korumalı bir uç `401` dönerse giriş ekranını gösterin. Sağlayıcıyla tekrar
  giriş çoğu zaman tek dokunuştur.
- **Çıkış:** Yerel token'ı silin. İsteğe bağlı olarak sağlayıcı SDK oturumunu da kapatın; Google'da
  bunu yapmak, sonraki girişte hesap seçiciyi tekrar gösterir.

## 10. Test listesi

Her sağlayıcı ve platform için şunları deneyin:

1. Yeni kullanıcıyla kayıt: `201`, token geliyor, kayıt sonrası akış açılıyor.
2. Aynı sağlayıcıyla çıkış yapıp tekrar giriş: `200`, `isNewUser: false`, aynı hesap.
3. Önce email/şifre ile kayıt olunmuş bir email'in sağlayıcısıyla giriş: `409` ve yönlendirme
   mesajı.
4. Apple:
   - İlk girişte ad ve soyad hesaba geliyor.
   - "Hide My Email" seçilince relay adresiyle hesap açılıyor.
5. Facebook iOS: ATT izni verilmiş (klasik) ve verilmemiş (Limited Login) iki durum.
6. Snapchat: `needsEmail` → kod → hesap açılıyor. Yanlış kodda `400`, 10 dakikadan sonra `401`.
7. Ayarlar ekranı:
   - Şifreli hesaba Google bağlama, sonra Google ile giriş aynı hesaba açılıyor.
   - Bağlantıyı kaldırma.
   - Şifresiz hesapta tek yöntemi kaldırmaya çalışınca `409`.
8. Hesap silme: `204`, yerel oturum temizleniyor, aynı sağlayıcıyla tekrar giriş **yeni** hesap
   açıyor.
9. Uçak modu ya da backend'in `503` dönmesi: anlamlı mesaj gösteriliyor, uygulama çökmüyor.

## 11. Yapma

- Sağlayıcı token'larını, `pendingToken`'ı ya da bizim JWT'mizi loglama, analitiğe gönderme.
- `error` metnini parse ederek dallanma.
- Android'de Apple düğmesi gösterme.
- Hesapları client'ta birleştirmeye çalışma; `409`'da kullanıcıyı bölüm 4'teki gibi yönlendir.
- Backend'de açık olmayan sağlayıcıyı yayına çıkarma.

Bitince bana şunları raporla:
- Hangi sağlayıcının hangi platformda çalıştığı.
- Test listesinde geçemeyen maddeler.
- Bu belgeyle backend davranışı arasında gördüğün farklar.
- İhtiyaç duyduğun client ID / ayar değerleri.
