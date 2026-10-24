# FauseTanks online — backend brifingi

Tarih: 24 Ekim 2026 · Hazırlayan: mobil (istemci) tarafı · Hedef: `houseflowApi` backend agent'ı

Durum: **Onaylandı, backend geliştirmesine hazır (24 Ekim 2026).** §5'teki tur kuralları,
§4.1'deki sabit arena boyutu ve kalabalık maç haritası kesinleşti. Kalabalık maç haritası
(Bodrum) demoda çalışıyor.

Bu belge FauseTanks'in (kod adı `houseTanks`) online çok oyunculu modu için backend'de
yapılması gerekenleri anlatır. İstemci tarafı ayrıca geliştirilecek; iki tarafın uyması
gereken kurallar ve sözleşme burada.

## 1. Özet

- Mevcut ortak çok oyunculu çekirdek (oturum, lobi, hazır penceresi, geri sayım, gateway,
  Redis koordinasyonu, kalıcılık, kurtarma) **olduğu gibi kullanılır**. Çekirdekte büyük
  değişiklik beklenmiyor.
- Yapılacak asıl iş: House Rockets'e benzer yeni bir **tick oyun modülü**:
  `internal/application/game/games/houseTanks`. Modül sunucu otoriterdir: fizik, isabet,
  elenme, kasa, joker, tur ve kazanan kararlarını sunucu verir.
- Oyun motorunun referans uygulaması Swift'te, `FausCrewCore` içinde hazır
  (`houseflowApp/FausCrew/shared/Sources/FausCrewCore/Games/HouseTanks/`; en önemli
  dosyalar `HouseTanksWorld.swift`, `HouseTanksBotMatch.swift`, `HouseTanksMapCatalog.swift`,
  `HouseTanksPowerUpRules.swift`). Go modülü bu motoru port eder. Eşitlik ortak test
  dosyalarıyla (fixture) doğrulanır; House Rockets'teki `houseRocketsSimulation.json`
  yaklaşımının aynısı.
- Yeni oyun anahtarı: `houseTanks`. House Rockets gibi bir Game dokümanı ve katalog kaydı
  olur; ev bazında açılıp kapanabilir ve yayına hazır olana kadar kapalı başlar.

## 2. Mevcut altyapının karşıladıkları

| İhtiyaç | Durum |
|---|---|
| En az 2 oyuncu | Var: `MinimumPlayers = 2` |
| 2 kişi hazır olunca 30 sn pencere, süre sonunda hâlâ ≥2 hazırsa başlat | Var: `readyWindow` (30 sn) → `countdown` → `running`. Hazır sayısı 2'nin altına düşerse pencere iptal olur. |
| En fazla ev üye sayısı kadar oyuncu, en çok 8 | Var: `MaximumPlayers = 8`. Yalnız ev üyeleri katılabildiği için üye sayısı doğal üst sınır. |
| Bot yok | Online modda bot yok, ekstra iş yok. |
| WebSocket zarfı, kontrol yetkisi (controlGranted), sıra numaraları, yeniden bağlanma, sahip çökmesi, kayıt noktası | Var (House Rockets ile aynı mekanizma). |
| Kalıcı maç sonucu | Var: `ResultCodec` ile modül kendi sonuç şemasını tanımlar. |

## 3. Modülde yeni olanlar (House Rockets'ten farklar)

1. **Turlu maç:** Maç, bir oyuncu 3 tur kazanana kadar süren turlardan oluşur. Oturum
   `running` iken modül kendi tur akışını yönetir (bkz. §5). Çekirdekteki oturum durumları
   değişmez.
2. **Turlar arası hazır olma oyun içinden:** Tur bitince sıradaki tur otomatik başlamaz.
   Oyuncular oyun ekranındaki panelden "hazırım" der. Bunun için modüle özel bir intent
   gerekir (`houseTanks.roundReady`). Lobiye geri dönülmez.
