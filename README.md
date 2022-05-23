tcplb
=====

### What

TCP load balancer

### Table of Contents

1. [Building Locally from Source](#building-locally-from-source)
2. [Example of forwarding insecure TCP connection](#example-of-forwarding-insecure-tcp-connection)
3. [Example of forwarding mTLS connection](#example-of-forwarding-mtls-connection)
4. [Containerised Build](#containerised-build)
5. [Further Reading](#further-reading)

### Building Locally from Source

Ensure your development environment has Go and `make` installed.
The version of Go required is shown in [`go.mod`](./go.mod).

Then, from a checkout of this repository, run

```
make all
```

If the tests and build succeed, the `tcplb` server binary will
be written to `dist/tcplb`.


### Example of forwarding insecure TCP connection

#### terminal #1, using tcplb repo checkout as working dir

Build the `tcplb` server binary:
```
make build
```

Start `tcplb`, forwarding to `example.com` port 80:
```
./dist/tcplb \
    -authzd-clients anonymous \
    -insecure-accept-tcp=true \ 
    -listen-address 127.0.0.1:4444 \
    -upstreams example.com:80 \
```
#### terminal #2, from somewhere on the same localhost

Connect to `tcplb` using `telnet`:
```
telnet 127.0.0.1 4444
```
If telnet establishes a TCP connection and grants you a prompt, you may type
a minimal HTTP request:
```
GET / HTTP/1.1
host: example.com

```
note:
- sending the blank line is necessary.
- to escape telnet, hit `ctrl ]` then type `close` and hit enter.

### Example of forwarding mTLS connection

#### terminal #1, using tcplb repo checkout as working dir

Build the `tcplb` server binary:
```
make build
```

Generate some test certificate and private keys for client and server:
```
make allkeys
```

Start `tcplb`, forwarding to `example.com` port 80:
```
./dist/tcplb \
    -authzd-clients client-strong \
	-ca-root-file testbed/client-strong/cert.pem \
	-cert-file testbed/tcplb-server-strong/cert.pem \
	-key-file testbed/tcplb-server-strong/key.pem \
	-listen-address 127.0.0.1:4321
	-upstreams example.com:80
```

#### terminal #2, from same working dir

Perform an example HTTPS query through `tcplb` using `curl`:
```
curl \
	--insecure -v \
	-H "Connection: close" \
	-H "host: example.com" \
	--key testbed/client-strong/key.pem \
	--cert testbed/client-strong/cert.pem \
	--cacert testbed/tcplb-server-strong/cert.pem \
	https://127.0.0.1:4321
```

FIXME: `--insecure` when invoking curl is necessary as the DNS name in
the generated server cert does not match `127.0.0.1`


### Containerised build

Ensure your development environment has Docker, `make`, `bash`.

Then, from a checkout of this repository, run

```
make containerised_build
```

If the tests and build succeed, the `tcplb` server binary will
be written to `dist/tcplb`.

### Further Reading

* [Design Doc](docs/DESIGN.md)