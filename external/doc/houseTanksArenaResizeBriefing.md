# FauseTanks online — arena boyutu değişikliği (backend brifingi)

Tarih: 25 Ekim 2026 · Hazırlayan: mobil (istemci) tarafı · Hedef: `houseflowApi` backend agent'ı

Durum: **Ürün sahibi onayladı (25 Ekim 2026).** Bu belge, onaylanmış
`houseTanksBackendBriefing.md` §4.1'deki "mantıksal arena 1180 × 640" kararını değiştirir.
Diğer her kural (fizik sabitleri, hızlar, süreler, tur kuralları, wire şekli) aynı kalır.

## 1. Neden

İstemci F0'ı (motor eşitliği) bitirdi: 17 senaryonun hepsi 1e-6 içinde geçiyor. Sabit arena
gerçek cihazda denendi. 1180 × 640'lık arena telefona sığdırılınca tank, mermi ve jokerler
eski demodakinin yaklaşık %61'ine (Bodrum'da %44'üne) küçülüyor ve oynanabilirlik bozuluyor.

Eski demoda arena telefon ekranının kendisiydi (yaklaşık 852 × 393 nokta). Tank yarıçapı
9, hızlar bugünküyle aynıydı ve ürün sahibi o hissi onayladı. Arenayı telefon boyutuna
çekmek, fizikte hiçbir sabite dokunmadan o hissi geri getirir. En-boy oranı telefonlara
(≈2,17–2,22) yakın olduğu için istemcide ekran da neredeyse tamamen dolar.

## 2. Değişiklik

| | Şimdi | Yeni |
|---|---|---|
| `ArenaWidth` × `ArenaHeight` | 1180 × 640 | **872 × 400** |
| Duvar çerçevesi `max(16, H × 0.035)` | 22,4 | 16 |
| Joker alanı `{48, 44, W − 96, H − 124}` | 1084 × 516 | 776 × 276 |
| Başlangıç mesafesi `max(80, alan.H × 0.22)` | 113,5 | 80 |

- Formüller aynı kalır; yalnız iki sabit değişir. Engel boyutları normalize tanımlı
  olduğundan haritalar arenayla birlikte orantılı küçülür (eski demoda olduğu gibi).
- Tank/mermi/joker boyutları, hızlar, kütleler ve tüm tick süreleri **değişmez**.
- `settings.arenaWidth/Height` (welcome) yeni değerleri taşır; wire şekli aynı.

## 3. Harita kataloğu: tek düzeltme

Go'nun `ValidationIssues` kurallarını 872 × 400 için bir betikle çalıştırdık. Katalogda
tek bir sorun çıkıyor:

- **courtyard, başlangıç 4** `(0.50, 0.08)`, saksının `(0.50, 0.16)` hemen altında.
  400 yükseklikte boşluk 24,8 kalıyor, kural 28 istiyor.
- **Öneri:** başlangıç 4'ü `(0.40, 0.08)` yap. Bu nokta 1180 × 640, 872 × 400, 880 × 400 ve
  852 × 393'te sıfır sorunla geçiyor. `courtyard` sürümünü 3 → 4 yükselt.

Diğer dört harita (Bodrum dahil) 872 × 400'te kurallardan olduğu gibi geçiyor.

## 4. Sürümler

Online henüz yayında olmadığı için eski sürümle uyum gerekmez. Yine de karışmayı önlemek
için önerimiz:

- `ContractRevision`: `houseTanks.v1` → `houseTanks.v2`
- `MapCatalogVersion`: 1 → 2
- `ProtocolVersion`: 2 kalır (mesaj şekli değişmiyor).

## 5. Fixture'lar

Üç fixture'ı yeniden üret (`-update-house-tanks`):

- **`houseTanksMaps.json`:** yeni arena değerleri, courtyard düzeltmesi, yeni sürümler.
- **`houseTanksSimulation.json`:** sabitlerde `arenaWidth/Height` değişir. Spawn'lar ve engeller
  normalize olduğundan 17 senaryonun hepsinin çıktısı değişir. Elle verilen konumlar ise mutlak
  olduğu için kontrol edilip gerekirse **yeniden yerleştirilmeli**:
  - Arenanın dışında kalanlar: `wallBounce` (x = 1000); `headOn`, `rocketKill`,
    `shieldBlocks`, `spreadProtection`, `stationaryTarget`, `wreckBlocks` (y = 580–583);
    `jokerExpiry` jetonu (900, 580).
  - Kasaya göre yerleştirilenler: `cratePush`, `shellBreaksCrate`, `muzzleBlocked`
    (y = 256). Kasa artık başka bir yerde olacağı için yeni kasa konumuna göre ayarlanmalı.
  - İçeride kalanlar: `driveAndRelease`, `aimLock`, `rapidFire`, `jokerPickup` (300, 60).
    Yol üstünde yeni bir engel çıkmadığı kontrol edilmeli.

  Konumlar yine seçilen haritanın açık zemininde olmalı ve senaryonun anlattığı durumu
  (duvar, kasa, enkaz, aynı tick isabet vb.) korumalı. Senaryo seti ve adları aynı kalsın.
- **`houseTanksProtocol.json`:** settings içindeki arena değerleri ve sürümler.

`TestHouseTanksSimulationMatchesBriefing` içindeki konuma bağlı beklentiler de yeni
yerleşime göre güncellenir.

## 6. İstemcinin yapacağı (backend bittikten sonra)

- Yeni fixture'ları bayt bayt kopyalar, sha256 değerlerini günceller.
- Kataloğu betikle yeniden üretir, `arenaSize`'ı 872 × 400 yapar.
- Eşitlik testini (17 senaryo, 1e-6) yeniden çalıştırır, fark çıkarsa somut farkla bildirir.
- İki platformda arenayı yine ekrana sığdırır (iOS'taki siyah şeritler istemcide zaten
  giderildi).

## 7. Kabul

- Go testleri ve fixture'lar 872 × 400 ile yeşil.
- Katalog doğrulaması her harita için sıfır sorun veriyor.
- Değişen yalnız arena boyutu, courtyard başlangıç 4 ve sürüm alanları; diğer sabitler
  ve senaryo seti aynı.
