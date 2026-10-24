# FauseTanks online — backend geliştirme planı

Tarih: 24 Ekim 2026 · Kaynak: `houseTanksBackendBriefing.md` (24 Ekim, onaylı) · Hedef paket:
`internal/application/game/games/houseTanks`

Durum: **T0–T7 kod ve belge işleri tamamlandı (24 Ekim 2026); açık olan yalnız T7'nin
canlı kabulü (deploy + iki istemcili tam maç). 0054 oyunu deploy'la tüm evlerde
açar.** Swift referans motoru
(`houseflowApp/FausCrew/shared/Sources/FausCrewCore/Games/HouseTanks/`) okundu; istemcinin
Faz 0 işleri (sabit arena, tam sayı tick, standart haritalara 5. başlangıç noktası) henüz
yok: motor hâlâ `resize(to:)` ve değişken `step(by:)` kullanıyor, standart haritalarda 4
başlangıç noktası var, Bodrum (`basement`) mevcut.

### Sıralama kararı (24 Ekim 2026)

**Backend geliştirmelerinin tamamı (T0–T6) bittikten sonra istemci agent'ı başlar.**
Briefing'deki "istemci fixture üretir, Go uyar" yönü bu yüzden tersine döner:

- **Referans Go'dur.** Go motoru Swift kaynak kodundan port edilir; eşitlik ve protokol
  fixture'larını (`houseTanksSimulation.json`, `houseTanksProtocol.json`) **backend
  üretir**. İstemci Faz 0'da Swift motorunu bu fixture'lara uydurur; bulunan farklar
  ya Swift'te düzeltilir ya da (Swift davranışı briefing'e daha uygunsa) backend'e
  hata olarak döner.
- **Harita kataloğunun sahibi backend'dir.** `fixtures/houseTanksMaps.json` Swift
  `HouseTanksMapCatalog.swift`'ten aktarılır; standart haritaların 5. başlangıç noktasını
  backend seçer ve bu haritaların sürümü 3'e çıkar. İstemci kataloğu bu dosyadan alır.
- **§3'teki sözleşme sapmaları backend kararıdır**, istemci onayı beklenmez; sözleşmeye
  ve fixture'a öyle girer.
- **§7'deki açık sorularda önerilen varsayılanlar uygulanır** (maç üst sınırı kullanıcı
  kararıyla **5 dk**, 2 dk
  hazırsız `roundReady` sınırı, ilk turda da `roundCountdown`). İkisi de sabit olduğu
  için değiştirmek sözleşme fixture'ında tek satırlık iştir.
- Son paket **T7 istemci devri** olur: House Rockets planının §0'ı gibi, istemci
  agent'ına doğrudan verilecek entegrasyon belgesi.

## 1. Özet

- Çekirdekte (oturum, lobi, gateway, oda sahipliği, checkpoint, kurtarma) **değişiklik
  gerekmiyor.** Tur aşamaları modül alanı olarak taşındığında (§2.1) gateway'in
  `PhasePlaying` ve `FrameHeader.Alive` kontrolleri olduğu gibi çalışıyor.
- Çekirdek dışına dokunan yerler yalnızca: `games/builtin.go` kaydı, sonuç hata
  anahtarları için bir yerelleştirme migration'ı ve testler.
- Asıl iş yeni tick modülü: fizik portu, joker kuralları, tur akışı, runtime (kontrol,
  girdi kuyruğu, gecikme telafisi), sonuç kodeki ve House Rockets'teki test aileleri.
- Briefing'den üç sözleşme sapması var (§3); backend kararı olarak sözleşmeye girer.

## 2. Briefing §9 kararları

### 2.1. Tur aşamaları: çekirdek fazı değil, modül alanı

**Karar:** `module.Phase` değişmez. Oturum `running` iken çerçeve fazı her zaman
`playing` (sonra `finalizing` / `ended` / `cancelled`) olur; `roundPhase`
(`roundCountdown | roundPlaying | roundReady | matchEnded`) modülün kendi alanıdır.

Gerekçe:

- Gateway heartbeat'i, yeniden bind etmeyi ve `ControlledBy` kontrolünü
  `PhasePlaying`'e bağlıyor (`gatewayV2.go:472`, `:491`, `:590`). Yeni bir çekirdek fazı
  bu üçünü de değiştirmeyi gerektirirdi.
- Yayın sırasında (rolling deploy) çerçeveyi sahip örnek üretir ama tüm örneklerin
  gateway'i okur. Eski sürüm bilinmeyen bir fazı görünce heartbeat'i keser ve kontroller
  zaman aşımına düşer. Modül alanı bu riski hiç doğurmuyor.

`FrameHeader.World.Players[].IsAlive` anlamı tanklarda **"maçta kontrol hakkı var"**
olur, "tankı bu turda canlı" değil:

| Oyuncu durumu | Başlıkta `isAlive` | Snapshot'ta `isAlive` (tank) | Sonuç |
|---|---|---|---|
| Bağlı, tankı canlı | true | true | Normal |
| Bağlı, tankı bu turda öldü | **true** | false | Kontrolü korur; `roundReady` gönderebilir, heartbeat akar |
| Bağlantısı koptu (oturumda hâlâ `playing`) | true | tur durumuna göre | Gateway yeniden bind edebilir; geri bağlanınca bir sonraki tick'te kontrol |
| Açıkça ayrıldı / üyeliği kaldırıldı | **false** | tur durumuna göre | Bind edilmez; tankı kalıcı hareketsiz hedef |

Kontrol yetkisi kontrolleri runtime içinde:

- `houseTanks.press`: yalnız `roundPlaying` ve tank canlıyken uygulanır. Ölü tankın
  girdisi `player_eliminated` ile reddedilir (istemci bunu maçtan elenme değil "bu turda
  öldün" olarak yorumlar). `roundCountdown` / `roundReady` sırasında gelen bas/bırak,
  tur sınırındaki yarışlar hata gürültüsü üretmesin diye **kabul edilip yok sayılır**
  (sıra numarası onaylanır).
- `houseTanks.roundReady`: yalnız `roundReady` aşamasında; ölü tank sahibi dahil kontrolü
  olan her oyuncu gönderebilir. Başka aşamada `invalid_input`.

Bu tasarım çekirdeğin hata eşleştirmesine (`tickProtocolError`) yeni kod eklemeyi de
gerektirmiyor.

### 2.2. Snapshot frekansı ve checkpoint sıklığı

- **Snapshot: 20 Hz** (operatör ayarı `snapshotRateHz`, 5…60). Mermiler istemcide
  `spawnTick` + hızdan çizildiği için 20 Hz yetiyor. 30 Hz yük testinde ölçülür; Game
  dokümanından ayar değişikliğiyle (deploy olmadan) denenebilir.
- **Checkpoint:** çekirdeğin mevcut politikası: kritik anahtar değişince hemen, değilse
  en geç 1 sn'de bir (`defaultCheckpointInterval`). Tanklar için kritik anahtar şunları
  içerir: kontrol devri (bağlantı + nesil), `roundPhase`, `roundNumber`, oyuncu başına
  `roundsWon`, tank canlılığı, `roundReady` oyları, maçta kontrol hakkı, sonuç önerisi.
  Konum, hız, mermi ve joker zamanlayıcıları kritik değildir.
- Snapshot boyutu tahmini: 8 tank, ~20 kasa, en kötü ~60 mermi → ham JSON 5–7 KB,
  permessage-deflate ile ~1–2 KB. 8 izleyici × 20 Hz için sorun değil; yük testinde
  ölçülüp baseline belgesine yazılır.

### 2.3. Gecikme telafisi

- **Pencere: 150 ms = 18 tick.** Runtime her tank için son 18 tick'in açısını ve
  `isDriving` bilgisini halka tamponda tutar (checkpoint'e girmez; kurtarmadan sonra
  boştur ve sunucu açısı kullanılır).
- `press(pressed: true, clientTick, heading)` geldiğinde:
  1. `serverTick − clientTick > 18` ya da `clientTick > serverTick + 2` ise sunucu açısı.
  2. Pencerede tank sürülmüş ise (açısı kilitliydi) sunucu açısı.
  3. Aksi halde tank `[clientTick, serverTick]` aralığında tek yönde döndüğü için taranan
     yay basit bir açı aralığıdır. `heading` bu yayın **±0.035 rad (~2°)** genişletilmiş
     hâlinin içindeyse açı `heading`'e çekilir ve atış o açıyla yapılır.
- Boştaki tank hareket etmediği için (bırakınca hız 0) **konumu geri sarmaya gerek yok**;
  yalnızca açı düzeltilir. İtilen hareketsiz tank istisnası ölçülebilir hata vermiyor.
- 150 ms'den eski girdiler sunucu açısıyla uygulanır, reddedilmez. `InputLifetime`
  (sunucu girişinden itibaren) House Rockets'teki gibi 1 sn kalır.
- Pencere ve tolerans operatör ayarıdır (`lagCompensationMilliseconds`,
  `headingToleranceMilliradians`); varsayılanlar ölçümle güncellenir.

## 3. Briefing'den sözleşme sapmaları (backend kararı)

1. **`mapId` / `mapVersion` / `seed` welcome'da değil snapshot'ta.** `module.Game.Welcome`
   yalnızca oturum kimliği ve ayarları görür; bağlantı lobide açıldığında harita henüz
   seçilmemiştir. Öneri: tohum `sessionId`'den türetilir (FNV-64a → SplitMix64), harita
   donmuş oyuncu sayısı + tohumla seçilir. Böylece geri sayım önizleme çerçevesi
   haritayı zaten gösterir, kurtarmada yeniden üretilebilir ve rastgelelik deposu
   gerekmez. Welcome yalnız sabit değerleri taşır: `mapCatalogVersion` ve `settings`
   içinde `arenaWidth`, `arenaHeight`, `physicsRateHz`, `snapshotRateHz`, girdi
   limitleri, `roundWinsNeeded`, `armorPerRound`, tur süreleri. Snapshot `mapId`,
   `mapVersion` ve `seed` (16 haneli hex metin; JSON sayısı 2^53'ü aşar) taşır.
2. **`houseTanks.roundReady` payload'ına `controlGeneration` eklensin:**
   `{ controlGeneration, ready }`. Diğer tüm oyun girdileri bunu taşıyor; eski bir
   bağlantı bindinden kalan oy yeni kontrol sahibinin yerine sayılmasın.
3. **Olaylar kaybolmasın diye kümülatif pencere.** Runtime'ın çerçeve kanalı tek
   elemanlı ve yavaş tüketicide eski çerçeveyi düşürüyor (`framesCoalesced`). "Son
   snapshot'tan beri olanlar" düşen çerçeveyle kaybolur. Öneri: her olay artan
   `eventSequence` taşır, her snapshot son ~1 sn'lik olayları içerir, istemci
   `eventSequence` ile tekilleştirir. `roundEnded` ayrıca durumdan da okunabilir
   (`roundPhase`, `roundWinnerId`).

Ayrıca bilgi olarak: zarf `protocolVersion` = 2 kalır (çekirdek v2 zarfı;
sözleşme testi bunu zorunlu tutuyor). `houseTanks.v1` sözleşme revizyonudur
(`ContractRevision`), House Rockets'teki `houseRockets.v2.1` gibi.

T0'da netleşen küçük wire kararları:

- Joker süresi saniye yerine **tick** ile verilir: oyuncuda `powerUpExpiresAtTick`,
  yerdeki jokerde `expiresAtTick`. İstemci kalan süreyi snapshot `tick`'inden hesaplar.
- Oyuncuda `seat` (koltuk sırası, renk ve başlangıç noktası buna bağlı) ve
  `hasLeft` (açıkça ayrıldı / üyeliği kaldırıldı; geri dönemez) alanları var.
  `connected = false, hasLeft = false` geri bağlanabilir demektir.
- `championId` House Rockets'teki `winnerId` gibi yalnız sonuç kaydedildikten sonra
  (`phase = ended`) dolar; `roundWinnerId` tur bitince hemen dolar.
- İptal edilen sonuçta `roundsWon` korunur, `finalRank` null'dır; maç hiç başlamadıysa
  `mapId` / `mapVersion` null'dır.
- `roundPhase` yalnız lobi geri sayımı önizlemesinde null'dır; `recovering` çerçevesi
  kurtarılan tur aşamasını taşır.
- Olay alanları türe göre isteğe bağlıdır (`omitempty`); hangi türün hangi alanı
  taşıdığı `HouseTanksEventModel` yorumunda ve fixture'da.
- Ayarlar: `maximumPressMessagesPerSecond` varsayılan 30 (House Rockets'teki
  `maximumSteerMessagesPerSecond` gibi sunucu sınırı), istemcinin hedefi
  `maximumInputRateHz` = 20.

## 4. Modül tasarımı

### 4.1. Paket yapısı

House Rockets'in dosya düzeni izlenir:

| Dosya | İçerik |
|---|---|
| `definition.go` | Anahtar, sürümler, sabitler (§4 briefing), renkler, faz/sebep enum'ları, `Definition()` |
| `settings.go` | Operatör ayarları, sınırlar, `resolveSettings`, `clientModel` |
| `maps.go` + `fixtures/houseTanksMaps.json` | `go:embed` ile harita kataloğu; Go haritaları elle yazmaz. Dosya Swift fixture'ının `maps` bölümüyle bayt bayt aynıdır (test kontrol eder) |
| `geometry.go` | Vektör, döndürülmüş kutu, daire temas, segment–daire / segment–kutu kesişimi |
| `world.go` | Fizik dünyası: tanklar, kasalar, engeller, enkaz, mermiler, temas çözümü (Swift `HouseTanksWorld` portu, sabit 1/120 adım) |
| `powerUps.go` | Torba, doğma alanı/açıklıklar, etki süreleri, nişan kilidi |
| `match.go` | Tur akışı durum makinesi (§4.3), tick bazlı son tarihler |
| `random.go` | SplitMix64 (yalnız sunucu; Swift dizisiyle eşitlik hedeflenmez) |
| `simulationState.go` | Checkpoint'e yazılan serileştirilebilir dünya + maç durumu |
| `runtime.go` | Kontrol/bind/heartbeat/varlık, sıralı girdi kuyruğu, gecikme telafisi, çerçeve yayını |
| `runtimeCheckpoint.go` | Checkpoint şeması, `CriticalKey`, restore |
| `models.go`, `protocol.go`, `wireSnapshot.go` | Wire DTO'ları, mesaj tipleri, snapshot projeksiyonu |
| `module.go` | `module.TickModule` adaptörü, `frameView` (paylaşılan baytlara `controlGeneration` ekleme hilesi aynen) |
| `result*.go`, `recoveryResult.go` | `ResultCodec`, doğrulama, iptal sonucu, sıralama |

### 4.2. Simülasyon ve tick modeli

- Fizik 120 Hz, çizelgeleyici 60 Hz; duvar saatinden tick sayısı House Rockets'teki
  `remainder` yöntemiyle çıkarılır. Tick sayacı oturum `running` olduğu sürece hiç
  durmadan ilerler; `roundReady` / `roundCountdown` sırasında yalnız dünya adımı atlanır.
- Tüm süreler (ateş aralığı, koruma, joker, tur geri sayımı, hazır penceresi) **tick
  cinsinden** tutulur; wire'daki `roundCountdownEndsAt` / `roundReadyWindowEndsAt`
  `startedAt + tick/120` ile hesaplanır. Böylece checkpoint/restore deterministik kalır.
- Adım sırası Swift'teki gibi: jokerler → tank hareketi → kasa sönüm/konum → 3× temas
  çözümü → joker toplama → mermiler.
- `scale` (Bodrum 0.72) yalnız briefing §4.1'deki ölçülere uygulanır; duvar çerçevesi,
  joker doğma alanı ve doğma açıklıkları ölçeklenmez.
- Hareketsiz hedef: `controllable = false` → basılı tutma bırakılır, hız 0, dönmez,
  ateş etmez, nişan kilidi temizlenir; fizik gövdesi olarak kalır (itilir, vurulur,
  joker toplayabilir, joker süresi işler).

### 4.3. Tur akışı (`match.go`)

```
running ─► roundCountdown (3 sn) ─► roundPlaying ─► roundEnded olayı
                ▲                                       │
                │            3 tura ulaşan var mı? ─evet─► matchEnded → sonuç önerisi
                │                                       │hayır
                └──── roundReady (oy toplama) ◄─────────┘
```

- Tur sonu: canlı (hareketsiz dahil) tek tank kalınca kazanan +1; aynı adımda hepsi
  ölürse berabere, puan yok, tur yine `roundsPlayed`'e sayılır.
- `roundReady` kuralı briefing §5.3'teki gibi: bağlı herkes hazırsa hemen; değilse ≥2
  hazırla 30 sn pencere; pencerede bağlı herkes hazır olursa hemen; hazır <2 olursa
  pencere iptal; pencere bitince bağlı herkes oynar.
- Yeni tur: kasalar yenilenir, tanklar koltuk sırasına göre başlangıç noktalarına
  (merkeze bakarak, çift sıra CCW / tek sıra CW), zırh 5, mermi ve joker temiz, torba
  devam eder, ilk joker 7–11 sn sonra.
- **Yetersiz oyuncu:** bağlı oyuncu <2 olduğu an `insufficientSince` başlar;
  `reconnectGrace` (10 sn) dolarsa maç `insufficientPlayers` ile biter, skorlar sonuca
  yazılır. Bu sürede bağlı sayı ≥2'ye dönerse sayaç sıfırlanır. Maç başında henüz bind
  etmemiş oyuncular House Rockets'teki gibi `presenceDeadline` boyunca "bekleniyor"
  sayılır; aksi halde ilk saniyelerde maç yanlışlıkla biterdi.
- Briefing'de olmayan güvenlik sınırı önerisi (§7 soru 1): toplam maç süresi üst sınırı.

### 4.4. Runtime farkları (House Rockets'e göre)

- **Elenme yok:** `connectionExpired`, `forfeit`, `membershipRevoked` elemesi yerine
  `Reconcile` ayrılan/üyeliği kaldırılan oyuncuyu kalıcı olarak "kontrol hakkı yok"
  yapar; zaman aşımı yalnız kontrolü düşürür.
- **Sıralı girdi kuyruğu:** House Rockets aynı çerçevedeki girdilerden sonuncusunu
  tutuyor (yön için doğru). Bas/bırak ayrık olduğu için bu bir dokunuşu (aynı 16 ms'de
  bas + bırak) yutar. Oyuncu başına küçük, sıralı bir kuyruk tutulur (en çok 8); bas en
  az bir tick uygulanır, sonra bırak.
- `pressed` ardışık aynı değerse no-op (idempotent), sıra numarası yine onaylanır.
- Hız limiti: `maximumPressMessagesPerSecond` (varsayılan 20, intent hız limitine eşit).
- Kurtarma: House Rockets gibi restore edilen runtime kontroller yeniden bağlanana kadar
  `recovering` çerçevesi yayınlar; `roundPlaying` içinde kurtarılırsa dünya en son
  checkpoint'ten (≤1 sn eski) devam eder.

## 5. Teslim paketleri

Her paket bir önceki paketin üstüne commit olarak birikir; paket başına deploy yok.
Tamamı tek bir yayınla çıkar (§6).

### T0 — Sözleşme iskeleti

- `definition.go`, `settings.go`, `models.go`, `protocol.go`, `input.go`; `DecodeIntent`
  (`press`, `roundReady`, katı alan kontrolü); `Welcome`, `ClientSettings`,
  `ResolveConfig`.
- `module.go`: `module.TickModule`'ün tamamını karşılar; runtime ve sonuç metotları
  T4/T5'e kadar `ErrNotImplemented` döner.
- **`builtin.go` kaydı T4'e kalır.** `tests/gameModuleContracts_test.go` kayıtlı her tick
  modülünde runtime'ı çalıştırıyor; yarım modül kaydedilirse kırılır. Kayıt yokken
  modül katalogda da görünmez.
- `fixtures/houseTanksProtocol.json` (backend üretir; istemci T7'de alır) ve
  `tests/houseTanksContract_test.go`.
- **Bitti ölçütü:** fixture'daki her istemci mesajı çözülür, her geçersiz mesaj
  beklenen kodla reddedilir, her sunucu mesajı DTO'larla bayt eşdeğer gidip gelir;
  ayar sınırları ve fuzz testi.
- **Durum: tamamlandı.** `tests/houseTanksContract_test.go` (7 test + fuzz) ve tüm
  `go test ./...` geçiyor. Fixture'daki snapshot/sonuç örnekleri elle tutarlı
  yazılmış örneklerdir; T4'te runtime'ın ürettiği çerçevelerle de karşılaştırılır,
  `seat` / koltuk sırası, olay penceresi ve sonuç sıralaması kuralları testte zorunlu.

### T1 — Harita kataloğu, geometri, fizik

- `maps.go` + gömülü `fixtures/houseTanksMaps.json` (Swift kataloğundan aktarılır,
  standart haritalara 5. başlangıç noktası eklenir, sürüm 3); Swift'teki
  `validationIssues` kuralları Go testinde. Oyuncu sayısına göre harita seçimi
  (2–5 standart, 6–8 Bodrum), `sessionId`'den tohum, başlangıç noktası yerleşimi.
- `world.go`: tank sürüş/dönüş/sönüm/hız sınırı, temas çözümü, kasa itme, mermi/roket
  segment isabeti, namlu engelde ise anında isabet, kalkan, enkaz.
- `houseTanksSimulation.json` **Go'dan üretilir**: `go test -run ... -update` bayrağıyla
  yazılan, rastgelelik içermeyen adım adım senaryolar (bas/bırak, sürüş, dönüş, kasa
  itme, çarpışmalar, mermi/roket isabeti, kalkan, nişan kilidi). Normal test bu dosyayı
  `1e-6` toleransla karşılaştırır, böylece fizik değişikliği fark edilmeden geçmez.
- **Bitti ölçütü:** Swift kaynağına satır satır karşılık gelen port; senaryolar
  briefing §4 sabitleriyle elle doğrulanan beklentileri (ör. ivme 288, azami hız 245,
  ateş aralığı 70 tick) karşılıyor.
- **Durum: tamamlandı.** `geometry.go`, `random.go` (SplitMix64 + `sessionId`'nin
  FNV-64a'sı, telde 16 haneli hex), `maps.go` (gömülü katalog, doğrulama, `SelectMap`),
  `world.go` (Swift `HouseTanksWorld` portu; süreler tam tick: ateş 70, hızlı ateş 20,
  koruma 44, mermi 336, roket 384, joker etkisi 960, yerde 1440). Standart haritaların 5.
  noktaları: livingRoom (0.50, 0.12), corridors (0.41, 0.66), warehouse (0.40, 0.50),
  courtyard (0.50, 0.08); Swift kurallarına ek olarak koltuklar arası en az 2× açıklık.
  `fixtures/houseTanksSimulation.json` 17 senaryo içeriyor (rölanti dönüş, sür/bırak,
  duvar, kafa kafaya, kasa itme/kırma, namlu engelde, roket öldürme, kalkan, yayılım
  koruması, nişan kilidi, hızlı ateş, hareketsiz hedef, enkaz, joker toplama/süre bitimi,
  8 kişilik Bodrum). `go test ./tests -run TestHouseTanksSimulationFixture
  -update-house-tanks` ile yeniden üretilir; normal koşuda `1e-6` toleransla karşılaştırılır.
  `TestHouseTanksSimulationMatchesBriefing` fixture'ı motordan bağımsız olarak briefing
  sayılarıyla doğrular; `TestHouseTanksMapCatalog` katalog, harita seçimi ve tohum
  belirlenimini test eder. Joker doğurma/torba T2'de, tur akışı T3'te `world.go`'ya bağlanır.

### T2 — Jokerler

- Torba (her torbada 5 tür birer kez, yeni torba öncekinin son türüyle başlamaz),
  doğma aralığı/alanı/açıklıklar, 80 deneme + 1 sn yeniden deneme, 12 sn yerde kalma,
  8 sn etki, toplama mesafesi, beş etki (nişan kilidi dahil).
- **Bitti ölçütü:** kural testleri (dizi eşitliği değil); fizik fixture'ındaki kalkan
  ve nişan kilidi senaryoları.
- **Durum: tamamlandı.** `jokers.go`: `JokerBag` (Fisher–Yates karıştırma; yeni torba
  önceki torbanın son türüyle başlarsa ilk eleman rastgele bir başkasıyla yer değiştirir;
  checkpoint için `Valid`), `JokerSchedule` (maçın üreteci, torba ve `nextTokenTick`),
  `JokerArea` (x 48…1132, y 44…560, ölçeksiz), `ValidJokerSpot` (başlangıç noktalarına
  ≥ 113.52, canlı tanklara ≥ 50, kasaların güncel konumu ve enkaz dahil siperlere ≥ 26).
  `World.StartJokers` ilk jokeri 840–1320 tick sonraya kurar; `Step` süre bitiminden hemen
  sonra, tanklardan önce vadesi gelen jokeri dağıtır: 80 deneme, olmazsa 120 tick sonra
  yeniden dener. Çekiliş sırası: her deneme için x, sonra y; başarıda tür, sonra aralık.
  - **Swift'ten bilinçli sapma:** Swift yerde joker varken yenisini doğurmuyor; briefing
    §4.4'teki "yeni joker eskisinin yerini alır" kuralı uygulandı. Böylece vade
    toplanıp toplanmamasından bağımsız ve 12 sn'lik ömür yalnız yeniden denemelerde
    devreye giriyor.
  - `world.Jokers == nil` iken joker dağıtılmaz; simülasyon fixture'ı bu yüzden
    değişmedi (yalnız kurallar metnine not eklendi).
  - Testler `tests/houseTanksJokers_test.go`: torba kuralı (400 tohum × 12 torba, art
    arda aynı tür yok, her tür maç açabiliyor, JSON'dan dönen torba aynı dağıtıyor),
    nokta kuralı sınır değerleri, her haritada vade/ömür/geçerli nokta/aralık, aynı
    oturum ve checkpoint'ten dönüşte aynı dizi, dolu arenada yeniden deneme, toplama.
  - T3/T4'e not: yeni turun dünyası önceki dünyanın `Jokers.Random` ve `Jokers.Bag`
    değerleriyle `StartJokers` çağırır. `World.definition` JSON'a yazılmadığından
    restore `MapID` + `MapVersion` ile kataloğdan yeniden bağlar.

### T3 — Tur akışı

- `match.go` saf durum makinesi; runtime'dan bağımsız birim testleri: tur sonu,
  berabere, 3 tur, `roundReady` pencere kuralları, hareketsiz hedeflerin canlı sayılması,
  `insufficientPlayers` sayacı ve sıfırlanması, yeni tur sıfırlamaları.
- **Durum: tamamlandı.** `match.go`: `Match` saf durum makinesi, `Advance()` bir fizik
  tick'i ilerletir. Tick oturum boyunca durmaz; dünya yalnız `roundPlaying`'de adım atar.
  - Runtime'ın kullanacağı API: `SetControl` (bind/kopma → hareketsiz hedef, oy düşer),
    `Leave` (kalıcı), `SetReady`, `AwaitPlayers` (kurtarma sonrası varlık süresi),
    `Cancel`, `Playing`, `DecodeMatch` (checkpoint JSON'undan harita kataloğuna yeniden
    bağlanarak geri yükleme).
  - Tur sırası: `roundCountdown` 360 tick boyunca dünya donuk ama tick'i maçla eşit;
    `roundPlaying` başında `StartJokers`; tur sonunda dünya `Freeze` (mermi, hareket,
    joker ve etkiler temizlenir), üreteç ve torba maça geri alınır; yeni tur yeni dünya.
  - Oy kuralı: bağlı (kontrollü) herkes hazır ve ≥2 → hemen; ≥2 hazır → 3600 tick
    pencere; <2'ye düşerse pencere kapanır; pencere bitince bağlı herkes oynar; pencere
    açılmadan 14400 tick geçerse `cancelled` / `sessionExpired`.
  - Varlık: kontrollü oyuncular ve başlangıç (ya da kurtarma) süresi içinde herkes
    "var" sayılır (`ControlTimeout + ReconnectGrace` = 1920 tick). Var <2 olduğunda
    1200 tick dolarsa `insufficientPlayers`. Toplam 36000 tick'te `sessionExpired`.
  - Olay penceresi: dünya olayları ve `roundEnded` (tur no + kazanan) artan `sequence`
    ile son 120 tick boyunca maçta tutulur; T4 snapshot'a aynen yansıtır.
  - **Briefing'e göre iki karar:** (1) Son tanklar aynı tick'te yok olursa berabere
    (Swift isabetleri tek tek işleyip ilkini kazandırıyordu; briefing §5.3 uygulandı).
    (2) Ayrılmamış oyuncu sayısı 2'nin altına inerse 10 sn beklemeden biter; ayrılan
    geri dönemediği için beklemenin anlamı yok.
  - Testler `tests/houseTanksMatch_test.go` (10 test): başlangıç ve harita, geri sayım,
    galibiyet/berabere/hareketsiz hedef, yeni tur sıfırlaması ve torbanın devamı, oy
    kuralları, 3 tur, varlık/yetersiz oyuncu/ayrılma, 5 dk sınırı ve iptal, olay penceresi,
    checkpoint'ten aynen devam ve bozuk checkpoint'lerin reddi.
  - T4'e not: `World.Press` olayı bir sonraki `Advance`'te kaydedilir; checkpoint
    `Advance`'ten sonra alınmalı (bekleyen olaylar JSON'a girmez).

### T4 — Runtime ve modül adaptörü

- `runtime.go`, `runtimeCheckpoint.go`, `module.go`, `wireSnapshot.go`: kontrol devri,
  sıralı kuyruk, gecikme telafisi, olay penceresi, `frameView`, `PreviewFrame`
  (haritayı geri sayımda gösterir), `CriticalKey`, restore + `checkpointMatches`.
- **Bitti ölçütü:** genel tick modülü sözleşme testi geçer; runtime testleri
  (`houseRocketsRuntime_test.go` muadili): bind/yeniden bind, zaman aşımı → hareketsiz
  hedef → geri bağlanma, dokunuşun yutulmaması, gecikme telafisi kabul/ret sınırları,
  ölü tankın `roundReady` göndermesi, ayrılan oyuncunun bind edilememesi.
- **Durum: tamamlandı.** `runtime.go` (bağlama/devir komut kanalından, oyuncu başına 8'lik
  sıralı kuyruk, tick başına oyuncu başına tek basış — dokunuşun basışı bu tick, bırakışı
  sonraki tick uygulanır; `roundPlaying` dışındaki basışlar ACK'lenip yok sayılır; ölü
  tankın basışı `player_eliminated`, oyu engellemez), gecikme telafisi
  (`compensatedHeading`: pencere `lagCompensationMs`, gelecekteki tick en fazla +2, pencerede
  sürmüş/nişanı kilitli tank reddedilir, aday açı süpürülen yay ± tolerans içinde olmalı),
  `runtimeCheckpoint.go` (`CriticalKey`: bağlama, ayrılma, galibiyet, tank ölümü, oy, tur
  fazı/penceresi ve maç sonu; hareket/atış/joker/ACK kritik değil; restore tüm tankları
  hareketsiz hedef yapar, `AwaitPlayers` ile varlık süresi tanır, tick harcamaz),
  `wireSnapshot.go`, `module.go` (House Rockets ile aynı `frameView`/`tickRuntime` yapısı;
  `PreviewFrame` haritayı `roundPhase: null`, `roundNumber: 0` ile gösterir).
  `tests/houseTanksRuntime_test.go` 7 test: kontrol (devir, eski generation reddi, zaman
  aşımı → hareketsiz hedef → yeniden bind, disconnect, ayrılan oyuncu bind edemez, epoch/
  createdAt/bağlantı reddi), girdiler (dokunuş, eski sıra, kuyruk ve hız sınırı, ölü tank
  oyu), gecikme telafisi kabul/ret (yay üstü, yay dışı, tolerans, eski, gelecek), maçtan
  sonuca (şampiyon commit'e kadar gizli, sahte sonuçlar reddedilir), yetersiz oyuncu,
  checkpoint/restore (kurcalanmış checkpoint reddi), çerçeve görünümü (fixture anahtarları,
  izleyiciye generation yok, ölü tankın sahibine oy için generation var). `-race` temiz.
  - T5'ten öne alınanlar: `result.go` (sonuç üretimi, `ValidateResult`, encode/decode,
    `RecoveryCancellationResult`) genel sözleşme testi için T4'te yazıldı. T5'te kalan:
    0053 lokalizasyon migration'ı ve ayrı sonuç testleri (eşit sıralar 1, 2, 2, 4 dahil).
  - Fixture değişikliği: `houseTanksProtocol.json`'daki `recovering` örneğinde tüm
    oyuncular `connected: false`, tüm tanklar `controllable: false` (restore davranışı).
  - `Runtime.WithMatch(func(*Match))` test/operasyon kancası kilit altında çalışır ve
    çerçeveyi kirli işaretler.
  - `builtin.go` modülü kaydeder; Game dokümanı olmadığı için katalogda kapalı kalır
    (sözleşme testi bunu doğrular). Sürüm tüm instance'lara çıkmadan doküman açılmaz.

### T5 — Kalıcı sonuç

- `HouseTanksResultModel` (briefing §7). `finalRank`: `roundsWon` azalan, eşitlerde aynı
  sıra (1, 2, 2, 4). `championId` yalnız `championDecided`'da dolu.
- `FinalizeResult` doğrulaması: oyuncular donmuş kadroyla aynı, `roundsWon` ≤ 3, şampiyon
  tam 3 turla tek, `roundsPlayed` ≥ en yüksek `roundsWon`.
- `CancellationResult`: kadrodan, skor 0, şampiyon yok.
- Migration `0053_houseTanksLocalizations`: `houseTanks.error.result_not_found`,
  `result_conflict`, `invalid_result`, `finalizing`, `runtime_completion_required`
  (en/tr), 0042'deki gibi `$setOnInsert`.
- ~~Game dokümanı seed edilmez~~ → 25 Ekim kararı: migration `0054_houseTanksConfig`
  dokümanı tüm evlere açık seed eder (§6).
- **Durum: tamamlandı.** Sonuç kodu (`result.go`) T4'te yazılmıştı; T5'te kurallar
  karşılaştırıldı, kod değişikliği gerekmedi. `roundsPlayed` için toplam galibiyet ≤
  `roundsPlayed` kontrolü, plandaki "en yüksek `roundsWon`" kuralından daha sıkı.
  `0053_houseTanksLocalizations` (5 anahtar, en/tr, `$setOnInsert`) `registry.go`'ya
  eklendi. Gerçek zamanlı hata kodları (`invalid_input`, `stale_control` vb.) House Rockets'ta
  olduğu gibi sunucuda çevrilmez; istemci çevirir. `tests/houseTanksResult_test.go`:
  - eşit sıralar 1, 2, 2, 4: 4 oyuncu, turlar a, b, beraberlik, c, a, a; anahtarlar
    fixture'daki `result` örneğiyle aynı;
  - iptal edilen maç skorları korur (`cancelledByUser`, `insufficientPlayers`), sıra ve
    şampiyon yok;
  - 30 bozulma senaryosu reddedilir (kimlik, süre, harita, kadro, galibiyet, şampiyon,
    sıra, durum/sebep);
  - `CancellationResult` donmuş kadrodan: başlamadan ayrılan ve hazır olmayan oyuncu yok,
    maç sırasında ayrılan oyuncu var; skor 0, harita yeniden hesaplanır;
  - `FinalizeResult` başlangıcı oturuma, bitişi `UpdatedAt`'e sabitler; payload ile
    uyuşmayan satırlar ve başka kadro reddedilir;
  - migration kayıtlı ve iki kez çalıştırılabilir (Mongo entegrasyon testi, yerelde geçti).
  - T7'ye not: fixture'daki `cancelledBeforeStartResult` örneği (`startedAt`/`mapId`
    null) House Rockets'taki gibi yalnız şema örneği; tick oyunlarında sonuç yalnız
    başlamış oturum için yazılır, sunucu bu şekli şu an üretmez.

### T6 — Entegrasyon, kurtarma ve yük

House Rockets test ailelerinin muadilleri:

- gateway (iki örnek arası girdi yönlendirme, gerçek WebSocket),
- sonuç (transaction, HTTP/socket, rövanş = yeni oturum),
- kurtarma ve sahip çökmesi (tur ortası ve `roundReady` ortası),
- yük: 8 oyunculu oda, rapidFire + spreadShot altında snapshot boyutu ve adım süresi;
  20 ve 30 Hz karşılaştırması `multiplayerBaseline.md`'ye.
- ADR: "Tur aşamaları modül alanı, çekirdek fazı değil" ve "Tanklarda bağlantı kopması
  elenme değil" (ADR-028/029).
- **Durum: tamamlandı.** `tests/houseTanksIntegration_test.go` (gerçek Mongo replica set +
  Redis, iki instance):
  - gateway: HTTP ile oturum, iki instance arası keşif, lobi geri sayımında harita
    önizlemesi, grant'ler, `roundCountdown` → `roundPlaying`, uzak instance'tan basış ve ACK
    (ateş olayı, sürüş, gizli generation), eski sıra `stale_control`, erken oy
    `invalid_input`, resync; bir oyuncu ayrılınca `insufficientPlayers` ile iptal, sonuç
    socket'te ve iki instance'ın HTTP'sinde byte byte aynı, dışarıdan 403, rövanş yeni
    oturum;
  - kurtarma: oy penceresinde kaybedilen sahibin checkpoint'i iki instance'tan devralınır;
    skor korunur, tankı ölen oyuncu dahil herkes yeni grant alır, oylar düşer ve yeniden
    verilen oylar 2. turu başlatır;
  - sahip çökmesi: alt süreç tur ortasında öldürülür, aynı socket'ler turu olduğu yerden
    (≤ 60 tick) geri alır; tanklar hareketsiz, eski generation reddedilir, yeni basış onaylanır;
  - sonuç: 4 eşzamanlı tamamlama tek kanonik sonuç yazar, slot boşalır, rövanşı silmez,
    farklı sonuç reddedilir.
  - `tests/houseTanksLoad_test.go`: 8 oyuncu, rapidFire + spreadShot, 20/30 Hz;
    `multiplayerBaseline.md` §10. Bulgu: bağlantı başına frame ağır atışta ~14 KB
    (House Rockets 3,8 KB), %57'si mermiler; sıkıştırmayla ~3,8 KB. Adım süresi
    bütçenin çok altında (en kötü 0,42 ms).
  - ADR-028 ve ADR-029 `architecture-decisions.md`'ye eklendi.
  - Altyapı değişikliği: gateway test fixture'ı oyun tanımını parametre alır
    (`newGameGatewayFixture`); `houseTanks.NewRuntimeWithSettings` yük ölçümü için eklendi.
    Sahip çökmesi testi House Rockets'ın oyundan bağımsız alt süreç yardımcısını kullanır.

### T7 — İstemci devri

- `houseTanksClientIntegration.md`: istemci agent'ına doğrudan verilecek belge
  (House Rockets planının §0'ı gibi): sözleşme, fixture'lar ve nasıl kullanılacakları,
  Swift motorunun Go fixture'ına uydurulması (Faz 0), mobil fazlar ve çıkış ölçütleri,
  staging'de açık test evi.
- Fixture'ların istemci deposuna kopyalanacak son hâli ve sha256 değerleri.
- **Bitti ölçütü:** staging'de oyun bir test evi için açık; iki gerçek WebSocket
  istemcisiyle (test aracı) tam maç oynanabiliyor.
- **Durum (24 Ekim 2026):** `houseTanksClientIntegration.md` yazıldı; iddiaları kodla
  karşılaştırıldı (wire alanları, hata kodu eşlemesi, tur oyu, gecikme telafisi, süre
  sabitleri, renk sırası). Fixture'lar istemciye bu deponun
  `internal/application/game/games/houseTanks/fixtures/` klasöründen olduğu gibi verilir.
  Ayrı bir kopya tutulmaz; sha256 değerleri belgenin §2'sinde. Wire biçimi değişmedi:
  kayan nokta yuvarlaması yapılmadı, bant genişliği için istemci permessage-deflate teklif
  eder. **Açık:** canlı kabul (iki gerçek WebSocket istemcisiyle tam maç) deploy sonrası
  yapılır. 25 Ekim kararı: istemciler henüz yayınlanmadığı için test evi kısıtı konmadı;
  oyun 0054 ile deploy'la birlikte tüm evlerde açılır (§6).

## 6. Yayın ve geri alma

- Yeni modül yalnız ekleme: Mongo/Redis formatlarında mevcut belgelere değişiklik yok.
- **Sıralama (25 Ekim kararı):** migration `0054_houseTanksConfig` Game dokümanını tüm
  evlere açık seed eder (`rollout.houseIds` boş, kural ve ayarlar kod varsayılanı,
  `$setOnInsert`). Doküman yayın sırasında oluştuğu için, o arada açılan bir oturumun odası
  eski örneğe düşerse başlatılamaz. İstemciler henüz yayınlanmadığı için bu risk kabul edildi.
  Eski sürüm tanımadığı oyunun dokümanını yok sayar. Ev kısıtı ileride kademeli açılış için
  config API'sinde kalır.
- **Geri alma:** geri almadan önce `houseTanks` Game dokümanı kapatılır; çalışan maçlar
  eski sürümde kurtarılamaz ve `recoveryFailed` ile iptal edilir.
- Deploy, yalnız istemci online moduna hazır olduğunda ya da başka gerekli bir yayına
  binerek çıkar.

## 7. Açık sorular

Sıralama kararıyla (bkz. baştaki not) 1 ve 2'de öneriler varsayılan olarak uygulanıyor;
3 backend kararı oldu. Kullanıcı farklı karar verirse sabit ve fixture güncellenir.

1. **Maç süresi üst sınırı:** Tur süresi sınırı yok; bağlı iki oyuncu hiç
   karşılaşmazsa ya da `roundReady`'de hiç kimse hazır demezse maç sonsuza kadar sürer
   (runtime, checkpoint ve oda kiralaması açık kalır). **Karar (kullanıcı, 24 Ekim):
   5 dk toplam üst sınır** → `cancelled` / `sessionExpired`, o ana kadarki skorlar
   yazılır, şampiyon yok; ve `roundReady`'de 2 dk boyunca hiç pencere açılmazsa aynı
   sonuç (2 dk önerisi uygulanıyor).
2. **İlk turun geri sayımı:** lobi geri sayımı (3 sn) ardından ilk tur `roundCountdown`
   (3 sn) iki kat bekleme yaratıyor. Öneri: ilk turda da `roundCountdown` kalsın; bu süre
   kontrol bind'i için tampon (bind etmeyen tank hareketsiz hedef olurdu). İstemci iki
   geri sayımı tek görsel akış olarak gösterebilir.
3. ~~§3'teki üç sözleşme sapmasının istemci tarafından onayı.~~ Backend kararı.
