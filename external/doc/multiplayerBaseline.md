# Multiplayer geliştirme planı — Faz 0 baseline ölçümleri

Tarih: 13 Ekim 2026. Ölçüm ortamı: Apple M2, Go 1.24, local MongoDB 7.0 (tek
node replica set) ve Redis. Rakamlar mutlak kapasite değil, sonraki fazların
karşılaştırma noktasıdır; aynı makinede aynı komutlarla tekrar alınmalıdır.

## Nasıl tekrar üretilir

```bash
export HOUSEFLOW_TEST_MONGO_URI="mongodb://127.0.0.1:27017/?replicaSet=rs0"
export HOUSEFLOW_TEST_REDIS_URL="redis://127.0.0.1:6379/0"

# Maç başına Mongo ayak izi ve oda yaşam döngüsü sonrası Redis anahtarları
go test -count=1 -v -run 'TestGameMatchPersistenceFootprint|TestRoomLifecycleRedisFootprint' ./tests/

# Frame başına owner ve gateway maliyeti
go test -run XXX -bench HouseRocketsFrame -benchtime 2s ./internal/infrastructure/realtime/

# İki instance'lı uçtan uca yük matrisi (worker başına GOMAXPROCS=1)
HOUSEFLOW_REALTIME_LOAD=true go test -count=1 -v -run TestHouseRocketsLoadMatrix ./tests/
```

## 1. Kalıcı veri — 4 oyunculu normal maç

`TestGameMatchPersistenceFootprint`: oluşturma, 4 join, 4 ready, countdown → running,
runtime owner claim ve kalıcı sonuç.

| Collection | Maç sonunda kalan doküman |
|---|---|
| GameSession | 1 |
| ActiveGameSession | 0 |
| GameSessionCommandReceipt | 11 |
| OutboxMessage | 14 |
| GameRuntimeOwner | 1 |
| GameMatchResult | 1 |
| **Toplam** | **28** |

Mongo komutları: 11 transaction commit, 39 yazma komutu (25 insert, 13 update,
1 delete), 24 find, 1 findAndModify. Outbox kayıtlarını okuyan bir tüketici yoktur.

## 2. Redis — oda yaşam döngüsü sonrası

`TestRoomLifecycleRedisFootprint`: lease, presence ve checkpoint temizlendikten sonra
TTL'siz kalan tek anahtar `room:{id}:fence` sayacıdır; her oda için bir anahtar
süresiz birikir.

## 3. Frame başına maliyet — 8 oyuncu, 8 kontrol eden bağlantı

| Benchmark | ns/frame | B/frame | alloc/frame | Payload |
|---|---|---|---|---|
| `FramePublish` (owner: critical key + room event) | 8.461 | 8.173 | 44 | 3.933 B room event |
| `FrameFanout` (gateway: 8 bağlantıya dağıtım) | 337.243 | 93.932 | 817 | 3.805 B/bağlantı |

Gateway maliyeti owner maliyetinin yaklaşık 40 katıdır: her bağlantı aynı room
event'ini ayrı ayrı decode edip bağlantıya özel modeli yeniden encode eder.

## 4. Uçtan uca yük — iki instance

Süre 3 sn; her satır iki worker'ın ortalamasıdır. Tüm kombinasyonlarda runtime
overload, checkpoint hatası, slow consumer ve admission reddi sıfırdır.

| Oda × oyuncu | CPU %/worker | Redis komut/sn/worker | Socket byte/sn/worker | Checkpoint/sn/worker | ACK p95 üst sınır |
|---|---|---|---|---|---|
| 1 × 2 | 2,1 | 32 | 34.119 | 0,3 | 50 ms |
| 1 × 4 | 3,4 | 52 | 102.433 | 0,3 | 50 ms |
| 1 × 8 | 4,6 | 92 | 327.697 | 0,3 | 100 ms |
| 4 × 2 | 5,8 | 125 | 136.685 | 1,3 | 100 ms |
| 4 × 4 | 9,2 | 205 | 409.953 | 1,3 | 100 ms |
| 4 × 8 | 14,7 | 367 | 1.310.935 | 1,3 | 100 ms |
| 8 × 2 | 4,4 | 247 | 273.985 | 2,8 | 100 ms |
| 8 × 4 | 16,9 | 406 | 821.004 | 3,0 | 50 ms |
| 8 × 8 | 20,0 | 723 | 2.622.347 | 2,8 | 100 ms |

8 × 8 satırında fizik adımı ortalama 0,04 ms, tick aralığı ortalama 16,67 ms
(maksimum 17,8 ms), worker RSS 35 MB'dır. Darboğaz simülasyon değil, serileştirme
ve fanout'tur.

