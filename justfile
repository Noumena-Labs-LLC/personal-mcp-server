set shell := ["bash", "-eu", "-o", "pipefail", "-c"]
export GOWORK := "off"

# Go toolchain pin: the one value to edit when changing toolchains. Recipes run
# under it regardless of the local GOTOOLCHAIN setting; `just toolchain-sync`
# writes it into go.mod, which CI's setup-go reads.
GO_TOOLCHAIN := "go1.27.2"
export GOTOOLCHAIN := GO_TOOLCHAIN

APP := "personal-mcp-server"
PKG := "./cmd/personal-mcp-server"
CONFIG := "configs/example.toml"
VERSION := `cat VERSION`
# Developer tools live in the shared ~/go/bin, not a repo-local copy, so the machine holds one
# install of each. `just tools` installs the pinned versions there.
TOOLS_BIN := env_var('HOME') + "/go/bin"
STATICCHECK := TOOLS_BIN + "/staticcheck"
GOLANGCI_LINT := TOOLS_BIN + "/golangci-lint"
GOVULNCHECK := TOOLS_BIN + "/govulncheck"
STATICCHECK_VERSION := "2026.2.2"
GOLANGCI_LINT_VERSION := "v2.14.0"
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

# Install the pinned developer tools into the shared ~/go/bin (replacing any other version there).
# The lint and vulnerability targets run this automatically when a tool is missing.
tools:
    mkdir -p {{TOOLS_BIN}}
    GOBIN="{{TOOLS_BIN}}" go install honnef.co/go/tools/cmd/staticcheck@{{STATICCHECK_VERSION}}
    GOBIN="{{TOOLS_BIN}}" go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@{{GOLANGCI_LINT_VERSION}}
    GOBIN="{{TOOLS_BIN}}" go install golang.org/x/vuln/cmd/govulncheck@{{GOVULNCHECK_VERSION}}

toolchain-sync:
    go mod edit -toolchain={{GO_TOOLCHAIN}}

toolchain-check:
    grep -qx 'toolchain {{GO_TOOLCHAIN}}' go.mod || (echo "go.mod toolchain does not match GO_TOOLCHAIN={{GO_TOOLCHAIN}}; run 'just toolchain-sync'" >&2; exit 1)

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

ci: toolchain-check lint-check test-race integration-test smoke-test govulncheck

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

# GNU tar renames archive paths with --transform; macOS's bsdtar has no --transform and uses -s.
dist:
    mkdir -p dist
    if tar --version 2>/dev/null | grep -q 'GNU tar'; then \
      rename=(--transform 's,^.,{{APP}}-v{{VERSION}},'); \
    else \
      rename=(-s ',^\.,{{APP}}-v{{VERSION}},'); \
    fi; \
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
      -czf dist/{{APP}}-v{{VERSION}}.tar.gz "${rename[@]}" .
    (cd dist && shasum -a 256 {{APP}}-v{{VERSION}}.tar.gz > {{APP}}-v{{VERSION}}.tar.gz.sha256)

clean:
    rm -rf bin dist build coverage.out coverage.html
    find . -name '__pycache__' -type d -prune -exec rm -rf {} +
    find . -name '*.pyc' -delete
    find . -name '.DS_Store' -delete
