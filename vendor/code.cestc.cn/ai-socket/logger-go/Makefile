# init project path
HOMEDIR := $(shell pwd)
OUTDIR  := $(HOMEDIR)/output

# init command params
GOROOT  := $(shell go env GOROOT))
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

# make, make all
all: prepare package

# set proxy env
set-env:
	$(GO) env -w GO111MODULE=on
	$(GO) env -w GOPROXY=https://goproxy.cn,direct
	$(GO) env -w GONOSUMDB=\*

#make prepare, download dependencies
prepare: gomod

gomod: set-env
	$(GOMOD) download

# make test, test your code
test: prepare test-case
test-case:
	$(GOTEST) -v -cover $(GOPKGS)

# make package
package: package-bin
package-bin:
	mkdir -p $(OUTDIR)

# make clean
clean:
	$(GO) clean
	rm -rf $(OUTDIR)
	rm -rf $(GOPATH)/pkg/darwin_amd64

# avoid filename conflict and speed up build
.PHONY: all prepare test package clean
