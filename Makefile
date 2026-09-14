COMPOSE_PARALLEL_LIMIT ?= 1
COMPOSE = COMPOSE_PARALLEL_LIMIT=$(COMPOSE_PARALLEL_LIMIT) docker compose

.PHONY: bootstrap up down reset test smoke smoke-private clean logs

bootstrap:
	$(COMPOSE) build server toolbox api dependency
	$(COMPOSE) pull prometheus

up:
	$(COMPOSE) up -d --build --wait server
	@printf '\nSRE Lab is running\nhttp://localhost:8080\n'

down:
	$(COMPOSE) down

# Only this project's disposable data is removed; no Docker-wide prune.
clean:
	$(COMPOSE) down --volumes --remove-orphans

reset:
	$(MAKE) clean
	$(MAKE) up

test:
	$(COMPOSE) --profile test run --rm --build --no-deps test
	$(COMPOSE) run --rm --no-deps --entrypoint promtool prometheus check config /etc/prometheus/prometheus.yml

smoke: up
	$(COMPOSE) --profile test run --rm --build --no-deps browser-test

smoke-private:
	$(MAKE) smoke COMPOSE='docker compose -f docker-compose.yml -f runtime/docker/compose.private.yml'

logs:
	$(COMPOSE) logs -f --tail=100