**Ölçüm notu:** CPU yüzdeleri oturumdan oturuma belirgin oynar (aynı Faz 0 kodu ertesi
gün 8 × 2'de %4,4 yerine %12,5 ölçüldü). İki sürüm yalnız aynı oturumda art arda
alınan çalıştırmalarla karşılaştırılmalıdır. Redis komut ve socket byte sayıları
çalıştırmalar arasında %1'in altında değişir ve doğrudan karşılaştırılabilir.

## 5. Faz 1A sonrası — 14 Ekim 2026

Aynı ölçüm testleriyle; değişiklikler ADR-017'dedir.

| Ölçüm | Faz 0 | Faz 1A |
|---|---|---|
| Maç sonunda kalan doküman (4 oyuncu) | 28 | 12 |
| — GameSessionCommandReceipt | 11 (süresiz) | 9 (1 saat TTL) |
| — OutboxMessage | 14 | 0 |
| — GameRuntimeOwner | 1 (süresiz) | 1 (session bitiminden 1 saat sonra silinir) |
| İptal edilen maçın kalıcı izi | session + result + owner (süresiz) | session + result 10 dakika, owner 1 saat sonra silinir |
| Mongo transaction commit | 11 | 10 |
| Mongo yazma komutu | 39 | 25 |
| Oda temizliği sonrası TTL'siz Redis anahtarı | 1 (fence) | 0 |
| Değişiklik yokken `access` event'i | her reconcile turunda | yok |

Faz 1B'de legacy `ActiveGameSession` pointer'ının kullanımı ve `OutboxMessage` collection'ı kaldırıldı;
aktif session okuması tek sorgudur ve maç başına Mongo yazma komutu 25'ten 23'e iner
(pointer insert ve delete yok).

Yük matrisi, Faz 0 ve Faz 1A aynı oturumda art arda çalıştırıldığında CPU, Redis
komut sayısı ve socket byte'larında ölçüm gürültüsünü aşan bir fark göstermez; Faz 1A
frame başına çalışan koda dokunmaz.

## 6. Faz 2 sonrası — 14 Ekim 2026

Core soyutlaması (ADR-018) davranışı ve wire formatını değiştirmez; frame başlığı artık hub
başına bir kez decode edilir, bağlantıya özel projeksiyonu oyun modülü yapar.

| Benchmark (8 oyuncu) | Faz 0 | Faz 2 |
|---|---|---|
| `FramePublish` ns/frame | 8.461 | 8.289 |
| `FrameFanout` ns/frame | 337.243 | 363.145 |
| `FrameFanout` alloc/frame | 817 | 885 |
| Wire byte/bağlantı | 3.805 | 3.805 |

Fanout'taki ~%8'lik artış modül projeksiyonunun `any` modeli ve generic sunucu mesajından
gelir; Faz 4'ün hedefidir. Yük matrisi Faz 1B ve Faz 2 aynı oturumda art arda
çalıştırıldığında CPU'da ölçüm gürültüsünü aşan bir fark göstermez; Redis komut sayısı ve
socket byte'ları aynıdır, ACK p95 üst sınırları aynı veya daha düşüktür.

## 7. Faz 3 sonrası — 14 Ekim 2026

Oyun config'i `Game` collection'ına taşındı (ADR-019). Frame başına çalışan kod değişmedi.
Runtime ayarları session'dan bir kez okunur. Gateway her bağlantıda, lobby her heartbeat'te
küçük bir JSON nesnesi çözer.

| Benchmark (8 oyuncu) | Faz 2 | Faz 3 |
|---|---|---|
| `FramePublish` ns/frame | 8.289 | 8.167 |
| `FrameFanout` ns/frame | 363.145 | 360.105 |
| `FrameFanout` alloc/frame | 885 | 885 |
| Wire byte/bağlantı | 3.805 | 3.805 |

İki instance'lı yük matrisi yeşil: `rooms8Players8` worker başına ~%25,6 CPU,
`runtimeOverloads` 0, `framesCoalesced` 0. Config değişikliğinin diğer instance'a
Redis üzerinden ulaşması testte saniyenin altında ölçüldü.

Kalıcı veri değişimi:
- Session dokümanı `configVersion` ve ~200 byte'lık `settings` alır.
- `Game` collection'ında oyun başına bir doküman tutulur.
- Instance başına config okuması 30 saniyede bir sorgudur.

## 8. Faz 4 sonrası — 14 Ekim 2026

Protokol ve wire byte'ları değişmedi (ADR-020, byte eşitliği testi).

| Benchmark (8 oyuncu) | Faz 0 | Faz 3 | Faz 4 |
|---|---|---|---|
| `FramePublish` ns/frame | 8.461 | 8.167 | ~7.000 |
| `FramePublish` alloc/frame | 44 | 44 | 6 |
| `FrameFanout` ns/frame | 337.243 | 360.105 | ~61.000 |
| `FrameFanout` alloc/frame | 817 | 885 | 151 |
| Wire byte/bağlantı | 3.805 | 3.805 | 3.805 |

Yük testi Faz 3 (HEAD worktree) ve Faz 4 aynı oturumda, 5 saniyelik koşularla dönüşümlü
çalıştırıldı. Değerler iki worker'ın toplamı ve koşuların ortalamasıdır.

| Yerleşim | Senaryo | CPU % (F3 → F4) | Redis komut/s (F3 → F4) |
|---|---|---|---|
| colocated | 1 oda × 8 | 7,7 → 6,3 | 25 → 6 |
| colocated | 4 oda × 8 | 24,3 → 18,7 | 90 → 14 |
| colocated | 8 oda × 8 | 40,3 → 31,0 | 175 → 23 |
| split | 4 oda × 8 | 30,2 → 28,0 | 733 → 734 |
| split | 8 oda × 8 | 38,4 → 40,0 | 1.448 → 1.459 |

- **Colocated** (odanın tüm socket'leri aynı instance'ta): frame'ler Redis'e hiç gitmiyor
  (`roomPublishesPerSecond=0`). Redis komutları ~%87, CPU ~%23 düşüyor.
- **Split** (oyuncular instance'lara dönüşümlü): her odada uzak socket olduğu için frame'ler Redis'ten
  gitmek zorunda; Redis hacmi aynı. CPU farkı bu makinede ölçüm gürültüsünün içinde. Fanout
  kazancı oda başına saniyede birkaç ms; toplam CPU'yu Redis ve socket I/O belirliyor.
- **Tick aralığı p99**: iki sürümde de 20 ms bucket'ında, ortalama 16,67 ms. Fanout fizik
  goroutine'inde çalışmadığından bu değer değişmedi. İlk turda iki sürümde de iki worker'da aynı
  anda görülen 40-77 ms'lik tekil boşluklar makine kaynaklıydı; dönüşümlü tekrar koşularında
  görülmedi (maksimum 18-28 ms).
