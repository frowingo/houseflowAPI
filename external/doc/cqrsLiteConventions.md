# CQRS-lite Uygulama Kuralları

`internal/application` yalnız taşıma katmanından bağımsız application
use-case'lerini içerir. Ortak handler sözleşmeleri ve process-içi mediator
`internal/infrastructure/cqrs` altında tutulur.

Bir command, state değiştirebilen bir niyeti temsil eder. Bir query yalnızca
state okur ve business side effect üretmez. Her ikisi de uygulamaya ait tek bir
process-içi mediator üzerinden çalıştırılır. Bu mediator harici message broker
veya dayanıklı iş kuyruğu değildir.

Handler implementasyonları aşağıdaki kurallara uyar:

- Tek giriş noktası olarak `Handle` metodunu sunar.
- `Handle(context.Context, Request) (Result, error)` imzasını kullanır.
- Fiber veya başka bir HTTP taşıma tipi yerine use-case'e özel command/query alır.
- Transaction sınırını ve business validation'ı command handler içinde tutar.
- Başka bir handler'ı doğrudan çağırmaz.
- Başka bir handler'ı mediator üzerinden zincirlemez.
- HTTP eşlemesi için mevcut typed application error'larını döndürür.

Birden fazla handler'ın ihtiyaç duyduğu business kuralları, handler'lara inject
edilebilen küçük policy bileşenlerinde tutulur. Controller yalnız request parse,
request validation, authentication bilgisini çıkarma ve HTTP response eşlemeden
sorumludur.

Mediator global singleton olarak kullanılmaz. Composition root'ta oluşturulur,
handler'lar request tipine göre bir kez kaydedilir ve controller'lara yalnız
`Sender` sözleşmesi verilir. Eksik handler çağrısı hata döner; mükerrer kayıt
uygulama başlangıcında fail-fast davranır. Transaction sınırları mediator'a
taşınmaz ve command handler içinde kalır. Şimdilik yalnız `Send` desteklenir;
event publish/notification davranışı kapsam dışıdır.
