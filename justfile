# Tasks for developing the sandbox CLI. Using the sandbox itself: see README.

default:
    @just --list

# Build ./bin/sandbox
build:
    go build -o bin/sandbox ./cmd/sandbox

# Install to ~/.local/bin/sandbox
install:
    go build -o {{ env('HOME') }}/.local/bin/sandbox ./cmd/sandbox
    @echo "installed {{ env('HOME') }}/.local/bin/sandbox"

# Unit tests (no Docker needed)
test:
    go test ./...

# End-to-end tests against Docker and a real SOCKS proxy (expected_egress: proxy exit IP or CIDR)
test-integration proxy="10.0.0.1:1080" expected_egress="":
    SANDBOX_IT_PROXY={{ proxy }} SANDBOX_IT_EXPECTED_EGRESS={{ expected_egress }} go test -tags integration -count=1 -v ./test/integration/

# Go, shell and Dockerfile linters
lint:
    golangci-lint run ./...
    golangci-lint run --build-tags integration ./test/...
    shellcheck assets/gateway/entrypoint.sh assets/image/*.sh
    docker run --rm -i -v "$PWD/.hadolint.yaml:/.config/hadolint.yaml:ro" hadolint/hadolint:v2.12.0 < assets/gateway/Dockerfile
    docker run --rm -i -v "$PWD/.hadolint.yaml:/.config/hadolint.yaml:ro" hadolint/hadolint:v2.12.0 < assets/image/Dockerfile

# Leak checks against your live sandbox
check *args:
    go run ./cmd/sandbox check {{ args }}

fmt:
    gofmt -w .
