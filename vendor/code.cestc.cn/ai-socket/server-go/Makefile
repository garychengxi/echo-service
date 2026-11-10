# init project path
HOMEDIR := $(shell pwd)
OUTDIR  := $(HOMEDIR)/output
# server use server ended name
SVCNAME := server-server
# CLI use short name
CLINAME := server

# init command params
GOROOT  := $(shell go env GOROOT)
GO      := $(GOROOT)/bin/go
GOPATH  := $(shell $(GO) env GOPATH)
GOMOD   := $(GO) mod
GOBUILD := $(GO) build
GOTEST  := $(GO) test -gcflags="-N -l"
GOPKGS  := $$($(GO) list ./...| grep -vE "vendor")

# test cover files
COVPROF := $(HOMEDIR)/covprof.out  # coverage profile
COVFUNC := $(HOMEDIR)/covfunc.txt  # coverage profile information for each function
COVHTML := $(HOMEDIR)/covhtml.html # HTML representation of coverage profile

all: clean prepare compile package

prepare: check-var set-env

check-var:
	$(info GO_1_21_HOME: $(GO_1_21_HOME))
	$(info GOROOT: $(GOROOT))
	$(info GOPATH: $(GOPATH))

set-env:
	$(GO) env -w GO111MODULE=on
	$(GO) env -w GONOSUMDB=\*

compile: build

build:
	$(GOMOD) tidy
	(cd $(HOMEDIR)/examples/example-service1 && $(GOMOD) tidy && make -f Makefile)
	(cd $(HOMEDIR)/examples/example-combine-service && $(GOMOD) tidy && make -f Makefile)

# enable CGO for sqlite integration tests
test:
	CGO_ENABLED=1 $(GOTEST) -v -cover $(GOPKGS)

package:
	mkdir -p $(OUTDIR)/example-service1
	cp -r $(HOMEDIR)/examples/example-service1/output $(OUTDIR)/example-service1

	mkdir -p $(OUTDIR)/example-combine-service
	cp -r $(HOMEDIR)/examples/example-combine-service/output $(OUTDIR)/example-combine-service

clean:
	$(GO) clean
	rm -rf $(OUTDIR)

# avoid filename conflict and speed up build
.PHONY: all compile build test package clean

