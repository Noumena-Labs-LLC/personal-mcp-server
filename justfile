set shell := ["bash", "-eu", "-o", "pipefail", "-c"]

APP := "personal-mcp-server"
PKG := "./cmd/personal-mcp-server"
CONFIG := "configs/example.toml"
VERSION := `cat VERSION`
TOOLS_BIN := ".tools/bin"
STATICCHECK := ".tools/bin/staticcheck"
GOLANGCI_LINT := ".tools/bin/golangci-lint"
GOVULNCHECK := ".tools/bin/govulncheck"
STATICCHECK_VERSION := "2026.1"
GOLANGCI_LINT_VERSION := "v2.12.1"
GOVULNCHECK_VERSION := "latest"

_default:
    just --list

fmt:
    gofmt -w ./cmd ./internal

fmt-check:
    test -z "$(gofmt -l ./cmd ./internal)"

test:
    go test ./...

test-race:
    go test -race ./...


integration-test:
    go test ./cmd/personal-mcp-server -run 'Integration'

smoke-test:
    go test ./cmd/personal-mcp-server -run 'Smoke'

stress-test:
    go test -race -count=1 ./cmd/personal-mcp-server -run 'Stress' -timeout 10m


coverage:
    go test ./... -cover

coverage-profile:
    go test ./... -coverprofile=coverage.out
    go tool cover -func=coverage.out

coverage-html:
    go test ./... -coverprofile=coverage.out
    go tool cover -html=coverage.out

vet:
    go vet ./...

# Install developer tools into the repo-local .tools/bin directory.
# This keeps lint-check usable on a fresh checkout without requiring global installs.
tools:
    mkdir -p {{TOOLS_BIN}}
    GOBIN="$PWD/{{TOOLS_BIN}}" go install honnef.co/go/tools/cmd/staticcheck@{{STATICCHECK_VERSION}}
    GOBIN="$PWD/{{TOOLS_BIN}}" go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@{{GOLANGCI_LINT_VERSION}}
    GOBIN="$PWD/{{TOOLS_BIN}}" go install golang.org/x/vuln/cmd/govulncheck@{{GOVULNCHECK_VERSION}}

tools-check:
    test -x {{STATICCHECK}} || (echo "missing {{STATICCHECK}}; run 'just tools' or rerun this target to bootstrap" >&2; exit 1)
    test -x {{GOLANGCI_LINT}} || (echo "missing {{GOLANGCI_LINT}}; run 'just tools' or rerun this target to bootstrap" >&2; exit 1)
    test -x {{GOVULNCHECK}} || (echo "missing {{GOVULNCHECK}}; run 'just tools' or rerun this target to bootstrap" >&2; exit 1)

staticcheck:
    test -x {{STATICCHECK}} || just tools
    {{STATICCHECK}} ./...

golangci-lint:
    test -x {{GOLANGCI_LINT}} || just tools
    {{GOLANGCI_LINT}} run ./...

lint: vet staticcheck golangci-lint

lint-check:
    just fmt-check
    go vet ./...
    test -x {{STATICCHECK}} || just tools
    {{STATICCHECK}} ./...
    test -x {{GOLANGCI_LINT}} || just tools
    {{GOLANGCI_LINT}} run ./...

check: fmt vet test lint

ci: lint-check test-race integration-test smoke-test govulncheck

govulncheck:
    test -x {{GOVULNCHECK}} || just tools
    {{GOVULNCHECK}} -show verbose ./...

build:
    mkdir -p bin
    go build -trimpath -ldflags="-s -w" -o bin/{{APP}} {{PKG}}

install-user: build
    root="${PERSONAL_MCP_ROOT:-$HOME/.personal-mcp-server}";     mkdir -p "$root/bin";     cp "bin/{{APP}}" "$root/bin/{{APP}}";     chmod +x "$root/bin/{{APP}}";     "$root/bin/{{APP}}" version

init:
    go run {{PKG}} init --config /tmp/personal-mcp-server-config.toml --root . --generate-token --force

config-validate:
    go run {{PKG}} config validate --config {{CONFIG}}

doctor:
    go run {{PKG}} doctor --config {{CONFIG}}

run:
    go run {{PKG}} serve --config {{CONFIG}}

curl-tools:
    curl -sS http://127.0.0.1:3929/mcp \
      -H "Authorization: Bearer $$PERSONAL_MCP_TOKEN" \
      -H "Accept: application/json, text/event-stream" \
      -H "Content-Type: application/json" \
      -d '{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}'

curl-prompts:
    curl -sS http://127.0.0.1:3929/mcp \
      -H "Authorization: Bearer $$PERSONAL_MCP_TOKEN" \
      -H "Accept: application/json, text/event-stream" \
      -H "Content-Type: application/json" \
      -d '{"jsonrpc":"2.0","id":1,"method":"prompts/list","params":{}}'

dist:
    mkdir -p dist
    tar \
      --exclude='./.git' \
      --exclude='./.tools' \
      --exclude='./bin' \
      --exclude='./dist' \
      --exclude='./build' \
      --exclude='./coverage.out' \
      --exclude='./coverage.html' \
      --exclude='./__pycache__' \
      --exclude='*/__pycache__' \
      --exclude='*.pyc' \
      --exclude='.DS_Store' \
      -czf dist/{{APP}}-v{{VERSION}}.tar.gz --transform 's,^.,{{APP}}-v{{VERSION}},' .
    (cd dist && shasum -a 256 {{APP}}-v{{VERSION}}.tar.gz > {{APP}}-v{{VERSION}}.tar.gz.sha256)

clean:
    rm -rf bin dist build coverage.out coverage.html
    find . -name '__pycache__' -type d -prune -exec rm -rf {} +
    find . -name '*.pyc' -delete
    find . -name '.DS_Store' -delete

clean-tools:
    rm -rf .tools