3. **Bağlantısı kopan oyuncu elenmez:** House Rockets'teki gibi süre dolunca elenme
   (`connectionExpired`) **yok**. Bağlantısı kopan, ayrılan ya da üyeliği kaldırılan
   oyuncunun tankı arenada **hareketsiz hedef** olarak kalır (ayrıntılar §5.4). Diğer
   oyuncular onu vurup öldürebilir. Oyuncu maç içinde geri bağlanırsa kontrolü bir sonraki
   tick'te geri alır.
4. **Arena tek ve sabit boyutlu:** Tüm istemciler aynı mantıksal arenayı paylaşır (§4.1).
   İstemci bunu ekrana sığdırarak çizer.
5. **Harita rastgele, oyuncu sayısına göre:** Maç başında sunucu, maçın oyuncu sayısını
   destekleyen haritalar arasından birini ve bir rastgelelik tohumunu (seed) seçer; harita
   maçın bütün turlarında aynı kalır. 2–5 oyunculu maçlar standart haritalardan, 6–8
   oyunculu maçlar kalabalık maç haritalarından seçilir (§4.1).

## 4. Oyun kuralları (Swift referans motoruyla birebir)

Koordinatlar: arena noktası, orijin sol alt, y yukarı. Açılar radyan, +x'ten saat yönünün
tersine. Zaman: 120 Hz sabit fizik adımı (`1/120 s`); tick tam sayı sayaçtır.

### 4.1. Arena

- Mantıksal arena: **1180 × 640** (onaylandı). Her istemci bunu ekranına sığdırarak çizer.
- Oynanabilir çerçeve (duvar): her kenardan `max(16, 640 × 0.035) = 22.4` içeride.
- Haritalar normalize (0…1) tanımlı: `HouseTanksMapCatalog.swift`. Konum:
  `(x × 1180, y × 640)`. Engel boyutu (`k = 0.36 × scale`):
  - `divider`, `sofa`, `bookshelf`: genişlik `w × 1180 × k`, yükseklik `h × 640 × k`.
  - `roundTable`, `pillar`, `planter`, `crate`: iki kenar da `h × 640 × k` (daire ya da kare).
- Engel türleri: `divider`, `sofa`, `bookshelf` (döndürülmüş dikdörtgen), `roundTable`,
  `pillar`, `planter` (daire), `crate` (kare, itilebilir, tek isabette kırılır).
- **Harita ölçeği ve oyuncu aralığı (onaylandı):** Her harita bir `scale` değeri ve
  desteklediği oyuncu aralığı (`minPlayers…maxPlayers`) taşır.
  - Standart haritalar (Salon, Koridorlar, Depo, Avlu): `scale = 1`, 2–5 oyuncu. Online için
    her birine 5. başlangıç noktası eklenecek.
  - Kalabalık maç haritası (Bodrum, `basement`): `scale = 0.72`, **6–8 oyuncu**, 8 başlangıç
    noktası, daha sık engel. 6 ve üzeri oyunculu maçlar daha az kalabalık hissettirsin diye
    tank, mermi, roket, joker ve engel boyutları bu ölçekle küçülür. Hızlar ve süreler
    değişmez. İleride başka kalabalık maç haritaları da bu şekilde eklenebilir.
  - `scale` ile küçülen değerler: tank yarıçapı (9) ve gövde uzunluğu (20), mermi ve roket
    yarıçapı ile uzunluğu, namlu çıkış mesafeleri, joker toplama mesafesi (25), engel boyutu
    ve başlangıç noktası açıklık kontrolü (28). Duvar çerçevesi, joker doğma alanı ve doğma
    açıklıkları ölçekten etkilenmez.
  - Oyuncu sayısı 2–5 ise standart haritalardan, 6–8 ise kalabalık maç haritalarından
    (şimdilik yalnız Bodrum) rastgele seçilir.
- **Başlangıç noktaları:** Her oyuncunun maç boyunca sabit bir koltuk sırası olur (katılım
  sırası). Her tur başında tanklar sırayla haritanın başlangıç noktalarına yerleşir,
  arenanın merkezine bakar; çift sıradakiler saat yönünün tersine, tek sıradakiler saat
  yönünde döner. Bağlantısı kopan oyuncu koltuğunu korur. Katalog (noktalar dahil) fixture
  ile paylaşılır; Go tarafı haritaları elle yazmaz, fixture'dan alır.
