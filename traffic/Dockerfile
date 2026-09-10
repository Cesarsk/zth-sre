FROM golang:1.26.1-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY traffic/ traffic/
COPY cmd/traffic/ cmd/traffic/
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/traffic ./cmd/traffic

FROM grafana/k6:0.57.0 AS k6

FROM alpine:3.21.3
RUN addgroup -g 1000 traffic && adduser -D -u 1000 -G traffic traffic
COPY --from=build /out/traffic /usr/local/bin/traffic
COPY --from=k6 /usr/bin/k6 /usr/bin/k6
COPY traffic/script.js /app/traffic/script.js
USER 1000:1000
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/traffic"]
