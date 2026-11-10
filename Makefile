GO ?= go
OUTDIR := output
SVCNAME := echoservice-server
CLINAME := echoservice
APP_VERSION ?= 0.1.0-dev
GIT_COMMIT := $(shell git rev-parse --verify HEAD 2>/dev/null || echo unknown)
BUILD_DATE := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
VERSION_VAR := code.cestc.cn/ai-socket/server-go/version
LD_FLAGS := -X $(VERSION_VAR).AppVersion=$(APP_VERSION) -X $(VERSION_VAR).GitCommit=$(GIT_COMMIT) -X $(VERSION_VAR).BuildDate=$(BUILD_DATE)

.DEFAULT_GOAL := build
.PHONY: all tidy fmt fmt-check typecheck vet test test-race test-echo check build compile cross-build package package-docker run smoke clean
all: check package
tidy:
	$(GO) mod tidy
fmt:
	gofmt -w cmd internal service sdk internalsdk
fmt-check:
	@test -z "$$(gofmt -l cmd internal service sdk internalsdk)" || (gofmt -l cmd internal service sdk internalsdk; exit 1)
typecheck:
	$(GO) test -run '^$$' ./...
vet:
	$(GO) vet ./...
test:
	$(GO) test ./...
test-race:
	CGO_ENABLED=1 $(GO) test -race ./...
test-echo:
	@test -n "$(ECHO_TEST_BINARY)" || (echo 'set ECHO_TEST_BINARY=/absolute/path/to/echo'; exit 1)
	ECHO_TEST_BINARY=$(ECHO_TEST_BINARY) $(GO) test ./internal/app/control -run TestRealEchoServer -count=1
check: fmt-check typecheck vet test
compile: build
build:
	mkdir -p $(OUTDIR)/bin
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags '$(LD_FLAGS)' -o $(OUTDIR)/bin/$(SVCNAME) ./cmd/server
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags '$(LD_FLAGS)' -o $(OUTDIR)/bin/$(CLINAME) ./cmd/cli
cross-build:
	@for arch in amd64 arm64; do 		CGO_ENABLED=0 GOOS=linux GOARCH=$$arch $(GO) build -trimpath -ldflags '$(LD_FLAGS)' -o $(OUTDIR)/bin/$(SVCNAME)-linux-$$arch ./cmd/server || exit 1; 		CGO_ENABLED=0 GOOS=linux GOARCH=$$arch $(GO) build -trimpath -ldflags '$(LD_FLAGS)' -o $(OUTDIR)/bin/$(CLINAME)-linux-$$arch ./cmd/cli || exit 1; 	done
package: build
	mkdir -p $(OUTDIR)/conf/server $(OUTDIR)/doc $(OUTDIR)/deploy
	cp conf/server/config.toml $(OUTDIR)/conf/server/
	cp README.md doc/architecture.md doc/api-current.md doc/api-plan-v1.md doc/frontend-api-v1.md $(OUTDIR)/doc/
	cp -R deploy/. $(OUTDIR)/deploy/
package-docker:
	sh scripts/package-docker.sh
run:
	$(GO) run ./cmd/server
smoke: build
	python3 scripts/smoke.py
clean:
	rm -rf $(OUTDIR)