- **Tank renkleri:** 8 oyuncu için 8 renk: `plum`, `orange`, `blue`, `olive`, `mustard`,
  `crimson`, `sky`, `ivory`. Online'da sunucu katılım sırasına göre atar.

### 4.2. Tank

| Değer | Sayı |
|---|---|
| Çarpışma | daire, yarıçap 9 |
| Gövde uzunluğu (namlu ucu hesabı için) | 20 |
| Boşta dönüş | 1.5 rad/s, yön `rotationDirection` (±1) |
| Basınca | açı kilitlenir, bir kez ateş eder, namlu yönünde sürer. Ateş aralığı dolmamışsa ateş etmez ama yine sürer. |
| Bırakınca | durur (hız 0), dönüş yönü tersine çevrilir |
| Sürüş kuvveti / kütle | 360 / 1.25 → ivme 288 pt/s² |
| Sönümleme | `v *= 1 / (1 + dt × 1.8)` her adımda |
| Azami hız | 245 |
| Zırh | 5 |
| Normal ateş aralığı | 0.58 s |
| Vurulma sonrası koruma | 0.36 s (dev roket bunu yok sayar) |
| Elenen tank | yerinde enkaz olarak kalır, sabit engel gibi davranır, mermi durdurur |

### 4.3. Mermiler

- Normal mermi: hız 510, yarıçap 1.8, ömür 2.8 s, namlu çıkışı tank merkezinden 15.
- Dev roket: hız 400, yarıçap 4, uzunluk 22, ömür 3.2 s, namlu çıkışı 25; isabet ettiği
  tankı zırhı ne olursa olsun öldürür (zırh = 0).
- İsabet testi her adımda segment (eski → yeni konum) ile yapılır; ilk temas kazanır:
  duvar, sahibi hariç tanklar (canlı ya da enkaz), kırılmamış engeller.
- Namlu ağzı bir engele ya da tanka değiyorsa mermi oluşturulmaz, isabet anında uygulanır.
- Kasa: mermi değince kırılır. Kalkan: tanka gelen mermiyi hasarsız durdurur; dev roketi
  de durdurur.

### 4.4. Jokerler

- Türler: `rocket`, `rapidFire`, `spreadShot`, `shield`, `aim`.
- Sıra: karıştırılmış torba. Her torbada beş tür birer kez gelir; yeni torba önceki torbanın
  son türüyle başlamaz. Torba turlar arasında devam eder.
- Aynı anda yerde en çok bir joker. Doğma aralığı 7–11 s (tekdüze), yerde kalma 12 s,
  etki süresi 8 s. Yeni joker eskisinin yerini alır.
- Doğma alanı: `x 48…(w−48)`, `y 44…(h−80)`. Geçerlilik: başlangıç noktalarına en az
  `max(80, alanYüksekliği × 0.22)`, canlı tanklara en az 50, engel sınırlarına en az 26.
  80 deneme; olmazsa 1 s sonra yeniden denenir.
- Toplama: tank merkezi ile joker merkezi arası < 25 (16 + 9).
- Etkiler:
  - `rapidFire`: basılı tutarken 0.16 s aralıkla ateş.
  - `spreadShot`: her atışta 5 mermi, açı farkları −0.28, −0.14, 0, 0.14, 0.28.
  - `shield`: süre boyunca hasar almaz.
  - `aim`: istemcide namludan duvara kırmızı nişan çizgisi; ateş edince tank, o atışın
    mermileri bir yere çarpana ya da süreleri dolana kadar dönmez. Kilitliyken normal
    aralıkta yeniden ateş edebilir.
  - `rocket`: atışlar dev roket olur.
- Joker ve etki süreleri yalnız tur oynanırken (`roundPlaying`) ilerler. İlk joker her tur
  başından 7–11 s sonra doğar.

