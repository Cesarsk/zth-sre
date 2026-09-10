FROM node:22.14.0-bookworm-slim AS frontend
WORKDIR /src/frontend
COPY frontend/package*.json ./
RUN npm ci
COPY frontend/ ./
RUN npm run build

FROM golang:1.26.1-bookworm AS go-base
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ cmd/
COPY internal/ internal/
COPY scenarios/ scenarios/
COPY traffic/ traffic/

FROM go-base AS test
ENV GOCACHE=/tmp/go-cache HOME=/tmp
USER 1000:1000
CMD ["sh", "-c", "go test -race -count=1 ./... && go vet ./..."]

FROM go-base AS build
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/server ./cmd/server && \
    CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/toolbox ./cmd/toolbox && \
    CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/demo ./cmd/demo && \
    CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/api-lb ./cmd/api-lb

FROM alpine:3.21.3 AS runtime
EXPOSE 8080

FROM runtime AS demo
COPY --from=build /out/demo /usr/local/bin/demo
USER 1000:1000
ENTRYPOINT ["/usr/local/bin/demo"]

FROM runtime AS server
COPY --from=build /out/server /usr/local/bin/server
COPY --from=frontend /src/frontend/dist /app/frontend
COPY --from=build /src/scenarios /app/scenarios
RUN mkdir /data && chown 1000:1000 /data
USER 1000:1000
ENTRYPOINT ["/usr/local/bin/server"]

FROM runtime AS api-lb
COPY --from=build /out/api-lb /usr/local/bin/api-lb
USER 1000:1000
ENTRYPOINT ["/usr/local/bin/api-lb"]

FROM mcr.microsoft.com/playwright:v1.56.1-noble AS browser-test
WORKDIR /tests
COPY tests/browser/package*.json ./
RUN npm ci
COPY tests/browser/ ./
CMD ["npm", "test"]
