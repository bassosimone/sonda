.PHONY: all
all: \
	sonda \
	sonda-measure \
	sonda-metrics \
	sonda-scan \
	sonda-spool

GOFLAGS ?= -v -ldflags '-s -w' -tags netgo

.PHONY: sonda
sonda:
	go build $(GOFLAGS) -o ./usr/libexec/sonda/sonda .

.PHONY: sonda-measure
sonda-measure:
	go build $(GOFLAGS) -o ./usr/libexec/sonda/sonda-measure ./cmd/sonda-measure

.PHONY: sonda-metrics
sonda-metrics:
	go build $(GOFLAGS) -o ./usr/libexec/sonda/sonda-metrics ./cmd/sonda-metrics

.PHONY: sonda-scan
sonda-scan:
	go build $(GOFLAGS) -o ./usr/libexec/sonda/sonda-scan ./cmd/sonda-scan

.PHONY: sonda-spool
sonda-spool:
	go build $(GOFLAGS) -o ./usr/libexec/sonda/sonda-spool ./cmd/sonda-spool
