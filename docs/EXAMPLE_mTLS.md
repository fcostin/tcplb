
### terminal #1, using tcplb repo checkout as working dir


build the `tcplb` server binary
```
make build
```

generate some test certificate and private keys for client and server

```
make allcerts
```

start `tcplb`, forwarding to `example.com` port 80:

```
./dist/tcplb \
	-ca-root-file testbed/client-strong/cert.pem \
	-cert-file testbed/tcplb-server-strong/cert.pem \
	-key-file testbed/tcplb-server-strong/key.pem \
	-upstreams example.com:80
```

### terminal #2, from same working dir

perform an example HTTPS query through `tcplb`

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

FIXME `--insecure` when invoking curl is necessary as the DNS name in generated server cert does not match `127.0.0.1`
