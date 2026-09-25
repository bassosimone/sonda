.PHONY: all
all: sonda sonda-qoe

.PHONY: sonda
sonda:
	go build -v -o ./usr/libexec/sonda/sonda .

.PHONY: sonda-qoe
sonda-qoe:
	go build -v -o ./usr/libexec/sonda/sonda-qoe ./cmd/sonda-qoe
