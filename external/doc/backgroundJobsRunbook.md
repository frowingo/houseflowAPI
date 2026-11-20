# Arka plan işleri — runbook

Arka plan işleri Asynq ile `faus-valkey` üzerinde çalışır (karar: ADR-032). Bu belge, işler
takıldığında ya da uyarı geldiğinde ne yapılacağını anlatır.

## Nerede çalışırlar

| Parça | Yer |
|---|---|
| Port (iş tipi, seçenekler, zamanlayıcı) | `internal/application/jobs/abstract` |
| Asynq ve süreç içi runtime, zamanlayıcı, istatistik | `internal/infrastructure/jobs` |
| Kurulum, rol, telemetry | `internal/adaptors/jobs.go`, `internal/adaptors/jobsTelemetry.go` |

- `REDIS_URL` boşsa ya da geçersizse işler süreç içinde çalışır: tekrar deneme ve kalıcılık yoktur.
  Bu durum yalnız yerel geliştirme içindir.
- `FAUSCREW_ROLE=all` (varsayılan) olan makine hem iş üretir hem tüketir. `api` olan makine yalnız
  üretir ve zamanlar.
- `houseflowapi worker` komutuyla çalışan makine yalnız iş işler (bkz. "Ayrı worker grubu").

**Kuyruklar:** `fauscrew-critical`, `fauscrew-default` ve `fauscrew-low`. Ağırlıkları 6/3/1'dir.

**İşler:**

| Tip | Kuyruk | Not |
|---|---|---|
| `fauscrew:email:send` | critical | Hiç arşivlenmez: payload adres ve kod taşır. Süresi geçen ya da son denemede başarısız olan iş düşürülür. |
| `fauscrew:notification:fanout` | default | Bildirim başına bir tane. Her cihaz için bir push işi açar. |
| `fauscrew:push:send` | default | Payload yalnız id taşır. Geçersiz token silinir. 24 saatten eski push düşürülür. |
| `fauscrew:chore:deadline-reconcile` | default | Dakikada bir. Tekrar denenmez, bir sonraki dakika telafi eder. |
| `fauscrew:chore:deadline-full-scan` | low | Günde bir (UTC 00:00) ve günün ilk açılışında. |

## Telemetry ve uyarılar

Her makine dakikada bir JSON log satırı yazar:

- `"msg":"jobMetrics"` satırında şunlar bulunur:
  - Kuyruk başına: pending, active, scheduled, retry, archived, latencyMs ve günlük processed/failed. Bu değerler cluster geneli içindir.
  - Bu makinenin iş tipi başına başarı/hata sayısı ve süreleri.
  - Valkey'in kullandığı ve izin verilen en yüksek bellek.
- Bir kural tetiklenince `"msg":"jobAlert"` uyarısı yazılır.

```bash
fly logs -a houseflowapi | grep jobAlert
```

| `kind` | Anlamı | Ne yapılır |
|---|---|---|
| `archivedGrew` | Son örnekten bu yana iş arşive düştü: denemeleri bitti ya da kalıcı hata verdi. | Aşağıdaki "Arşivdeki işler" bölümüne bak. Hatanın sebebi, aynı makinenin `job ... failed` log satırlarında yazar. |
| `queueLatency` | En eski bekleyen iş limiti aştı: critical 1 dk, default 5 dk, low 30 dk. | İşleyen makine var mı (`FAUSCREW_ROLE`)? Kuyruk duraklatılmış mı? Bir iş tipi tüm worker'ları mı tutuyor (`jobMetrics.jobs.*.maxMs`)? |
| `queuePaused` | Kuyruk duraklatılmış. | Bilerek yapılmadıysa `asynq queue unpause <kuyruk>`. |
| `valkeyMemory` | Valkey belleği `maxmemory`'nin %80'ini geçti. | "Valkey belleği" bölümüne bak. Bellek dolunca kuyruğa ekleme hata verir (`noeviction`). |
| `statsUnavailable` | Valkey'e ulaşılamıyor. Kesinti başına bir kez yazılır. | `fly status -a faus-valkey` ve `/api/v1/base/health/jobs` ile kontrol et. Mail bu sırada doğrudan SMTP ile gider, push kaybolur, deadline bir sonraki erişilebilir dakikada telafi edilir. |

## Valkey'e yerelden bağlanma

Valkey yalnız Fly iç ağında açıktır. Önce yerel bir port açılır:

```bash
fly proxy 16379:6379 -a faus-valkey
```

Parola `faus-valkey` uygulamasının `VALKEY_PASSWORD` secret'ıdır. Aşağıdaki örneklerde
`$VALKEY_PASSWORD` olarak geçer.

**Asynq CLI**

Asynq ile aynı sürüm kullanılır:

```bash
go install github.com/hibiken/asynq/tools/asynq@v0.26.0
```

```bash
asynq --uri=127.0.0.1:16379 --password="$VALKEY_PASSWORD" stats
```

Aşağıdaki komutlarda `--uri` ve `--password` bayrakları kısaltılmıştır. Komut adları v0.26 içindir;
`asynq --help` ile doğrulanabilir.

**Asynqmon (web arayüzü)**

Yalnız yerelde çalıştırılır, deploy edilmez:

```bash
docker run --rm -p 8080:8080 hibiken/asynqmon --redis-addr=host.docker.internal:16379 --redis-password="$VALKEY_PASSWORD"
```

