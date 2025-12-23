BIN  ?= strfry
APPS ?= dbutils relay mesh
OPT  ?= -O3 -g

include golpe/rules.mk

LDLIBS += -lsecp256k1 -lzstd
INCS += -Iexternal/negentropy/cpp

# macOS homebrew include paths (for flatbuffers, secp256k1, etc.)
ifeq ($(shell uname -s),Darwin)
    INCS += -I/opt/homebrew/include
    LDFLAGS += -L/opt/homebrew/lib
endif

build/StrfryTemplates.h: $(shell find src/tmpls/ -type f -name '*.tmpl')
	PERL5LIB=golpe/vendor/ perl golpe/external/templar/templar.pl src/tmpls/ strfrytmpl $@

src/apps/relay/RelayWebsocket.o: build/StrfryTemplates.h

# ============================================================================
# Docker and Testing Commands
# ============================================================================

.PHONY: docker-build docker-up docker-down docker-restart docker-logs \
        test test-verbose test-short test-security test-ratelimit test-query test-load \
        test-setup test-clean help-test

# Docker commands
docker-build:
	@echo "🔧 Building Docker image..."
	docker-compose build

docker-up:
	@echo "🚀 Starting strfry relay..."
	docker-compose up -d
	@echo "⏳ Waiting 5 seconds for relay to be ready..."
	@sleep 5
	@echo "✅ Relay is running!"

docker-down:
	@echo "🛑 Stopping strfry relay..."
	docker-compose down

docker-restart:
	@echo "🔄 Restarting strfry relay..."
	@docker-compose stop strfry-relay
	@docker-compose rm -f strfry-relay
	@docker-compose up -d strfry-relay
	@echo "⏳ Waiting 5 seconds for relay to be ready..."
	@sleep 5
	@echo "✅ Relay restarted with fresh state!"

docker-logs:
	docker-compose logs -f strfry-relay

# Test commands
test-setup: docker-build docker-up

test: docker-restart
	@echo ""
	@echo "🧪 Running all tests..."
	@echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
	@cd strfry-nip-tests && go test -timeout 5m
	@echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
	@echo "✅ All tests completed!"

test-verbose: docker-restart
	@echo ""
	@echo "🧪 Running all tests (verbose)..."
	@echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
	@cd strfry-nip-tests && go test -v -timeout 5m
	@echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
	@echo "✅ All tests completed!"

test-short: docker-restart
	@echo ""
	@echo "🧪 Running quick tests (skipping load tests)..."
	@echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
	@cd strfry-nip-tests && go test -short -timeout 2m
	@echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
	@echo "✅ Quick tests completed!"

test-security: docker-restart
	@echo "🔒 Running security tests..."
	@cd strfry-nip-tests && go test -v -timeout 2m -run "^TestSecurity|^TestEvent|^TestAuth|^TestPrivilege|^TestWhitelist|^TestInput"

test-ratelimit: docker-restart
	@echo "⏱️  Running rate limit tests..."
	@cd strfry-nip-tests && go test -v -timeout 5m -run "^TestRateLimit"

test-query: docker-restart
	@echo "🔍 Running query tests..."
	@cd strfry-nip-tests && go test -v -timeout 2m -run "^TestQuery"

test-load: docker-restart
	@echo "📊 Running load tests..."
	@cd strfry-nip-tests && go test -v -timeout 5m -run "^TestLoad"

test-coverage: docker-restart
	@echo "🧪 Running tests with coverage..."
	@cd strfry-nip-tests && go test -coverprofile=coverage.out -timeout 5m
	@cd strfry-nip-tests && go tool cover -html=coverage.out -o coverage.html
	@echo "✅ Coverage report generated: strfry-nip-tests/coverage.html"

test-clean:
	@echo "🗑️  Cleaning up test artifacts..."
	@cd strfry-nip-tests && rm -f coverage.out coverage.html

help-test:
	@echo "Strfry Docker & Test Commands:"
	@echo ""
	@echo "Docker Management:"
	@echo "  make docker-build   - Build Docker image"
	@echo "  make docker-up      - Start relay container"
	@echo "  make docker-down    - Stop and remove container"
	@echo "  make docker-restart - Restart relay with fresh state"
	@echo "  make docker-logs    - Follow container logs"
	@echo ""
	@echo "Testing:"
	@echo "  make test-setup     - First-time setup (build + start)"
	@echo "  make test           - Run all tests (auto-restarts relay)"
	@echo "  make test-verbose   - Run tests with verbose output"
	@echo "  make test-short     - Run quick tests only"
	@echo "  make test-security  - Run security tests"
	@echo "  make test-ratelimit - Run rate limit tests"
	@echo "  make test-query     - Run query tests"
	@echo "  make test-load      - Run load tests"
	@echo "  make test-coverage  - Generate coverage report"
	@echo "  make test-clean     - Clean test artifacts"
	@echo ""
	@echo "Build (native):"
	@echo "  make                - Build strfry binary"
	@echo "  make clean          - Clean build artifacts"
	@echo ""
