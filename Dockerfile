FROM golang:1.24-bookworm AS builder

WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /webcopy .

FROM debian:bookworm-slim

RUN apt-get update && apt-get install -y \
    chromium \
    chromium-sandbox \
    && rm -rf /var/lib/apt/lists/*

ENV CHROME_BIN=/usr/bin/chromium
ENV PORT=8080

RUN mkdir -p /app/downloads /app/templates

COPY --from=builder /webcopy /app/webcopy
COPY templates/index.html /app/templates/index.html

WORKDIR /app

EXPOSE 8080

CMD ["/app/webcopy"]