## Arşivdeki işler

```bash
asynq task ls --queue=fauscrew-default --state=archived
```

```bash
asynq task inspect --queue=fauscrew-default --id=<id>
```

- Sebep giderildiyse iş tekrar çalıştırılır:
  - Tek iş için: `asynq task run --queue=<kuyruk> --id=<id>`
  - Hepsi için: `asynq task runall --queue=<kuyruk> --state=archived`
- Artık anlamsızsa silinir: `asynq task delete ...` ya da `asynq task deleteall --queue=<kuyruk> --state=archived`.
  Örneğin 24 saati geçmiş push'lar zaten düşürülür, yeniden çalıştırmanın faydası yoktur.
- Arşiv kendiliğinden 90 gün ya da 10.000 işle sınırlıdır.
- Mail işleri arşive hiç düşmez, bu yüzden arşivde e-posta adresi ya da kod bulunmaz.

## Kuyruğu durdurma

Bir iş tipi hatalı veri üretiyorsa ya da dış servis (FCM, SMTP) sorunluysa:

```bash
asynq queue pause fauscrew-default
```

- Duraklatılan kuyruğa iş eklenmeye devam eder ama işlenmez.
- Sorun çözülünce `asynq queue unpause fauscrew-default` ile devam ettirilir.
- Duraklatma `queuePaused` uyarısı üretir. Bu, unutulan bir duraklatmayı yakalamak içindir.

## Valkey belleği

```bash
valkey-cli -h 127.0.0.1 -p 16379 -a "$VALKEY_PASSWORD" INFO memory
```

```bash
asynq --uri=127.0.0.1:16379 --password="$VALKEY_PASSWORD" queue inspect fauscrew-default
```

- `maxmemory 128mb` ve `noeviction` ayarları `deploy/faus-valkey/valkey.conf` içindedir.
  Coordination lease'leri ve işler sessizce silinmemelidir.
- Belleği çoğunlukla arşiv, `completed` (Retention süresince tutulan işler) ve uzak tarihli
  `scheduled` işler doldurur. Zamanlanmış bir iş yaklaşık 0,8 KB tutar.
- Önce gereksiz arşiv silinir. Kalıcı büyüme varsa `maxmemory` ve makine belleği birlikte artırılır.
  AOF rewrite fork'u için makinede `maxmemory` kadar boşluk kalmalıdır.

## Ayrı worker grubu

Şu an işleri API makineleri işler (`FAUSCREW_ROLE=all`). Ayrı bir worker grubu şu durumlarda
açılır:

- İşler API isteklerini yavaşlatıyorsa.
- API makineleri boşta kapanırken periyodik işlerin aksamadan çalışması gerekiyorsa. Worker'ın
  `http_service`'i olmadığı için Fly onu boşta kapatmaz.

Worker komutu:

- `:3163` portunda yalnız health endpoint'lerini açar: `/api/v1/base/health/live`, `/ready`
  (Mongo) ve `/jobs` (Valkey).
- `REDIS_URL` olmadan başlamaz.
- `FAUSCREW_ROLE` değerini dikkate almaz.

**Açma.** Rolling ve rollback güvenli olması için iki deploy ile yapılır:

1. `fly.toml` içindeki yorum satırındaki `[processes]`, worker `[[vm]]` ve worker `[checks]`
   bloklarını aç ve deploy et.
   - Bu adımda API hâlâ `all` rolündedir; işleri iki grup birlikte işler.
   - `fly logs` içinde worker'ın `worker started` satırını ve `jobMetrics.jobs` sayaçlarını
     kontrol et.
2. API'yi yalnız üretici yap ve tekrar deploy et:

   ```bash
   fly secrets set FAUSCREW_ROLE=api -a houseflowapi
   ```

   Ya da `fly.toml` içine `[env] FAUSCREW_ROLE = 'api'` eklenir. Worker bu değeri dikkate almaz.

**Kapatma ya da J6 öncesine geri dönme.**

1. Önce `FAUSCREW_ROLE`'u kaldır (`fly secrets unset FAUSCREW_ROLE -a houseflowapi`). Böylece
   API makineleri yeniden iş işler.
2. Sonra `[processes]` bloğunu kaldırıp deploy et; worker makineleri silinir.

- `worker` komutunu tanımayan eski bir sürüme geri dönülürse worker makinesi "unknown command" ile
  açılamaz.
- API `api` rolündeyse bu sırada işleri kimse işlemez. İşler kaybolmaz, kuyrukta birikir ve
  `queueLatency` uyarısı gelir.

**Yeni iş tipi eklerken** handler'ı `cmd/api/backgroundJobs.go` içinde bağla. Başka yerde bağlanan
iş, worker'da olmaz. `tests/architecture` içindeki sözleşme testi bunu yakalar.

## Deploy notları

- Rolling deploy sırasında eski sürümün tanımadığı bir iş tipi tekrar denenir ve yeni sürüm onu işler.
  Payload alan adları sabittir.
- `kill_timeout = 30`: kapanışta önce HTTP boşaltılır, sonra çalışan işler en fazla 8 sn beklenir.
  Yarıda kalan iş Asynq tarafından başka makineye yeniden verilir. Bu yüzden handler'lar idempotenttir.
- Zamanlanmış işler her makinede zamanlanır. Aralığa özel TaskID sayesinde her aralık cluster'da
  bir kez çalışır.
