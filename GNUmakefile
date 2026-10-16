sonda_plugins = \
	sonda-etl-ptnop-archive \
	sonda-etl-ptnop-prom \
	sonda-measure-ptnop \
	sonda-scan \
	sonda-serve-ptnop \
	sonda-spool-gc \
	sonda-spool-run

.PHONY: all
all: sonda $(sonda_plugins)

exec_dir = ./dist/portable/libexec

.PHONY: clean
clean:
	cd $(exec_dir) && rm -f sonda $(sonda_plugins)

.PHONY: sonda
sonda:
	go build -v -o $(exec_dir)/sonda .

.PHONY: $(sonda_plugins)
$(sonda_plugins):
	go build -v -o $(exec_dir)/$@ ./cmd/$@
