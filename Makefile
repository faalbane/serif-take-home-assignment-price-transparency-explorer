# Price Transparency Explorer
#
# Quick start:
#   make dev     - backend on :8080 and the Vite dev server on :5173
#   make run     - build the frontend and serve everything from :8080
#   make test    - run every test

SHELL := /bin/bash
BACKEND := backend
FRONTEND := frontend

.PHONY: help
help:
	@echo "Price Transparency Explorer"
	@echo ""
	@echo "  make dev        Run backend (:8080) and frontend dev server (:5173)"
	@echo "  make run        Build the UI and serve the whole app from :8080"
	@echo "  make backend    Run the API only (:8080)"
	@echo "  make frontend   Run the Vite dev server only (:5173)"
	@echo "  make build      Build the frontend and the server binary"
	@echo "  make test       Run Go tests and the frontend typecheck"
	@echo "  make test-race  Run Go tests under the race detector"
	@echo "  make cover      Go test coverage summary"
	@echo "  make verify     Cross-check the running API against the raw CMS files"
	@echo "  make fast       Run with a small ingest cap, for quick iteration"
	@echo "  make offline    Run using only files already in the cache"
	@echo "  make clean      Remove build output and the downloaded-file cache"

.PHONY: deps
deps:
	cd $(FRONTEND) && npm install

$(FRONTEND)/node_modules:
	cd $(FRONTEND) && npm install

.PHONY: backend
backend:
	cd $(BACKEND) && go run ./cmd/server

.PHONY: frontend
frontend: $(FRONTEND)/node_modules
	cd $(FRONTEND) && npm run dev

# Runs both and shuts both down on Ctrl-C.
.PHONY: dev
dev: $(FRONTEND)/node_modules
	@echo "backend :8080  frontend :5173  (Ctrl-C stops both)"
	@trap 'kill 0' EXIT INT TERM; \
	(cd $(BACKEND) && go run ./cmd/server) & \
	(cd $(FRONTEND) && npm run dev) & \
	wait

.PHONY: build
build: $(FRONTEND)/node_modules
	cd $(FRONTEND) && npm run build
	cd $(BACKEND) && go build -o bin/server ./cmd/server

# Single-process mode: the Go server also serves the built UI.
.PHONY: run
run: build
	@echo "open http://localhost:8080"
	cd $(BACKEND) && ./bin/server -static-dir ../$(FRONTEND)/dist

.PHONY: fast
fast:
	cd $(BACKEND) && go run ./cmd/server -max-items 200

.PHONY: offline
offline:
	cd $(BACKEND) && go run ./cmd/server -offline

.PHONY: test
test: $(FRONTEND)/node_modules
	cd $(BACKEND) && go test ./... -count=1
	cd $(FRONTEND) && npm run typecheck

.PHONY: test-race
test-race:
	cd $(BACKEND) && go test -race ./... -count=1

.PHONY: cover
cover:
	cd $(BACKEND) && go test ./... -coverprofile=/tmp/pte-cover.out -count=1 && \
		go tool cover -func=/tmp/pte-cover.out | tail -1

.PHONY: lint
lint:
	cd $(BACKEND) && gofmt -l . && go vet ./...

# Requires the server to be running on :8080 and python3 on PATH.
.PHONY: verify
verify:
	python3 scripts/crosscheck.py

.PHONY: clean
clean:
	rm -rf $(FRONTEND)/dist $(BACKEND)/bin $(BACKEND)/.cache .cache
