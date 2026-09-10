FROM golang:1.26.1-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/toolbox/ cmd/toolbox/
COPY internal/server/ internal/server/
COPY internal/terminal/ internal/terminal/
COPY internal/progress/ internal/progress/
COPY internal/history/ internal/history/
COPY internal/scenario/ internal/scenario/
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /toolbox ./cmd/toolbox

FROM debian:12.10-slim
RUN apt-get update && apt-get install -y --no-install-recommends \
    bash curl wget jq dnsutils iputils-ping traceroute iproute2 procps \
    openssl netcat-openbsd ca-certificates && \
    rm -rf /var/lib/apt/lists/* && \
    useradd --uid 1000 --create-home --shell /bin/bash learner && \
    setcap -r /usr/bin/ping
COPY --from=build /toolbox /usr/local/bin/toolbox
ENV HOME=/home/learner TERM=xterm-256color PS1="learner@sre-lab:\w\$ "
WORKDIR /home/learner
USER 1000:1000
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/toolbox"]
