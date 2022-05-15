tcplb
=====

### What

TCP load balancer

### Table of Contents

1. [Building Locally from Source](#building-locally-from-source)
2. [Containerised Build](#containerised-build)
3. [Further Reading](#further-reading)

### Building Locally from Source

Ensure your development environment has Go and `make` installed.
The version of Go required is shown in [`go.mod`](./go.mod).

Then, from a checkout of this repository, run

```
make all
```

If the tests and build succeed, the `tcplb` server binary will
be written to `dist/tcplb`.

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