### 4.5. Çarpışma çözümü (referans motordaki sıra)

Her adımda: jokerler → tank hareketi (sürüş, dönüş, sönüm, hız sınırı, konum) → kasa
sönümü ve konumu → 3 kez temas çözümü → joker toplama → mermiler.

- Tank–duvar: konumu sınırla, normal hızı `−0.36` ile sektir.
- Tank–sabit engel / enkaz: dışarı it, normal hızı `0.2` sekmeyle yansıt.
- Tank–tank: örtüşmeyi ikiye böl, `0.36` sekmeli itki.
- Tank–kasa: örtüşmeyi ters kütleyle böl (kasa kütlesi 0.45, sönüm 4), `0.05` sekme.
  Kasalar dönmez, yalnızca kayar.
- Kasa–duvar/engel/kasa/enkaz: kasa dışarı itilir (kasa–kasa yarı yarıya).

### 4.6. Rastgelelik

Swift motoru tek bir **SplitMix64** üreteci (`HouseTanksRandom.swift`) kullanır; joker
türü, konumu ve doğma aralığı bundan çekilir. Online'da jokerleri sunucu belirleyip
anlık görüntüde gönderdiği için **istemciyle aynı joker dizisini üretmek gerekmez**. Swift'in
karıştırma algoritmasını birebir kopyalamaya çalışmayın; Go tarafının tutması gereken
şey kurallardır: torba kuralı (§4.4), doğma alanı ve açıklıklar, aralıklar. Sunucu yine de
maç başına bir tohum seçip `welcome` ile bildirir (kayıt ve hata ayıklama için).

## 5. Oturum ve maç akışı

1. **Lobi (çekirdek, değişiklik yok):** Ev üyeleri odaya katılır. 2 kişi hazır olunca
   30 sn pencere başlar. Süre sonunda hâlâ ≥2 hazır varsa 3 sn geri sayım, ardından
   `running`. Maçın oyuncuları geri sayım başlarken dondurulur (mevcut davranış).
2. **Maç başı:** Sunucu haritayı ve tohumu seçer, `welcome` / ilk anlık görüntüde gönderir.
3. **Tur akışı (modül içi aşamalar):** `roundCountdown` (3 sn) → `roundPlaying` →
   `roundReady` (ya da `matchEnded`).
   - Tur, canlı tek tank kalınca biter (aynı anda hepsi ölürse berabere, kimse puan almaz).
     Süre sınırı yok. Hareketsiz hedefler de canlı sayılır: tur, onlar da vurulmadan bitmez.
   - Tur bitince `roundEnded` olayı (kazanan bilgisiyle) yayınlanır. 3 tura ulaşan varsa
     `matchEnded` ve sonuç kaydedilir; yoksa hemen `roundReady` aşamasına geçilir.
   - `roundReady` kuralı (**onaylandı**). Hazır bilgisi her tur için sıfırdan toplanır:
     - Maçtaki bağlı oyuncuların hepsi hazırsa hemen `roundCountdown`.
     - Değilse ≥2 hazır olunca 30 sn pencere başlar. Pencere sürerken bağlı herkes hazır
       olursa hemen başlar; hazır sayısı 2'nin altına düşerse pencere iptal olur (lobideki
       davranışın aynısı).
     - Pencere bitince bağlı olan herkes (hazır demeyenler dahil) oynar; bağlı olmayanların
       tankı hareketsiz hedeftir.
   - Her yeni turda kasalar yenilenir, tanklar başlangıç noktalarına döner, zırhlar 5'e
     döner, mermiler ve jokerler temizlenir; joker torbası devam eder.
   - **Bağlı oyuncu <2 (onaylandı):** Maçın herhangi bir anında bağlı oyuncu sayısı 2'nin
     altına düşerse maç `insufficientPlayers` ile biter; o ana kadarki tur skorları sonuçta
     yer alır. Kısa ağ kopmaları maçı bitirmesin diye bu durum mevcut yeniden bağlanma süresi
     (10 sn) boyunca sürerse uygulanır; bu sürede biri geri bağlanırsa maç devam eder.
