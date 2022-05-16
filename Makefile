all:	test build
.PHONY: all

build:
	CGO_ENABLED=0 go build -o dist/tcplb ./cmd/tcplb
.PHONY: all

test:
	go test -vet=all -race ./...
.PHONY: test

containerised_build:
	./builder/builder.sh
.PHONY: containerised_build