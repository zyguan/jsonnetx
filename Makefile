GO := CGO_ENABLED=0 go

.PHONY: FORCE build clean

build: bin/jx bin/jxs

clean:
	rm -rf bin/

bin/jx: FORCE
	$(GO) build -o $@ ./cmd/jx

bin/jxs: FORCE
	$(GO) build -o $@ ./cmd/jxs