4. **Bağlantı kopması ve hareketsiz hedef:**
   - "Bağlı" = oyuncunun geçerli bir kontrol bağlantısı var ve heartbeat süresi (mevcut
     ayar: 2 sn ping, 6 sn kontrol zaman aşımı) dolmamış.
   - Kontrol kaybolduğu anda (soket kapanması, zaman aşımı, açık ayrılma ya da üyeliğin
     kaldırılması) tank hareketsiz hedef olur: basılı tutma bırakılır, hız sıfırlanır,
     dönmez, ilerlemez, ateş etmez, nişan kilidi temizlenir. Eldeki joker süresi işlemeye
     devam eder.
   - Tank canlı bir gövde olarak kalır: zırhı vardır, vurulabilir, diğer tanklar ve kasalar
     çarpınca itilebilir, üzerine itilirse jokeri toplayabilir. Ölürse diğer tanklar gibi
     enkaza dönüşür.
   - Süre dolunca elenme yok. Maç içinde geri bağlanan oyuncu kontrolü bir sonraki tick'te
     geri alır.
   - Maç başladıktan sonra yeni oyuncu katılamaz; oyuncular geri sayımda dondurulur
     (mevcut davranış).
5. **Rövanş:** Maç bitince mevcut rövanş akışı (yeni oturum) kullanılır. İstemci bunu oyun
   ekranındaki panelden yönetir; lobi ekranına dönülmez.

## 6. Sözleşme: `houseTanks.v1`

Zarf ve ortak mesajlar House Rockets v2 ile aynı (`protocolVersion`, `messageId`, `type`,
`payload`, `gameSession.*`, `realtime.ping/pong`). Oyuna özel mesajlar:

### İstemciden

- `houseTanks.press`
  `{ controlGeneration, inputSequence, pressed: Bool, clientTick: Int64, heading: Double }`
  Bas / bırak. Sıra numarası kesin artan. Kontrolü olmayan (hareketsiz hedef) ya da elenmiş
  oyuncunun girdisi reddedilir.
- `houseTanks.roundReady` `{ ready: Bool }` — yalnız `roundReady` aşamasında kabul edilir.
- `houseTanks.resync` `{}`

### Sunucudan

- `realtime.welcome` ayarları: `mapId`, `mapVersion`, `seed`, `arenaWidth`, `arenaHeight`,
  `physicsRateHz`, `snapshotRateHz`, girdi limitleri, `roundWinsNeeded`, `armorPerRound`.
