PKG    = github.com/sapcc/castellum
PREFIX = /usr

all: build/castellum

# NOTE: This repo uses Go modules, and uses a synthetic GOPATH at
# $(CURDIR)/.gopath that is only used for the build cache. $GOPATH/src/ is
# empty.
GO            = GOPATH=$(CURDIR)/.gopath GOBIN=$(CURDIR)/build go
GO_BUILDFLAGS =
GO_LDFLAGS    = -s -w

build/castellum: FORCE
	$(GO) install $(GO_BUILDFLAGS) -ldflags '$(GO_LDFLAGS)' '$(PKG)'

install: FORCE all
	install -D -m 0755 build/castellum "$(DESTDIR)$(PREFIX)/bin/castellum"

# convenience target for developers: `make run-api/run-collector/run-worker`
# runs one of the components, with environment options sourced from $PWD/.env
run-%: build/castellum
	set -euo pipefail && source ./.env && ./build/castellum $*

vendor: FORCE
	$(GO) mod vendor

.PHONY: FORCE
