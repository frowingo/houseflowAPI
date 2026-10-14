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

Yük matrisi, Faz 0 ve Faz 1A aynı oturumda art arda çalıştırıldığında CPU, Redis
komut sayısı ve socket byte'larında ölçüm gürültüsünü aşan bir fark göstermez; Faz 1A
frame başına çalışan koda dokunmaz.
