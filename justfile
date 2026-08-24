set shell := ["bash", "-uc"]

golangci := "go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.13.1"
govulncheck := "go run golang.org/x/vuln/cmd/govulncheck@latest"
gosec := "go run github.com/securego/gosec/v2/cmd/gosec@latest"
gitleaks := "go run github.com/zricethezav/gitleaks/v8@latest"

default:
    @just --list --unsorted

check: format test security

build:
    mkdir -p bin
    CGO_ENABLED=0 go build -trimpath -o bin/mqtt2prometheus ./cmd/mqtt2prometheus

run *args: build
    MQTT2PROMETHEUS_CONFIG_DIR=config ./bin/mqtt2prometheus run {{ args }}

verify: build
    MQTT2PROMETHEUS_CONFIG_DIR=config \
    MQTT_BROKER="${MQTT_BROKER:-tcp://mqtt.example:1883}" \
    MQTT_USERNAME="${MQTT_USERNAME:-verify}" \
    MQTT_PASSWORD="${MQTT_PASSWORD:-verify}" \
    ./bin/mqtt2prometheus verify

format:
    test -z "$(gofmt -l . | tee /dev/stderr)"
    go vet ./...
    {{ golangci }} run

lint:
    gofmt -w .
    {{ golangci }} run --fix

test: test-unit test-feature

test-unit:
    go test -race -coverprofile=cover.out -covermode=atomic ./internal/...
    @go tool cover -func=cover.out | awk '/^total:/ {gsub("%","",$3); if ($3+0 < 100) {printf "coverage %s%%, need 100%%\n", $3; exit 1}; print "coverage 100%"}'

test-feature:
    go test -race -tags feature -count=1 ./tests/...

security:
    go mod verify
    {{ govulncheck }} ./...
    {{ gosec }} -quiet ./...
    {{ gitleaks }} dir . --no-banner

docker-build:
    docker build -t mqtt2prometheus:local .
