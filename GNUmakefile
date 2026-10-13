sonda_plugins = \
	sonda-etl-ptnop-archive \
	sonda-etl-ptnop-prom \
	sonda-inetd-ptnop \
	sonda-measure-ptnop \
	sonda-scan \
	sonda-spool

.PHONY: all
all: sonda $(sonda_plugins)

.PHONY: clean
clean:
	cd ./dist/portable/usr/libexec/sonda && rm -f sonda $(sonda_plugins)

.PHONY: sonda
sonda:
	CGO_ENABLED=0 go build -ldflags '-s -w' -o ./dist/portable/usr/libexec/sonda -v .

.PHONY: $(sonda_plugins)
$(sonda_plugins):
	CGO_ENABLED=0 go build -ldflags '-s -w' -o ./dist/portable/usr/libexec/sonda -v ./cmd/$@
