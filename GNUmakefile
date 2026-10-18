sonda_plugins = \
	sonda-etl-ptnop-archive \
	sonda-etl-ptnop-prom \
	sonda-measure-ptnop \
	sonda-scan \
	sonda-serve-ptnop \
	sonda-serve-spool \
	sonda-spool-gc \
	sonda-spool-run

.PHONY: all
all: sonda $(sonda_plugins)

.PHONY: clean
clean:
	rm -f sonda $(sonda_plugins)

.PHONY: sonda
sonda:
	go build -v .

.PHONY: $(sonda_plugins)
$(sonda_plugins):
	go build -v ./cmd/$@
