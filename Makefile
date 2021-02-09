GO := CGO_ENABLED=0 go
IMAGE_TAG ?= latest

.PHONY: FORCE build clean

build: bin/jx bin/jxs

image:
	docker build -t zyguan/jsonnetx:$(IMAGE_TAG) .

clean:
	rm -rf bin/

bin/jx: FORCE
	$(GO) build -o $@ ./cmd/jx

bin/jxs: FORCE
	$(GO) build -o $@ ./cmd/jxs
