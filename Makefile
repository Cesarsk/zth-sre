COMPOSE_PARALLEL_LIMIT ?= 1
COMPOSE = COMPOSE_PARALLEL_LIMIT=$(COMPOSE_PARALLEL_LIMIT) docker compose

.PHONY: bootstrap up down reset test smoke smoke-private verify-catalog verify-exercises verify-exercises-full clean logs

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

verify-exercises: up
	BASE_URL=http://localhost:8080 node tests/exercises/simulate.mjs

verify-exercises-full: up
	BASE_URL=http://localhost:8080 node tests/exercises/simulate.mjs --full

verify-catalog:
	node tests/exercises/catalog-consistency.mjs

logs:
	$(COMPOSE) logs -f --tail=100
