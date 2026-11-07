# Faus Switch online — backend geliştirme planı

Tarih: 6 Kasım 2026 (rev. 2: kullanıcı kararları işlendi) · Hedef paket:
`internal/application/game/games/fausSwitch`

Kaynaklar (salt okunur, `houseflowApp`): `FausCrew/Documents/fausSwitchBackendPrompt.md`,
`fausSwitchBackendBriefing.md` (courseVersion 7), `shared/Fixtures/fausSwitchProtocol.json`
(`fausSwitch.v1`), Swift motoru `shared/Sources/FausCrewCore/Games/FausSwitch/`, testler
`shared/Tests/FausCrewCoreTests/FausSwitch*`.

Durum: **F0–F6 tamam (7 Kasım 2026); sıradaki tek release deploy.** 7 faz (F0–F6).

## 0. Kullanıcı kararları (6 Kasım 2026)

| # | Karar | Plana etkisi |
|---|---|---|
| K1 | Lobide renkler gösterilir. | Oturum modeline genel `lobbyState` alanı; `sessionSnapshot`, `PUT .../session` ve oturum okuma cevaplarında yazılır (§3). İstemciden okuması istenir (§9). |
| K2 | Yarış sonu 250 ms beklenebilir. | Koşan robot kalmadığında sonuç 30 tick'lik geri sarma penceresi kapanınca kesinleşir (§4.4). |
| K3 | Oyun katalogda tüm evlere açık. | Migration `fausSwitch` `Game` dokümanını `enabled: true`, boş rollout ile `$setOnInsert` yazar (0054 kalıbı). Brifingdeki "kapalı başlar" kuralının yerine geçer. |

## 1. Temel kurallar

- **`houseflowApp` salt okunur.** Orada dosya yazma/silme, git, build, test yok. Fixture
  kopyalanır, değiştirilmez. İstemciden istenenler §9'daki listeye ve devir notuna yazılır.
- **Referans Swift'tir.** Go, Swift'e uyar; simülasyon fixture'ını istemci üretir.
- **Tek release, ara deploy yok.** Fazlar commit olarak birikir. Release eski sürümle yan yana
  çalışabilmeli ve geri alınabilmeli (rolling deploy, §8).
- **Dosya düzeni House Rockets ile aynı** (`definition.go`, `module.go`, `runtime.go`,
  `simulation*.go`, `wireSnapshot.go`, `protocol.go`, `settings.go`, `resultPersistence.go`,
  `resultValidation.go`, `recoveryResult.go`, `runtimeCheckpoint.go`, `fixtures/`), motor
  dosyaları eklenir (§2).

## 2. Mimari: paket içi katmanlar

Tek paket (şablonla aynı), ama dosyalar dört katmana ayrılır ve bağımlılık yalnız aşağı doğrudur.

```
sözleşme   definition.go  protocol.go  settings.go  colors.go
           resultPersistence.go  resultValidation.go
              │
oturum     module.go  runtime.go  wireSnapshot.go  runtimeCheckpoint.go  recoveryResult.go
              │        (zaman, kontrol, girdi kuyruğu, JSON; motoru sürer)
simülasyon simulation.go  simulationState.go  simulationResult.go
              │        (tick'li yarış: geri sarma halkası, girdi/olay günlüğü, bitiş penceresi)
motor      metrics.go  course.go  courseData.go  camera.go  startGrid.go  physics.go
                       (saf, deterministik; time/json/sync yok)
```

- **Motor** Swift'in birebir karşılığıdır: her Go fonksiyonu yorumunda Swift sembolünü
  (`FausSwitchPhysics.resolveContacts`) ve brifing bölümünü (§4.4) anar. Swift'i bilen biri
  dosyaları yan yana okuyabilmeli. `time`, `encoding/json`, `sync` içe aktarmaz. Bunu küçük bir
  mimari testi `go/parser` ile denetler.
- **Simülasyon** zaman bilmez; yalnız tick, flip ve olay alır. Bütün geri sarma mantığı
  buradadır ve runtime olmadan test edilir.
- **Oturum** House Rockets runtime kalıbıdır: duvar saati → tick, kontrol grant'leri, girdi
  kuyruğu, snapshot zamanlaması, checkpoint.
- **Sözleşme** wire DTO'ları ve sonuç şemasıdır. Motor durumu ile wire modeli ayrı tutulur,
  dönüşüm yalnız `wireSnapshot.go` ve `simulationResult.go`'dadır.
- **Hatalar:** modüle özel kodlar (`runner_finished`, `not_in_lobby`, `not_running`)
  `helpers.ApplicationError{Key: module.ErrorCode(GameKey, …)}` ile döner. `protocolErrorFrom`
  bunu zaten taşıdığı için çekirdekteki `tickProtocolError` eşlemesine dokunulmaz. Ortak
  durumlar (`stale_control`, `rate_limited`) çekirdek sentinel'leriyle kalır.

## 3. Çekirdek değişikliği: lobi niyeti ve `lobbyState` (K1)

Bugün `selectColor` sunucuya ulaşamıyor: tick modülünün her niyeti kontrol yetkisi istiyor
(`gatewayV2.go:475`), lobide yetki yok. `roomCommand.go` ise bilinmeyen komutu yalnız tur
oyununa yolluyor. Çözüm genel bir yoldur; ileride takım, harita ya da karakter seçimi de
aynı yolu kullanır.

### 3.1. Arayüz

```go
// LobbyModule is an optional Game capability: player choices made before the race,
// kept on the session and frozen when the countdown starts.
type LobbyModule interface {
    // DecodeLobbyIntent returns ErrUnsupportedIntent for any other type.
    DecodeLobbyIntent(messageType string, payload json.RawMessage) (json.RawMessage, error)
    // ApplyLobbyIntent returns the new lobby state (a JSON object). Called only while
    // the session is lobby/readyWindow and the player has joined.
    ApplyLobbyIntent(session gameDomain.SessionSnapshot, playerID, messageType string,
        intent json.RawMessage) (json.RawMessage, error)
}
```

Uygulamada (F3) imza değişti: mevcut durum `session.LobbyState`'ten okunur, ayrı parametre
gereksizdi; `messageType` ise bir oyunun birden çok lobi niyeti olabilsin diye eklendi.

### 3.2. Akış

1. `DecodeV2ClientMessage`: oyun `LobbyModule` ise önce `DecodeLobbyIntent`'e sorulur →
   `decoded.LobbyIntent`. Sonra mevcut `DecodeIntent` yolu.
2. Gateway: `LobbyIntent`, `realtime.ready` gibi işlenir: grant istemez, komut hız sınırına
   tabidir, `manager.Dispatch` ile oda sahibine gider, başarıda `realtime.accepted` döner.
3. `roomCommand.go`: yeni dal → `ApplyLobbyIntentCommand` (CQRS handler). Oturum lobby veya
   readyWindow değilse ya da oyuncu katılmamışsa `<gameKey>.error.not_in_lobby`.
