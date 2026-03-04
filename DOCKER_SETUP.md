# HouseFlow API - Docker Setup

## ⚡ Hızlı Başlangıç

### 1. Docker Compose ile Başlat
```bash
docker-compose up -d
```

Bu komut şunları çalıştıracak:
- **HouseFlow API**: http://localhost:3162
- **MongoDB**: mongodb://localhost:27017
- **Mongo Express** (opsiyonel): http://localhost:8081

### 2. API'yi Test Et
```bash
# Swagger UI
curl http://localhost:3162/swagger/

# API Health Check
curl http://localhost:3162/api/v1/health
```

### 3. Logları İzle
```bash
docker-compose logs -f api
```

---

## 📋 Gereksinimler

- **Docker**: 20.10+
- **Docker Compose**: 2.0+

---

## 🛠️ Manual Docker Build

### Build
```bash
docker build -t houseflow-api:latest .
```

### Run
```bash
docker run -p 3162:3162 \
  -e MONGO_HOST=host.docker.internal \
  -v $(pwd)/internal/config/config.json:/root/internal/config/config.json \
  houseflow-api:latest
```

---

## 📝 Environment Variables

`docker-compose.yml` içinde ayarlanabilen değişkenler:

| Variable | Default | Açıklama |
|----------|---------|----------|
| MONGO_HOST | mongodb | MongoDB host |
| MONGO_PORT | 27017 | MongoDB port |
| MONGO_DB | houseflowDb | Database adı |

---

## 🧹 Cleanup

Tüm containerları ve volumeleri kaldır:
```bash
docker-compose down -v
```

Sadece containerları durdur:
```bash
docker-compose stop
```

---

## 📊 Services

### houseflow-api
- Go Fiber API server
- Port: 3162
- Health check: Swagger endpoint

### mongodb
- MongoDB 7.0
- Port: 27017
- Persistent storage: `mongodb-data` volume

### mongo-express (Opsiyonel)
- MongoDB Admin Panel
- Port: 8081
- URL: http://localhost:8081

---

## 🔗 Useful Links

- **API Documentation**: http://localhost:3162/swagger/
- **MongoDB Express**: http://localhost:8081

---

## ⚠️ Production Dosyaları

Production'da kullanmadan önce:
1. `.env` dosyası oluştur (environment variables)
2. Config dosyalarını secure hale getir
3. MongoDB credentials ayarla
4. CORS, security headers yapılandır

---

## 🐛 Troubleshooting

### Container başlamıyor
```bash
docker-compose logs api
```

### Database bağlantısı hatası
- MongoDB'nin çalıştığını kontrol et: `docker-compose logs mongodb`
- Config.json dosyasını kontrol et

### Port zaten kullanımda
```bash
# Port değiştir docker-compose.yml içinde:
ports:
  - "3163:3162"  # 3162 yerine 3163 kullan
```

---

## 📦 Multi-Stage Build

Dockerfile, optimized image oluşturmak için multi-stage build kullanır:
1. **Builder Stage**: Go binary build et
2. **Final Stage**: Alpine Linux (sadece runtime)

Sonuç: ~15MB image vs ~300MB full Go image
