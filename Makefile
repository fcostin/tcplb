CERTKEY_RECIPES := $(shell find testbed -name 'certkey.recipe')
KEYS := $(addsuffix key.pem,$(dir $(CERTKEY_RECIPES)))

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

allkeys:	$(KEYS)
.PHONY: allkeys

%/key.pem:		tool/generate_cert %/certkey.recipe
	$< -common-name $* -out-key $@ -out-cert $(addsuffix cert.pem,$(dir $@)) $(shell cat $(word 2,$^))
	# display the cert contents
	openssl x509 -inform pem -in $(addsuffix cert.pem,$(dir $@)) -noout -text
