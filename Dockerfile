FROM golang:1.22-alpine AS build
WORKDIR /src

COPY go.mod ./
COPY cmd ./cmd
COPY internal ./internal

RUN CGO_ENABLED=0 GOOS=linux go build -o /kv-server ./cmd/server

FROM alpine:3.20
RUN adduser -D -u 10001 appuser
RUN mkdir -p /data /app && chown appuser:appuser /data /app
USER appuser
WORKDIR /app

COPY --from=build /kv-server /usr/local/bin/kv-server

EXPOSE 8080
ENTRYPOINT ["kv-server"]
