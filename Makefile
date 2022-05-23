CERTKEY_RECIPES := $(shell find testbed -name 'certkey.recipe')
KEYS := $(addsuffix key.pem,$(dir $(CERTKEY_RECIPES)))

export TCPLB_TESTBED_ROOT=$(shell pwd)/testbed

all:	test build
.PHONY: all

build:
	CGO_ENABLED=0 go build -o dist/tcplb ./cmd/tcplb
.PHONY: all

test:	libtest servertest
.PHONY: test

servertest:	allkeys
	go test -vet=all -race -v ./cmd/...
.PHONY: servertest

libtest:
	go test -vet=all -race -v ./lib/...
.PHONY: libtest

containerised_build:
	./builder/builder.sh
.PHONY: containerised_build

allkeys:	$(KEYS)
.PHONY: allkeys

tool/generate_cert:	tool/generate_cert.go
	go build -o $@ ./$<

%/key.pem:		tool/generate_cert %/certkey.recipe
	$< -common-name $* -out-key $@ -out-cert $(addsuffix cert.pem,$(dir $@)) $(shell cat $(word 2,$^))
	# display the cert contents
	openssl x509 -inform pem -in $(addsuffix cert.pem,$(dir $@)) -noout -text
