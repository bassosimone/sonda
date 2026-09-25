.PHONY: all
all: \
	sonda \
	sonda-qoe

GOFLAGS ?= -v -ldflags '-s -w' -tags netgo

.PHONY: sonda
sonda:
	go build $(GOFLAGS) -o ./usr/libexec/sonda/sonda .

.PHONY: sonda-qoe
sonda-qoe:
	go build $(GOFLAGS) -o ./usr/libexec/sonda/sonda-qoe ./cmd/sonda-qoe