4. Domain: `GameSession.UpdateLobbyState(playerID, state, now)` (oyuncu kontrolü domain'de): aşama kontrolü, JSON nesnesi
   doğrulaması, `session.lobbyStateChanged` olayı, sürüm artışı. Oturum `ReplaceOne` ile tam
   doküman olarak yazıldığı için alan `SessionSnapshot.LobbyState` ve
   `gameSessionDocument.lobbyState` (`omitempty`) olarak gidip gelir.
5. Yayın: mevcut akış `sessionSnapshot`'ı yollar. `GameSessionToResponseModel` alanı
   `lobbyState` olarak yazar; **boşken yazmaz.** Böylece House Rockets, FauseTanks ve
   Tic-Tac-Toe fixture'ları ve istemcileri değişmez.
6. Runtime'a geçiş: `PreviewParams` ve `RuntimeParams`'a `LobbyState json.RawMessage`.
   `tickHost.go` her ikisine `snapshot.LobbyState`'i verir. Kurtarma nihai renkleri
   checkpoint'ten okur, lobi durumuna bakmaz.

### 3.3. Renk kuralları (`colors.go`, saf fonksiyonlar)

- Durum: `{"colors": {"<playerId>": "plum"}}`.
- Uygulamadan önce katılmamış oyuncuların kayıtları atılır (çıkan oyuncunun rengi boşalır).
- Renk boşsa ya da zaten bu oyuncununsa: oyuncunun eski kaydı silinir, yenisi yazılır.
  Başkasınınsa durum değişmez, cevap yine `accepted` (brifing §5).
- `assignColors(players, state) []Color`: koltuk sırasıyla geçerli talebini alır;
  talebi olmayan ya da geçersiz olana katalog sırasıyla ilk boş renk. `PreviewFrame` ve
  `NewRuntime` aynı fonksiyonu çağırır, aynı girdiyle aynı sonucu verir.

## 4. Simülasyon tasarımı

### 4.1. Değer tipleri (performans ve okunabilirlik)

- `runner`: `x, y, vy float64`, `gravity`, `boostedUntil, slowedUntil int64`,
  `usedBoosts, usedSlows uint16` (bit maskesi: 9 boost, 14 yavaşlatma), `status`,
  `finishedAt, eliminatedAt int64` (yoksa −1), `reason`. Yaklaşık 80 bayt, işaretçi yok.
- `world`: `tick int64`, `count int`, `runners [8]runner`. Kopyası tek `memcpy`
  (yaklaşık 700 bayt).
- Robot ölçeği yarış düzeyindedir: `runnerSize{width, height, probeInset}` yarış başında
  **çalışma anında** `runnerWidth * scale` çarpımıyla bir kez hesaplanır. Swift her çağrıda
  aynı çarpımı yaptığı için sonuç bit bit aynıdır.

### 4.2. Paylaşılan, değişmez veriler (süreç başına bir kez)

- **Yarış parkurları:** `n = 1…8` için `standard` + platformlar; katı kovaları düz dizi
  (CSR: `offsets []int32`, `indices []int32`). `solids(near:)` Swift'in ziyaret sırasını
  (`firstBucket` kuralı) korur ama closure'suz döngüyle çalışır.
- **Kamera tablosu:** tick `0…14.401` için `trail[t]`, `behindKeep[t] = 1 − behind(f₁)/behind(f₀)`,
  `aheadKeep[t]`. `turnCarry` = `gap * keep[t]`. Bu, Swift'in
  `gap * (1 - behind(next)/now)` ifadesiyle aynı işlem olduğu için sonuç bit bit aynıdır.
  Geri sarmada `turnBlend` yeniden hesaplanmaz. Bellek ~345 KB, tüm oturumlarca paylaşılır.
  Bir eşitlik testi tablonun her tick'te doğrudan formülle aynı olduğunu doğrular.

### 4.3. Geri sarma

- **Halka:** `[32]world`, indeks `tick % 32`. Her tick adımından önce durum yazılır.
- **Flip günlüğü:** `[32][]flip` (tick başına; `seat`, `inputSequence`, varış sırası).
  Dilimler `[:0]` ile yeniden kullanılır, ısındıktan sonra ayırma yok. **Olay günlüğü** de
  aynı yapıdadır: forfeit ve membershipRevoked yeniden simülasyonda kaybolmaz.
- **Birleştirme:** runtime bir scheduling adımında (60 Hz) gelen flip'leri toplar.
  `simulation.applyFlips(batch)`: en erken `clientTick`'e **tek** geri sarma yapılır; her
  tick'te önce olaylar, sonra flip'ler (koltuk, sonra varış sırası; her biri destek kontrolüyle)
  uygulanır, sonra `step`. En erken tick şimdiki tick ise geri sarma yapılmaz.
- **Kırpma:** `clientTick < now − 30` → `now − 30`; `> now` → `now`; yarış öncesi → 0.
- **Geçerlilik geri sarılmış dünyada:** robotun `running` olup olmadığı ve desteği kırpılmış
  `clientTick`'teki duruma göre bakılır, şimdiki tick'e göre değil. Böylece 5 tick önce elenmiş
  ama 10 tick önce flip yapmış oyuncunun flip'i geçerli olur. Desteksiz flip hata değil
  no-op'tur; sıra numarası işlenmiş sayılır. `runner_finished` yalnız robot kırpılmış tick'te
  zaten bitmiş/elenmişse döner.

### 4.4. Yarış sonu penceresi (K2)

- Tick `T`'de koşan robot kalmazsa `pendingEnd = T`. Snapshot'lar sürer, `phase = playing`.
- `now ≥ T + 30` olunca hâlâ koşan yoksa `raceComplete`; sonuç tick'i `T`'dir (süre ve
  sıralama `T`'deki durumdan). Bu arada gelen bir geç flip son robotu kurtarırsa
  `pendingEnd` silinir ve yarış sürer. Bitiş sıraları da pencere içinde değişebilir.
- `timeLimit` 14.400'de kesindir; pencere beklenmez (`clientTick ≤ 14.400`'e kırpılır).
- Kontrol hakkı (`FrameHeader.IsAlive`) yarış boyunca "oyuncu yarıştan çıkmadı" demektir,
  robot durumu değil. Bu sayede elenen oyuncunun geç flip'i gateway'den geçer.

### 4.5. Checkpoint ve kurtarma

Checkpoint'e son tick'in `world`'ü, `runnerScale`, nihai renkler, `pendingEnd`, oyuncu başına
`lastProcessedInputSequence` ve `controlGeneration` yazılır. Halka ve girdi günlükleri yazılmaz
(F2'de netleşti: halka olmadan günlükler yeniden oynatılamaz, işe yaramaz). Kurtarmadan sonra
geri sarma tabanı geri yüklenen tick'tir; daha eski `clientTick` ona kırpılır. O tick'in canlı
dünyası girdilerini zaten içerdiği için günlüğe gerek kalmaz.

F4'te netleşti: `runnerScale` ayrıca yazılmaz (robot sayısından çıkar), `pendingEnd` simülasyon
durumundaki `stoppedAtTick`'tir. `controlGeneration` yazılır ama kurtarmada hiçbir kontrol yaşamaz;
geri yüklenen yalnız `lastProcessedInputSequence` ve oyuncunun yarıştan çıkmış olmasıdır.

## 5. Belirleyici sayısal kurallar

1. **Untyped sabit katlama yok.** Türetilmiş her değer çalışma anında, tipli `float64`
   değişkenlerle hesaplanır: `ceiling := worldHeight - railThickness`,
   `turnLength := turnRadius * pi / 2` (`pi` tipli değişken), `width := runnerWidth * scale`,
   `verticalAhead := worldHeight / verticalZoom - verticalBehind`.
2. **İşlem sırası birebir:** `x += speed*dt + carry`,
   `focus = spawnX + runSpeed*(float64(tick)*tickDuration)`, parantezler Swift'teki gibi.
3. **Trigonometri yok.** Düzlüklerin yönü tam çeyrek dönüş tablosudur; çizim taşınmaz.
4. **Yuvarlama:** `rounded()` = `math.Round`, `Int(x)` = `int(x)`.
5. **Sıra bağımlı yerler:** kova ziyaret sırası, portal kimlik sırası, önce robotlar sonra
   katılar, en fazla 4 tur, `1e-9` eşikleri.
6. **Parkur verisi elle yazılmaz:** `courseData.go`, Swift `FausSwitchCourse.swift`'ten
   tek kullanımlık bir betikle (scratchpad'de) üretilir. Başlığında kaynak dosya ve
   courseVersion yazar. İstemciden istenen parkur dökümüyle (§9) tam karşılaştırılır.
7. **FMA yok.** Go derleyicisi `x*y + z`'yi tek FMA komutuna birleştirebilir (arm64'te
   birleştiriyor); bu bir kez yuvarlar, Swift iki kez. Toplanan/çıkarılan her `float64` çarpım
   `float64(...)` dönüşümüne sarılır (Go spesifikasyonuna göre dönüşüm yuvarlar ve birleştirmeyi
   engeller): `y += float64(velocityY * tickDuration)`. `architecture_test.go` bunu AST + tip
   bilgisiyle denetler, `swiftParity_test.go` arm64'te sonucunu.

## 6. Fazlar

Her faz kendi testleriyle biter; `go build ./...` ve `go test ./...` her faz sonunda temiz.
Sıra: F0 → F1 → F2 → F4 → F5 → F6. **F3 bağımsızdır** (çekirdek + `colors.go`), F1–F2 ile
paralel yürüyebilir, F4'ten önce bitmelidir.

### F0 — Sözleşme iskeleti ve ön kontroller

- `fixtures/fausSwitchProtocol.json` birebir kopya (sha256 devir notunda).
- `definition.go`: `GameKey`, `ProtocolVersion = 2`, `CourseVersion = 7`,
  `ContractRevision = "fausSwitch.v1"`, 2–8 oyuncu, 30 sn / 3 sn, 120 Hz fizik,
  60 Hz scheduling, 20 Hz snapshot, 20/sn girdi, 30 tick geri sarma, 2/6/10 sn, 14.400 tick.
- `protocol.go`: niyet, `welcome`, `snapshot` (`runnerScale` dahil), `result` DTO'ları; boş
  alanlar açıkça `null` (pointer, `omitempty` yok). Hata kodları sabitleri.
- `settings.go`: fixture `settings` şeması, `ResolveConfig` ve `ClientSettings`.
- `tests/fausSwitchContract_test.go`: fixture'daki 17 mesajın her biri için decode → encode →
  JSON eşitliği; `rules`, `playerColors`, `errorCodes`, `http` sabitlerle aynı.
- Modül henüz kaydedilmez.

**Durum: tamam (6 Kasım 2026).** Dosyalar: `definition.go`, `protocol.go`, `models.go`
(wire modelleri; House Rockets/FauseTanks kalıbı), `settings.go`, `fixtures/fausSwitchProtocol.json`
(sha256 `ba32a5be803b289337c9a57ab804255e02a69366d657b54fd685fd7fd0029c6d`, kaynakla aynı),
`tests/fausSwitchContract_test.go`. Notlar:

- Modül nesnesi F4'te gelir. Bu yüzden sözleşme fonksiyonları paket düzeyindedir:
  `ResolveConfig`, `RuntimeSettings`, `Welcome`, `DecodeFlip`, `DecodeSelectColor`. F4'teki
  `gameModule` metotları bunları çağırır (tek satır), F3 `DecodeLobbyIntent` de `DecodeSelectColor`'ı.
  `join` / `ready` / `ping` gateway çözücüsüyle, `selectColor` / `flip` / `resync` zarf + paket
  çözücüsüyle sınanıyor; tam gateway yolu F3 ve F4'te teste eklenir.
- Operatörün ayarlayabildikleri: snapshot hızı, girdi hızı, heartbeat, zaman aşımı, yeniden bağlanma
  payı. Fizik hızı, geri sarma (250 ms) ve yarış süresi (120 sn) sözleşme değeridir, ayarla değişmez.
- Testler: 17 fixture mesajının JSON eşitliği, `null` yazımı, snapshot kuralları (ölçek = robot
  sayısından, koltuk/renk tekliği, durum ↔ bitiş/eleme alanları, artan pad dizileri), sonuç
  kuralları (§4.5 sıralaması, tek bitiren kazanan), `countdown` ve `crowdedCountdown`'ın brifing
  tablolarıyla değer değer eşitliği, HTTP örnekleri, ayar sınırları, flip fuzz testi.
  Fixture kopyası üzerinde yapılan geçici bozmalarla (ölçek, kazanan, fazla alan, eksik `null`)
  testlerin yakaladığı doğrulandı; kopya kaynakla aynı hale geri getirildi.
- **Sözleşme tutarsızlığı (istemciye, §9.6):** Swift `FausSwitchResultDTO.validate`, `completed`
  sonuçta `winnerId == null` ise birden fazla 1. sıra istiyor. Brifing §4.5'e göre kimse
  bitirmediğinde (süre doldu ya da hepsi elendi) tek bir 1. sıra varken de `winnerId` `null`dır;
  istemci bu geçerli sonucu `invalidResponse` sayar. Backend brifingi uygular.

### F1 — Deterministik motor

- `metrics.go`, `course.go` + üretilmiş `courseData.go`, `camera.go` (tablo dahil),
  `startGrid.go`, `physics.go` (`supportProbe`, `isSupported`, `flip`, `step`,
  `resolveContacts`, `crash`, `ranks`).
- Testler:
  - `fausSwitchCourse_test.go`: brifing §4.2 listeleri, uzunluk 20.400, bitiş 20.280; start
    grid `n = 1…8` (Swift `FausSwitchCollisionTests` değerleri, brifingin `n = 2/5/8` tabloları).
  - `fausSwitchCamera_test.go`: `FausSwitchTrackTests` değerleri (tavan 19.936, tırmanışta
    `focus − 140`, iki dönüşte toplam carry ≈ 0, düzlükte tam 0); tablo = doğrudan formül.
  - `fausSwitchPhysics_test.go`: `FausSwitchPhysicsTests` senaryoları.
  - `fausSwitchCollision_test.go`: üst üste, taban tabana, arka arkaya; **8 robotlu tam
    yarış** (sabit tohumlu flip planı, 14.400 tick, hiçbir tick'te iki koşan robot iç içe değil).
  - `fausSwitchSimulation_test.go`: `fixtures/fausSwitchSimulation.json` karşılaştırıcısı
    (tam eşitlik). Dosya gelene kadar `t.Skip`; gelince 2, 4 ve 8 kişilik senaryolar zorunlu.
  - Mimari testi: motor dosyaları `time`/`encoding/json`/`sync` içe aktarmaz.
  - Benchmark: `BenchmarkStep8`, hedef 0 ayırma/op.

**Durum: tamam (6 Kasım 2026).** Motor: `metrics.go`, `courseData.go` (üretilmiş, 75 blok),
`course.go`, `camera.go`, `startGrid.go`, `physics.go`. Testler (paket içi): `course_test.go`,
`camera_test.go`, `physics_test.go`, `collision_test.go`, `swiftParity_test.go`,
`simulationFixture_test.go`, `architecture_test.go`. Notlar:

- **Swift ile bit düzeyinde eşitlik kanıtlandı.** Swift motorunun dört dosyası (Course, Models,
  Physics, Track; bot dosyaları hariç) houseflowApp'e dokunmadan scratchpad'e kopyalanıp
  `swiftc` ile derlendi. 1–8 robot, 3 tohum ve 2 flip sıklığıyla 48 senaryo (37 MB, her
  float bit deseniyle) iki motorda çalıştırıldı; çıktılar bayt bayt aynı. Senaryo başına
  sha256 özetleri `testdata/swiftParity/digests.txt`'te, Swift üreticisi ve yeniden üretme
  adımları `testdata/swiftParity/main.swift`'te. `TestSwiftParityTrace` her çalıştırmada
  Go izini bu özetlerle karşılaştırır.
- **Bulunan ve düzeltilen sapma:** ilk karşılaştırmada `y`'de son bit farkları çıktı. Sebep
  arm64'te FMA birleştirmesiydi; §5.7 kuralı eklendi, 7 noktada uygulandı.
- **Robot iç içe geçmesi referans davranışı:** Rastgele flip'li yarışlarda bir robot bir blokla
  başka bir robot arasında sıkıştığında (ya da portal çıkışında bir robotun üstüne indiğinde)
  katı en son ittiği için iki robot bir tick boyunca iç içe kalabiliyor (en fazla ~22 birim).
  Swift aynısını yapıyor (iz eşitliği). Bu yüzden "hiçbir tick'te iç içe değil" hedefi şu
  değişmezlerle değiştirildi: hiçbir robot katı içinde bitmez, robot örtüşmesi iki tick üst üste
  sürmez (2–8 robot × 20 tohum). Swift'teki bot demo testi bunu yakalamıyor çünkü botlar bu
  durumlara girmiyor.
- **Swift'te çalışmayan test (istemciye, §9.7):** `testPortalNeverDropsARobotBehindTheTrail`,
  `testPortalIsJustBigEnoughForARobot`'un içine iç fonksiyon olarak yazılmış; XCTest onu hiç
  çalıştırmıyor. Ayrıca "x geri gitti" kontrolü tam da bu senaryoda (iz robotu girdiği yerin
  önüne çektiğinde) tutmuyor. Go portunda ışınlanma yer çekimi değişimiyle algılanıyor.
- **Simülasyon fixture'ı:** yükleyici hazır, dosya yokken atlanıyor. Biçim §9.1'de. Biçim,
  Swift `JSONEncoder` ile üretilmiş geçici bir dosyayla denendi: geçti, `1e-12`'lik bir değişikliği
  yakaladı, dosya silindi.
- **Benchmark (Apple M2):** `BenchmarkStep8` 8 robot için adım başına 383 ns, 0 ayırma
  (`TestStepDoesNotAllocate` 0'ı zorunlu tutar). 30 tick'lik en uzun geri sarma ≈ 11,5 µs. Her
  60 Hz zamanlama adımında bir geri sarma olsa bile oturum başına saniyede < 1 ms CPU.
- Mimari testi: motor dosyaları yalnızca `cmp`, `math`, `slices` içe aktarır; trigonometri,
  `Pow`, `Sqrt`, `FMA` yasak.

### F2 — Simülasyon: geri sarma ve yarış sonu

- `simulationState.go` (`world`, `runner`, günlükler), `simulation.go` (§4.3–4.4),
  `simulationResult.go` (sıralama → sonuç oyuncuları; `distance` = son x).
- Testler (`fausSwitchRewind_test.go`):
  - 30 tick geç flip = zamanında flip: tek robot; üst üste iki robot, alttakinin geç flip'i;
    taban tabana iki robot.
  - Aynı adımda birden çok oyuncudan gelen flip'lerin birleştirilmiş geri sarması = tek tek
    uygulama.
  - Kırpma (eski ve gelecek tick), yinelenen sıra, desteksiz no-op, kırpılmış tick'te geçerlilik.
  - Geri sarılan aralıktaki forfeit korunur.
  - Yarış sonu penceresi: geç flip son robotu kurtarır; pencere kapanınca sonuç tick'i `T`.
- Benchmark: `BenchmarkRewind8` (8 robot, 30 tick), hedef 0 ayırma/op.

**Durum: tamam (6 Kasım 2026).** Dosyalar: `simulation.go` (dünya, halka, günlükler, flip
birleştirme, eleme, yarış sonu), `simulationState.go` (checkpoint ve sıkı doğrulama),
`simulationResult.go` (sıralama, kazanan, mesafe), `simulation_test.go`. Test dosyası adı plandaki
`fausSwitchRewind_test.go` yerine paketin diğer testleriyle uyumlu `simulation_test.go` oldu.
Netleşen kurallar:

- **Zamanlama:** `ring[t]`, tick `t`'nin girdilerinden önceki dünyadır. Şimdiki tick'e gelen girdi
  canlı dünyaya hemen uygulanır, böylece her flip'in sonucu (`flipped`, `unsupported`,
  `notRunning`) aynı çağrıda bilinir. Geri sarma `ring[c]`'yi yükler, sonra `now`'a kadar
  "girdiler, sonra adım" diye ilerler ve halkayı yeniden yazar.
- **Bir tick içinde sıra:** önce elemeler, sonra flip'ler (koltuk, sonra `inputSequence`). Flip
  gövdeyi hareket ettirmediği için farklı koltukların flip'leri birbirinin yerini değiştiremez,
  varış sırası sonucu değiştirmez. Canlı bir eleme de şimdiki tick'i halkadan yeniden oynatır;
  böylece aynı tick'teki flip + forfeit hem canlıda hem sonraki geri sarmalarda aynı biter
  (test ve mutasyonla doğrulandı).
- **Reddedilen flip:** kırpılmış tick'te robot koşmuyorsa sonuç `notRunning` olur (runtime:
  `runner_finished`) ve flip günlükten çıkarılır. Sonraki bir geri sarma robotu o tick'te koşar
  hale getirse de istemciye reddedildiği söylenmiş flip uygulanmaz. Desteksiz flip günlükte kalır.
- **Yarış sonu:** `stoppedAt`, koşan robot kalmadığı ilk tick'tir ve tam olarak kimse koşmadığı
  sürece doludur (checkpoint doğrulaması da buna dayanır). Pencere `stoppedAt + 30`'da kapanır,
  sonuç tick'i `stoppedAt`'tir. 14.400'de pencere açıksa yarış beklemeden `raceComplete` olarak
  `stoppedAt`'te biter, açık değilse `timeLimit`.
- **Testler:** 250 ms geç flip = zamanında flip (start grid, üst üste duranların alttakisi ve
  üsttekisi, taban tabana) ve ikisi de çıplak fizik döngüsüyle aynı. Rastgele özellik testi
  (2–8 robot × 6 tohum, 0–40 tick gecikme, karışık batch'ler, rastgele forfeit): geri sarmalı yarış,
  aynı girdilerin baştan zamanında uygulandığı yarışla bit düzeyinde aynı. Birleştirilmiş batch =
  tek tek uygulama. Kırpma (eski, gelecek, başlangıç öncesi, restore öncesi). Desteksiz no-op,
  reddedilenin düşmesi, geri sarmada korunan forfeit, eleme/flip sırası. Yarış sonu penceresi: duvara
  sıkışıp 517'de elenen son robotu 522'de gelen 492 tick'li flip kurtarır, yarış 731+30'da 731
  sonucuyla biter. Süre sınırı, iptal, sonuç sıralaması/kazanan, JSON checkpoint gidiş-dönüş
  (devam eden yarış bit düzeyinde aynı sürer), 22 bozuk checkpoint reddi, geri sarmada 0 ayırma.
  Dört mutasyonla (halka yeniden yazılmıyor, eleme canlıya yamalanıyor, `stoppedAt` sıfırlanmıyor,
  reddedilen flip kalıyor) testlerin yakaladığı doğrulandı.
- **Benchmark (Apple M2):** `BenchmarkRewind8` (8 robot, 30 tick geç flip) 29,7 µs, 0 ayırma.
  Sürenin %95'i fizik adımı: yarış başında 8 robot birbirine değerken adım ~1 µs. Batch başına tek
  geri sarma ile en kötü durumda oturum başına saniyede 60 × 29,7 µs ≈ 1,8 ms CPU.
- Mimari testi simülasyon dosyalarını da kapsar (yalnız `cmp`, `errors`, `math`, `slices`; FMA
  denetimi dahil).
- "Aynı `inputSequence` iki kez uygulanmaz" testi runtime'ın işidir (F4): simülasyon yalnız kabul
  edilmiş flip'leri alır.

### F3 — Çekirdek lobi niyeti ve renkler (bağımsız)

- §3'teki çekirdek değişiklikleri: `module.LobbyModule`, `protocolV2.go`, `gatewayV2.go`,
  `roomCommand.go`, `ApplyLobbyIntentCommand`, domain `LobbyState` + olay, Mongo dokümanı,
  response modeli (`omitempty`), `PreviewParams` / `RuntimeParams` alanı, `tickHost.go`.
- `colors.go` ve modülün `DecodeLobbyIntent` / `ApplyLobbyIntent` uygulaması (modül kaydı
  F4'te olduğu için bu fazda test çift modülüyle sınanır).
- Testler: ilk talep kazanır; başkasının rengine talep `accepted` ama değişmez; oyuncu renk
  değiştirir; çıkan oyuncunun rengi boşalır; `countdown`/`running`'de ve katılmamış oyuncuda
  `not_in_lobby`; `lobbyState` `sessionSnapshot`'ta görünür, diğer oyunlarda hiç yazılmaz;
  Mongo gidiş-dönüşü; `assignColors` (8 oyuncu 8 renk, çakışma koltuk sırasıyla). Mevcut
  oyunların gateway ve kontrat testleri değişmeden geçer.
- ADR-030: genel lobi niyeti yolu ve `lobbyState`.

**Durum: tamam (6 Kasım 2026).** ADR-030 `architecture-decisions.md`'de. Netleşenler:

- **Yönlendirme:** oda komut işleyicisi (`commandProcessor`) oyun kaydını ve oturumun
  `gameKey`'ini alır; mesaj o oyunun lobi niyetiyse `ApplyLobbyIntentCommand`, değilse eskisi
  gibi tur hamlesi. Bir mesajın lobi niyeti olup olmadığı `DecodeLobbyIntent`'e boş yükle
  sorularak anlaşılır (`ErrUnsupportedIntent` dışındaki her cevap "evet"), bu yüzden
  `DecodeLobbyIntent` türü yükten önce kontrol eder.
- **Komut:** `ApplyLobbyIntentCommand` üyelik kontrolü yapar ve makbuzla kalıcıdır (tekrar =
  aynı sonuç, farklı içerikle aynı `commandId` = `game.error.command_id_reused`). Başkasının
  rengine talep `accepted` döner ve sürüm artırmaz. Hata kodları oyunun ad alanında:
  `not_in_lobby` (aşama, katılmamış ya da ayrılmış oyuncu), `invalid_input`.
- **Çıkan oyuncu:** kaydı bir sonraki niyette temizlenir; o zamana kadar `lobbyState`'te durur.
  `assignColors` kadroda olmayanları zaten yok sayar. İstemci yalnız katılmış oyuncuların
  rengini çizmeli (§9.3).
- **`assignColors`:** okuma esnektir; bozuk durum ya da bilinmeyen renk talep sayılmaz.
- **Testler:** `colors_test.go` (talep kuralları tablosu, niyet çözme, `assignColors` 8 oyuncu ve
  koltuk sırasıyla çakışma); `tests/lobbyIntent_test.go`: domain kuralları (aşama, katılmamış,
  JSON nesnesi, aynı durum sürüm artırmaz, olay, kopya, restore doğrulaması), DTO'da alanın
  yalnız doluyken yazılması, protokol çözme (lobi oyunu / lobi niyeti olmayan oyun), Mongo ile
  handler (ilk talep, tekrar, `commandId` yeniden kullanımı, geçersiz renk, ev dışı kullanıcı,
  başkasının rengi, değiştirme, çıkan oyuncu, gidiş-dönüş), geri sayımda kapanma, iki gateway
  instance'ı üzerinden uçtan uca (kabul, karşı tarafın `sessionSnapshot`'ında renkler,
  `invalid_input` reddi). Modül kaydı F4'te olduğu için çekirdek testleri Tic-Tac-Toe'ya
  `fausSwitch` lobi fonksiyonlarını takan bir test çifti kullanır. Tüm test paketi (Mongo
  replica set ve Redis ile) değişmeden geçiyor.

### F4 — Runtime, snapshot, checkpoint ve kayıt

- `runtime.go`: duvar saati → tick (120 Hz, 60 Hz scheduling), kontrol grant / heartbeat /
  zaman aşımı, oyuncu başına girdi kuyruğu → adım başına tek `applyFlips`, `Reconcile` →
  forfeit/membershipRevoked olayı, bağlantı kopması → `connected = false` (robot koşar),
  `Cancel`, `runtimeOverloaded`.
- `runnerScale` `NewRuntime` ve `PreviewFrame`'de `len(Players)`'tan hesaplanır; çekirdek
  kadroyu geri sayıma girerken dondurduğu için (`roomLobby.go:200`) geri sayımda çıkan oyuncu
  koltuğunu korur ve tick 0'da `forfeit` olur.
- `wireSnapshot.go`: ortak kısım frame başına bir kez işlenir, izleyenin kendi
  `controlGeneration`'ı yerine eklenir (House Rockets `frameView` kalıbı). `runners[]` koltuk
  sırasıyla; `usedBoosts`/`usedSlows` artan dizi; geri sayım tick 0 snapshot'ı `PreviewFrame`'den.
- `runtimeCheckpoint.go`, `recoveryResult.go`: §4.5.
- `module.go`: `TickModule` + `LobbyModule`; `builtin.go` kaydı.
- Testler (`fausSwitchRuntime_test.go`): 4 kişi → 1, 5 kişi → 0,75; geri sayımda çıkan ile 0,75
  kalır; `stale_control`, `runner_finished`, `not_running`, `rate_limited`, yinelenen sıra;
  bağlantı kopması; checkpoint → restore → aynı dünya ve aynı sonraki snapshot'lar; snapshot
  şekli fixture'daki `countdown` / `crowdedCountdown` / `playing` ile aynı (`crowdedCountdown`
  tick 0'ı değer değer).

**Durum: tamam (7 Kasım 2026).** Dosyalar: `runtime.go`, `wireSnapshot.go`,
`runtimeCheckpoint.go`, `recoveryResult.go`, `module.go`, ayrıca F5'ten öne alınan
`resultPersistence.go` ve `resultValidation.go`; `games/builtin.go` kaydı. Netleşenler:

- **Sonuç kodlayıcısı F4'e alındı.** Modülün kaydı `module.Game`'i, o da `ResultCodec`'i
  (`CancellationResult`, `FinalizeResult`, `DecodeResult`) istiyor; runtime da yarış bitince
  sonucu önermek zorunda. F5'e migration'lar ve fixture'daki sonuç örnekleriyle eşitlik testi
  kaldı. Kayıt deploy'dan önce zararsızdır: oyunun yeni oturum alıp almadığına Game dokümanı
  karar verir, o da F5'teki migration'la gelir.
- **Adım sırası (60 Hz):** bind/disconnect kuyruğu → süresi dolan kontroller → yarışın tick'leri
  → adımın flip'leri tek batch ve tek geri sarma ile → gerekirse frame. Flip'ler adımlamadan sonra
  uygulandığı için istemcinin "şimdi"si sunucunun yeni tick'ine göre kırpılır. Bir `Advance`
  en fazla 250 ms kapsar (30 tick, simülasyonun tek adım sınırı); daha uzun boşluk
  `runtimeOverloaded` ile iptal eder.
- **Flip kabulü:** `Submit` yalnız kontrolü (bağlantı, generation, heartbeat), sırayı ve hızı
  denetler. `inputSequence ≤` en yüksek sıra ise flip **hatasız kabul edilir ve uygulanmaz**
  (brifing §6; `stale_control` değil). Hız: saniyede `maximumInputRateHz`, oyuncu başına en çok
  8 bekleyen flip; fazlası `realtime.error.rate_limited`. Uygulanan ve desteksiz flip'i
  `lastProcessedInputSequence` onaylar. Robot kırpılmış tick'te zaten durmuşsa adım sırasında o
  bağlantıya `fausSwitch.error.runner_finished` gider. Yarış bitmişse `not_running`.
- **Hata kodu:** `runner_finished` sebepsiz bir `helpers.ApplicationError`'dır. Çekirdeğin
  `tickProtocolError`'ı önce sentinel'lere baktığı için `ErrPlayerEliminated` sarılsaydı kod
  sözleşmede olmayan `player_eliminated` olurdu; bunu bir test korur.
- **Yarıştan çıkma:** `Reconcile` oturumdan çıkanı `forfeit`, evden çıkanı `membershipRevoked` ile
  eler; oyuncu bir daha bind edemez, her girdisi `runner_finished` alır. `FramePlayer.IsAlive`
  "yarıştan çıkmadı" demektir: elenen robotun oyuncusu kontrolü tutar, kurtaran geç flip
  gateway'den geçer. Bağlantı kopması eleme değildir (robot girdisiz koşar); yarış ortasında
  oyuncu azalınca iptal yok (House Rockets ile aynı), son koşan robot durunca pencereyle biter.
  Yeniden bağlanma payı sunucuda bir sonuç doğurmaz, istemciye bilgi olarak gider.
- **Yeniden bind:** yeni generation, en yüksek ve işlenmiş sıra 0'a döner, bekleyen flip'ler
  atılır (FauseTanks ile aynı).
- **Kritik anahtar:** kontrol devri, yarıştan çıkma, robot durumu (bitirdi/elendi), yarışın durması
  ve sonucu. Hareket ve flip kritik değildir.
- **Önizleme:** `PreviewFrame` tick 0 dünyasını, nihai renkleri (`assignColors` +
  `lobbyState`) ve `connected: true` kontrolleri yazar; fixture'daki `countdown` ve
  `crowdedCountdown` ile **değer değer** aynıdır (iki oyunculuda ilk koltuğun `ocean` talebi dahil).
- **Testler:** `runtime_test.go` (ölçek ve geri sayımda çıkan, kontrol/bind/heartbeat/kopma/
  resync, flip uygulama, yinelenen sıra, hız sınırı, 20 tick geç flip = zamanında flip,
  `runner_finished`/`not_running`, çıkanlar ve sıralamalı sonuç, tam yarış sonucu + kodlama +
  `FinalizeResult` + kesinleşen frame, sekiz bozuk sonuç reddi, aşırı yük iptali, checkpoint →
  restore → aynı dünya ve aynı frame'ler, bitmiş yarışın restore'u, iptal sebebiyle restore, sekiz
  bozuk checkpoint reddi, kritik anahtar, frame görünümü ve generation ekleme, flip'siz adımda 0
  ayırma). `tests/fausSwitchContract_test.go`: önizleme = fixture (değer değer), `playing`
  şekli, kayıtlı modülle gateway çözümü. Yedi mutasyonla (reddin düşmesi, çıkanın kabulü,
  yinelenen sıranın uygulanması, `left`'in yazılmaması/geri yüklenmemesi, `IsAlive`'ın sabitlenmesi,
  flip batch'inin atlanması) testlerin yakaladığı doğrulandı.

### F5 — Kalıcı sonuç ve katalog (K3)

- ~~`resultPersistence.go`, `resultValidation.go`~~: F4'te yazıldı (yukarıda). F5'te fixture'daki
  `result` / `cancelledResult` örnekleriyle eşitlik ve sonuç uç noktası testi kalır.
- Migration `0060_fausSwitchLocalizations`: sonuç ve lobi hata mesajları (en/tr, 0053 kalıbı).
- Migration `0061_fausSwitchConfig`: `enabled: true`, `rollout.houseIds: []`, settings ve kurallar
  boş (kod varsayılanları; katalog `ClientSettings`'i çözülmüş yapılandırmadan üretir), `$setOnInsert`.
  Numaralar yazım anında kontrol edilir.
- Testler (`fausSwitchResult_test.go`): fixture `result` / `cancelledResult` eşitliği,
  sıralama eşitlikleri, geçersiz sonuç reddi; katalogda `fausSwitch` `settings` ile görünür.

**Durum: tamam (7 Kasım 2026).** Netleşenler:

- **Numaralar 0060 ve 0061.** 0059, stash'te bekleyen bildirim işinin
  (`0059_notificationDelivery.go`, `push-noti` stash'i). Runner sürüm başına atlar, sıralama
  zorunlu değil; bildirim işi sonradan gelse de 0059 ayrıca uygulanır. Bir test aynı sürümün iki
  kez kullanılmadığını denetler.
- **0060** `$setOnInsert` ile en/tr mesajlar: çekirdeğin oyun ad alanında ürettiği
  `result_not_found`, `result_conflict`, `invalid_result`, `finalizing`,
  `runtime_completion_required` ve oyunun `not_in_lobby`, `invalid_input`, `runner_finished`
  kodları. İki kez çalışınca her anahtar yine iki doküman (Mongo testi).
- **0061** Game dokümanını tüm evlere açık, kurallar ve settings boş (kod varsayılanları) ekler,
  var olan dokümana dokunmaz. Mongo testi: tohum sürüm 1, her eve açık, varsayılan kural ve
  ayarlar; evin kataloğunda `settings` fixture'ın `settings`'iyle değer değer aynı; operatörün
  değişikliği migration yeniden çalışınca korunur. Game dokümanı olmadan oyun kapalı kalır.
- **Var olan bir test değişti:** `TestGameConfigControlsNewSessionsOnly` evin kataloğunda tam iki
  oyun bekliyordu; artık anahtar sırasıyla `fausSwitch`, `houseRockets`, `houseTanks`.
- **Sonuç testleri (`tests/fausSwitchResult_test.go`):** fixture'daki `result` oturumuna karşı
  doğrulanır, `FinalizeResult` → depolama → `DecodeResult` sonrası hem `fausSwitch.result`
  payload'ı hem HTTP `result` cevabıyla değer değer aynı; 23 bozuk sonuç reddi; süre dolunca tek
  başına 1. olan ama bitirmeyen robotun kazanan sayılmaması (istemci notu §9.6) ve aynı tick'te
  bitiren iki robotun 1. sırayı paylaşması; kadrodan iptal sonucu (geri sayımdan önce çıkan yok,
  sıra/bitiş/mesafe uydurulmaz).
- **`cancelledResult` örneği:** `startedAt: null` olan bu şekli sunucu hiç üretmez (sonuç yalnız
  başlamış yarıştan doğar; lobide iptal sonuç yazmaz, sonuç uç noktası `result_not_found` döner).
  FauseTanks'te de aynı; istemci şekli çözebiliyor, sorun yok. Devir notuna bilgi olarak yazılacak.

### F6 — Entegrasyon, yük, benchmark ve devir

- `tests/fausSwitchIntegration_test.go` (geçici replica-set Mongo + Redis, scratchpad'de):
  2 ve 8 istemcili tam akış (lobi → renk → `sessionSnapshot.lobbyState` → hazır → geri sayım →
  yarış → sonuç), owner değişimi sonrası kurtarma.
- `BenchmarkFausSwitchSession8`: 8 robot, robot başına 20 flip/sn (0–30 tick rastgele gecikme),
  120 sn yarış. Ölçülenler: ns/tick, ayırma/tick, oturum başına CPU yüzdesi. `runtimeOverloaded`
  eşiği bununla doğrulanır. Kaba beklenti: adım 8 robotta ~10–20 µs; 60 Hz'de en çok 60
  birleştirilmiş geri sarma/sn × ~0,3–0,6 ms → oturum başına çekirdeğin ~%2–4'ü.
- Bant genişliği: 8 robotlu snapshot (ham ve deflate), 20 Hz × 8 izleyici.
- Belgeler: `multiplayerBaseline.md`'ye yeni bölüm; ADR-031 (geri sarma, kırpılmış tick'te
  geçerlilik, yarış sonu penceresi); `external/doc/fausSwitchClientIntegration.md` devir notu
  (kararlar, fixture sha256, benchmark, §9).

**Durum: tamam (7 Kasım 2026).** Netleşenler:

- **Entegrasyon (`tests/fausSwitchIntegration_test.go`).** İki gateway instance'ı, gerçek Mongo
  (replica set) ve Redis. Tam akış 2 ve 8 istemciyle: renk seçimi (çakışan talep `accepted`, durum
  değişmez) → her sokette `lobbyState` → hazır (2 kişide hazır penceresi, 8 kişide son hazırla
  hemen geri sayım) → geri sayım önizlemesi (renkler, ölçek 1 / 0,75, tick 0) → grant → her
  oyuncu kendi robotunu rayda görünce flip → onay ve yer çekimi değişimi → yalnız kendi
  robotunda `controlGeneration` → yarış kendiliğinden biter (~8 sn) → `ended` snapshot'ı, sonra
  sonuç; socket/HTTP sonuç bayt eşitliği her iki instance'ta; dışarıdan biri 403. Kurtarma:
  çocuk süreçteki sahip yarış ortasında öldürülür; aynı soketler yeni epoch'la grant alır, tick
  kesinti için ilerlemez, renkler ve uygulanmış flip korunur, eski generation `stale_control`,
  yeni generation'la flip uygulanır, yarış tek sonuçla biter. 8 istemci için ev üyeleri test
  içinde eklenir; ortak soket yardımcısının kuyruğu 1024'e çıktı ve `awaitWithin` eklendi.
- **Benchmark (`runtime_bench_test.go`).** `BenchmarkFausSwitchSession8`: adım 30–34 µs, tick
  15–17 µs, tick başına 1,9 ayırma, oda başına çekirdeğin ~%0,2'si (tahmin %2–4 idi).
  `BenchmarkFausSwitchCatchUpStep` (tam pencere geri sarma): ~80 µs. Bant (120 sn): bağlantı
  başına 3,3 KB/frame, deflate ile 0,7 KB; 64 KB/s ham, 14 KB/s sıkıştırılmış.
  Ayrıntı `multiplayerBaseline.md` §11.
- **Benchmark'ın bulduğu iyileştirme.** `criticalKey` her adımda iki kez string üretiyordu
  (ayırmaların %41'i). Artık adım başına iki yeniden kullanılan bayt tamponu karşılaştırılır;
  string yalnız checkpoint'e yazılırken üretilir. Tick başına ayırma 8 → 1,9; frame'siz adım 0
  ayırma (`TestRuntimeStepBetweenFramesDoesNotAllocate`).
- **`runtimeOverloaded` eşiği.** En kötü adım 80 µs, eşik 250 ms; hesap eşiğe yaklaşamaz.
  Entegrasyon testinin ilk koşularından birinde kurtarmadan ~1 sn sonra bir yarış bu sebeple
  iptal oldu (aynı dizüstünde 2 instance + çocuk süreç + Mongo + Redis); sonraki koşularda
  tekrar üretilemedi. Bu bir süreç duraklamasıdır; eşik House Rockets ve House Tanks'le aynı
  kaldı, üretimde `runtime_overloaded` metriğiyle izlenir.
- **Belgeler.** ADR-031 (geri sarma, kırpılmış tick'te geçerlilik, yarış sonu penceresi, eşik);
  `multiplayerBaseline.md` §11; istemci devir notu `fausSwitchClientIntegration.md` (kararlar,
  netleşen davranışlar, fixture sha256'sı, ölçümler, istemciden istenenler, görev metni).

## 7. Riskler

| Risk | Önlem |
|---|---|
| 1 ulp sapma | §5 kuralları (FMA dahil); Swift iz özetleri (48 senaryo, bit düzeyinde); kamera tablosu eşitlik testi; simülasyon fixture'ında tam eşitlik. |
| Parkur aktarım hatası | Betikle üretim + istemcinin parkur dökümüyle tam karşılaştırma. |
| Geri sarma maliyeti | Değer tipleri, paylaşılan tablolar, birleştirme, 0 ayırma hedefi, benchmark. |
| Çekirdek değişikliği mevcut oyunları etkiler | İsteğe bağlı arayüz; `lobbyState` boşken yazılmaz; mevcut testler değişmeden geçer. |
| Simülasyon fixture'ı gecikir | F1'de test hazır ve atlanıyor; istek hemen gönderilir. |

## 8. Rolling deploy ve geri alma

- Yeni Mongo alanı `lobbyState` eklemeli ve `omitempty`. Eski sürüm `fausSwitch` modülünü
  bilmediği için bu oturumlara sahip olamaz, bu yüzden `ReplaceOne` ile alanı silme riski yok.
- 0061 deploy'da oyunu açar. Yayın sırasında eski bir örneğe düşen `PUT .../session` bilinmeyen
  oyun hatası alır, istemci yeniden dener (FauseTanks'te kabul edilen durum). İstemci henüz
  yayında olmadığı için kullanıcı etkisi yok.
- Geri almada eski sürüm `fausSwitch` oturumlarını sahiplenemez. Açık oturumlar mevcut
  kurtarma/iptal yoluyla `recoveryFailed`/`cancelled` sonuçlanır. `lobbyState` alanı eski
  sürümce yok sayılır.

## 9. İstemciden istenenler

Devir notuna girer; ilk ikisi şimdiden iletilmeli.

1. **`fausSwitchSimulation.json`** (Swift motorundan): 2, 4 ve 8 kişilik (ölçek 0,75)
   senaryolar. Backend kendi Swift iz karşılaştırmasını yaptığı için artık engelleyici değil;
   istemcinin kendi testlerinde aynı dosyayı kullanması için yine isteniyor. Biçim
   (`JSONEncoder` varsayılanı, `Double`'lar kısaltmasız):
   `{"courseVersion": 7, "scenarios": [{"name", "runnerCount", "flips": [{"tick", "seat"}],
   "checkpoints": [{"tick", "runners": [{"x", "y", "velocityY", "gravity", "status",
   "finishedAtTick", "eliminatedAtTick", "eliminationReason"}]}]}]}`. Opsiyoneller `null`
   yazılır. Anlam: senaryo `FausSwitchStartGrid.lanes(count:runnerScale:)` ile başlar, her
   tick'te o tick'in flip'leri liste sırasıyla `FausSwitchPhysics.flip(_:index:course:)` ile
   uygulanır, sonra `step`. Tick `T`'deki checkpoint, `T`'ye ulaştıran adımdan sonraki ve
   `T`'nin flip'lerinden önceki durumdur. Flip'ler tick sırasıyla verilir.
2. **Parkur dökümü (isteğe bağlı):** iz karşılaştırması 1–8 kişilik yarış parkurlarını dolaylı
   olarak doğruladığı için artık engelleyici değil. `FausSwitchCourse.race(runnerCount:)` için `n = 1…8` katı (kimlik, tür,
   kutu), portal, boşluk, boost ve yavaşlatma listeleri JSON olarak.
3. **Lobide renkler (K1):** `sessionSnapshot.lobbyState.colors` (`playerId → color`) okunup
   lobide gösterilsin. Alan boşken yazılmaz. Fixture'ın `sessionSnapshot` örneğine bir
   `lobbyState` örneği eklensin. Ayrılan oyuncunun kaydı bir sonraki seçime kadar alanda
   kalabilir; istemci yalnız katılmış oyuncuların rengini çizmeli. Başkasının rengine talep
   `accepted` döner ama durum değişmez; istemci kendi seçimini `lobbyState`'ten okumalı.
   Geri sayım başlayınca seçim `fausSwitch.error.not_in_lobby` ile reddedilir.
4. **Yarış sonu (K2):** son robot elendikten sonra sonuç snapshot'ı ve `fausSwitch.result`
   250 ms sonra gelir. İstemci bu arada "bekleniyor" durumunu göstermeli ve son robotun
   elenmesini kesin sonuç saymamalı.
5. **Katalog (K3):** oyun deploy'la tüm evlerde açık gelir.
6. **Sonuç doğrulaması düzeltmesi:** `FausSwitchResultDTO.validate` (`completed` dalı)
   `winnerId == null` için "birden fazla 1. sıra" yerine "1. sıradaki tek oyuncu bitirmemiş ya
   da birden fazla 1. sıra var" kuralını kullanmalı. Örnek: süre dolduğunda en ilerideki robot
   tek başına 1. sıradadır ama bitirmediği için `winnerId` `null`dır.
7. **Çalışmayan Swift testi:** `FausSwitchPhysicsTests.testPortalNeverDropsARobotBehindTheTrail`
   iç fonksiyon olarak `testPortalIsJustBigEnoughForARobot`'un içinde kaldığı için hiç çalışmıyor.
   Dışarı alınırsa `runner.x < before` kontrolü başarısız olur (iz robotu öne çektiğinde iniş
   noktası giriş noktasının önünde). Işınlanmayı yer çekimi değişimiyle algılamak yeterli.
8. **Bilgi:** referans motorda iki robot, sıkışma ya da portal inişinde bir tick boyunca iç içe
   olabiliyor (F1 notu). Sunucu bunu birebir uygular; çizimde kısa bir üst üste binme normaldir.
9. **Bilgi (F4–F5):** yinelenen `inputSequence` hatasız kabul edilir ve uygulanmaz; reddedilen
   flip (robot o tick'te zaten durmuş) `fausSwitch.error.runner_finished` alır ama yine
   `lastProcessedInputSequence`'a sayılır. Elenen robotun oyuncusu yarış boyunca kontrolü tutar
   (`controlGeneration` dolu kalır); yarıştan çıkan oyuncunun tutmaz. Sunucu `startedAt: null`
   olan bir sonuç hiç göndermez (fixture'daki `cancelledResult` yalnız istemci şekli olarak kalır).
