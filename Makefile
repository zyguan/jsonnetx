GO := CGO_ENABLED=0 go

.PHONY: FORCE build clean

build: bin/jx

clean:
	rm -rf bin/

bin/jx: FORCE
	$(GO) build -o $@ ./cmd/jx
