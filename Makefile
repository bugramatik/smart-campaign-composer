.PHONY: up down logs test test-backend test-frontend clean

up:
	docker compose up --build -d
	@echo "UI: http://localhost:3000  API: http://localhost:8080  Provider: http://localhost:8090/stats"

down:
	docker compose down

logs:
	docker compose logs -f api worker

## test: backend unit + integration tests (real Postgres/Redis in Docker) and frontend tests
test: test-backend test-frontend

test-backend:
	docker compose -f docker-compose.test.yml up --build --abort-on-container-exit --exit-code-from test test; \
	status=$$?; docker compose -f docker-compose.test.yml down -v >/dev/null 2>&1; exit $$status

test-frontend:
	docker run --rm -v $(CURDIR)/frontend:/app -w /app node:24-alpine sh -c "npm ci --no-audit --no-fund && npm run typecheck && npm test"

clean:
	docker compose down -v
