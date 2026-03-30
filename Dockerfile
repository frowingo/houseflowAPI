# Builder stage
FROM golang:1.24-alpine AS builder

# Build tools yükle
RUN apk add --no-cache git

WORKDIR /app

# Go modülleri kopyala
COPY go.mod go.sum ./

# Modüleri indir
RUN go mod download

# swag CLI kur
RUN go install github.com/swaggo/swag/cmd/swag@latest

# Kaynak kodları kopyala
COPY . .

# Swagger docs'u generate et
RUN swag init -g cmd/api/main.go -o external/swagger/docs

# Binary'yi build et
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o houseflowapi ./cmd/api

# Final stage
FROM alpine:latest

RUN apk --no-cache add ca-certificates

WORKDIR /root/

# Builder'dan binary'yi kopyala
COPY --from=builder /app/houseflowapi .

# Config dosyalarını kopyala
COPY ./internal/config/config.json ./internal/config/

# Port'u expose et
EXPOSE 3162

# Health check
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
  CMD wget --quiet --tries=1 --spider http://localhost:3162/swagger/index.html || exit 1

# Uygulamayı çalıştır
CMD ["./houseflowapi"]
