# Testler

Geliştirme sırasında yalnızca değişikliğin etkileyebileceği testler çalışır. Tüm
paket, PR açıldığında CI'da çalışır (`.github/workflows/deploy.yml`, `verify`).

## Etkilenen testleri çalıştırmak

```bash
scripts/testAffected.sh                 # master'a göre değişenler (commit + stage + çalışma ağacı + yeni dosyalar)
scripts/testAffected.sh --dry-run       # yalnızca neyin neden çalışacağını yazar
scripts/testAffected.sh --base dev      # başka bir dala göre
scripts/testAffected.sh --dry-run --file internal/x/y.go   # "bunu değiştirirsem ne çalışır?"
scripts/testAffected.sh -- -run TestX   # go test'e ek argüman
scripts/testAffected.sh --all           # her şey, CI ile aynı
```

Betik değişen her dosyayı dizininin Go paketine eşler. Fixture ve testdata
dosyaları, üstlerindeki en yakın pakete eşlenir. Ardından `go list -test`
grafiğinde bu paketlere bağlı test paketlerini seçer ve her birinin hangi
dosya yüzünden seçildiğini yazar. İstisnalar:

- `go.mod` / `go.sum` değişirse ya da bir `.go` dosyasının paketi silinmişse her şey çalışır.
- `internal/` ve `cmd/` altındaki kaynak ile `fly.toml` dosyasını import etmeden
  okuyan testler `tests/architecture` içindedir (yaklaşık 2 sn). Bu paket, bu
  dosyalardan biri değişince çalışır. Dosya okuyan yeni bir test de buraya konur.
- Doküman, CI, Docker ve `scripts/` değişiklikleri hiçbir testi çalıştırmaz.

Seçim, üretim paketlerinin ayrımı kadar incedir. Örneğin gateway fixture'ı
gerçek `internal/controllers` paketini kullanır ve bu paket tüm uygulama
kodunu bağlar. Bu yüzden oyun dışı bir komut değişikliği de oyun testlerini
seçer.

`HOUSEFLOW_TEST_MONGO_URI` tanımlı değilse ve `tests/` altından bir paket
seçilmişse, betik geçici `mongod` (replica set) ve `redis-server` süreçleri
başlatır, iş bitince kapatır (Homebrew `mongodb-community` ve `redis`).
`--no-services` ile başlatmaz; entegrasyon testleri o zaman kendini atlar.

## Düzen

| Paket | Kapsam |
| --- | --- |
| `architecture` | CQRS/katman kuralları, kaynak ve deploy dosyası sözleşmeleri, HTTP hataları, güvenlik, rate limit |
| `account` | auth, sosyal giriş, hesap silme, kullanıcı, lokalizasyon |
| `house` | ev, davet kodu, profil/çıkış, eşzamanlılık |
| `chore` | görev akışı, deadline, fotoğraf, bildirim, görev kataloğu |
| `gamecore` | oturum domain'i, persistence, katalog, config, lobi niyetleri, modül sözleşmeleri |
| `realtime` | koordinasyon, gateway, oda runtime, kapasite, sıkıştırma |
| `games/<oyun>` | houseRockets, houseTanks, fausSwitch, ticTacToe |
| `testkit` | `TestMain` (`testkit.Main`), Mongo DB havuzu, Redis seçimi, `RepoPath` |
| `testkit/appkit` | uygulama fixture'ı (`NewConcurrencyFixture`) |
| `testkit/gamekit` | oyun oturumu, oda runtime ve iki instance'lı gateway fixture'ları |

Paketler kendi süreçlerinde ve paralel çalışır.

- **Mongo:** Her test havuzdan kendine ait, migrate edilmiş bir DB alır (`testkit/databasePool.go`).
- **Redis:** Testler DB'yi `FlushDB` ile temizlediği için her paket ayrı bir
  Redis DB'si kullanır. `HOUSEFLOW_TEST_REDIS_URL` ilk DB'yi belirtir (`/1`),
  paket bu DB'ye kendi slotunu ekler.
- **Redis Pub/Sub DB'ye göre ayrılmaz:** Oda olay kanalları oda ID'siyle
  adlandırılır (`houseflow:coordination:v1:room:<id>:events`). Aynı anda
  çalışan iki paket aynı oda ID'sini kullanırsa birbirinin olaylarını alır.
  Örneğin yabancı bir erişim olayı soketi "house membership revoked" ile
  kapatır, istemci de 1006 görür. Gateway'e ya da `RoomManager`'a giden
  oturumların ID'si benzersiz olmalı (`gamekit.PersistRocketsSession` gibi).
  Sabit ID yalnızca Redis'e ulaşmayan saf modül testlerinde kullanılabilir.
- **Yeni paket:** Kendi `main_test.go` dosyasında `testkit.Main(m, "<ad>")`
  çağırır ve `testkit/main.go` içindeki `redisSlots` tablosuna eklenir.

Paket içindeki testler sıralı çalışır. `t.Parallel` denendi ve ölçüldü: realtime
testleri yük altında kararsızlaşıyor ve eşzamanlı migration'lar mongod'u
tıkıyor, bu yüzden geri alındı.

## Elle çalıştırmak

```bash
export HOUSEFLOW_TEST_MONGO_URI="mongodb://127.0.0.1:27017/?replicaSet=rs0"
export HOUSEFLOW_TEST_REDIS_URL="redis://127.0.0.1:6379/1"
go test -race -count=1 ./tests/games/houseTanks
```

House Tanks simülasyon fixture'ını yeniden üretmek için:
`go test ./tests/games/houseTanks -run TestHouseTanksSimulationFixture -update-house-tanks`.