- `houseTanks.controlGranted` (House Rockets'teki gibi).
- `houseTanks.snapshot`:
  - `runtimeEpoch`, `stateSequence`, `tick`, `phase` (çekirdek aşaması),
    `roundPhase` (`roundCountdown | roundPlaying | roundReady | matchEnded`),
    `roundNumber`, `roundCountdownEndsAt`, `roundReadyWindowEndsAt`.
  - `players`: `playerId`, `displayName`, `color`, `armor`, `roundsWon`, `isAlive`,
    `connected`, `roundReady`, `powerUp`, `powerUpRemaining`, `lastProcessedInputSequence`.
  - `tanks`: `playerId`, `x`, `y`, `vx`, `vy`, `rotation`, `rotationDirection`,
    `isDriving`, `aimLocks`, `controllable`.
  - `crates`: `index`, `x`, `y`, `destroyed`.
  - `token`: `kind`, `x`, `y`, `expiresAtTick` veya null.
  - `projectiles`: canlı mermiler (`id`, `ownerId`, `x`, `y`, `vx`, `vy`, `rocket`,
    `spawnTick`). İstemci düz çizgiyi kendisi çizer.
  - `events` (son anlık görüntüden beri, tick damgalı): `fired`, `hit`, `shielded`,
    `crateBroken`, `explosion`, `eliminated`, `jokerCollected`, `roundEnded`.
  - `roundWinnerId`, `championId`.
- `houseTanks.result`: kalıcı sonuç (§7).

### Gecikme telafisi

Tank sürekli döndüğü için bas komutu sunucuya geç ulaşınca namlu başka yöne bakmış olur
(1.5 rad/s'de 50 ms ≈ 4°). Bu yüzden:

- `press` istemcinin gördüğü `heading` ve `clientTick` değerini taşır.
- Sunucu, o tankın son ~150 ms içindeki gerçek açı aralığıyla karşılaştırıp tutarlıysa
  açıyı istemcinin değerine çeker ve atışı o açıyla yapar. Tutarsızsa sunucu açısını
  kullanır.
- 150 ms'den eski girdiler sunucu açısıyla uygulanır (ya da `inputExpired`).

### Frekanslar (başlangıç değerleri, ölçümle ayarlanır)

Fizik 120 Hz, çizelgeleme 60 Hz, anlık görüntü 20 Hz (mermiler hızlı olduğu için 30 Hz de
denenebilir), girdi en çok 20 mesaj/sn. Bas/bırak ayrık olduğu için yalnızca değişimde
gönderilir.

## 7. Kalıcı sonuç

`sessionId`, `houseId`, `gameKey = houseTanks`, `protocolVersion`, `mapId`, `mapVersion`,
`status` (`completed` / `cancelled`), `endReason` (`championDecided`,
`insufficientPlayers`, `cancelledByUser`, `sessionExpired`, `recoveryFailed`, …),
`championId`, `roundsPlayed`, `players[]` (`playerId`, `roundsWon`, `finalRank`),
`startedAt`, `endedAt`.

## 8. Test ve eşitlik beklentileri

- **Eşitlik fixture'ı:** İstemci tarafı Swift motorundan `houseTanksSimulation.json`
  üretecek: sabitler, harita katalogu (ölçek, oyuncu aralığı ve başlangıç noktaları dahil)
  ve rastgelelik içermeyen adım adım fizik senaryoları (bas/bırak, sürüş, dönüş, kasa itme,
  çarpışmalar, mermi ve roket isabeti, kalkan, nişan kilidi). Go simülasyonu bunları küçük
  bir toleransla (`1e-6` önerisi) geçmeli. Joker doğma ve torba için dizi eşitliği değil,
  kural testleri beklenir (§4.6).
- **Bağımlılık:** Fixture'lar istemcinin Faz 0 işleriyle (sabit arena, tick zamanı, standart
  haritalara 5. başlangıç noktası) birlikte gelecek. O zamana kadar Go tarafı bu belgedeki
  sabitlerle ve Swift kaynak koduyla ilerleyebilir.
- **Protokol fixture'ı:** `houseTanksProtocol.json` (House Rockets'teki gibi iki depoda
  aynı dosya).
- House Rockets paketlerindeki test türleri: simülasyon, runtime, gateway, sonuç, kurtarma,
  sahip çökmesi, yük.

## 9. Backend agent'ın karar vereceği noktalar

1. Tur aşamaları çekirdeğin `module.Phase` değerlerine eklenmeli mi, yoksa
   `PhasePlaying` altında modül alanı (`roundPhase`) olarak mı taşınmalı? Kontrol yetkisi
   kontrolleri buna göre ayarlanmalı.
2. Anlık görüntü frekansı (20 / 30 Hz) ve kayıt noktası sıklığı.
3. Gecikme telafisi penceresi (150 ms) ve tolerans değeri.

## 10. İstemci tarafının yapacakları

- Motoru sabit arenaya, tam sayı tick'e ve kaydet/yükle desteğine geçirmek; standart
  haritalara 5. başlangıç noktasını eklemek (kalabalık maç haritası 8 noktayla geliyor).
- Eşitlik ve protokol fixture'larını üretmek.
- `FausCrewCore` içinde FauseTanks online motoru: lobi ve bağlantı (House Rockets'in genel
  kısmı yeniden kullanılarak), ara kare, kendi tankı için tahmin, mermilerin yerelde
  çizilmesi, tur hazır paneli.
- iOS ve Android host katmanları ve online oda ekranının gerçek oturuma bağlanması.
