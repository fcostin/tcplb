## what

a primitive TCP load balancer design

## why

there is no why

## details

TODO explain what this doc will explain
-   key concepts?
-   protocol level used for forwarding, and design consequences
-   general flow of forwarding?
-   how connections are torn down?
-   security details
-   other tradeoffs
-   high availability considerations

### key concepts client, server and upstream

client is
server is
client is

### forwarding flow

1. server (load balancer) begins listening for client connections
2. client opens tls connection to server
3. client & server negotiate TLS handshake & perform mutual authentication.
4. server enforces a client rate limit based on identity of authenticated client, if identity of client has too many connections, server closes this connection
5. server evaluates authorisation policy based on identity of authenticated client to determine which upstreams the client may be forwarded to. If the client is not authorised to be forwarded to any upstreams, the server closes the connection to the client
6. server narrows the set of upstreams to the subset of upstreams it currently believes to be healthy. If no upstreams are believed to be healthy, the server closes the connection to the client
7. if there are multiple healthy upstreams that the client is authorised to use, the server selects one using the configured forwarding prioritisation rule (e.g. priorise an upstream with the fewest forwarding conections from the server). any ties are broken in an implementation-defined way.
8. the server attempts to establish a TCP connection with the selected upstream. if this fails, the server closes the connection to the client
9. the server begins forwarding data between the client TLS connection and the upstream TCP connection.

### authorisation layer

The job of the server's authorisation layer is to decide which (if any) upstreams an authenticated client may be forwarded to.

Let
- `c` denote an authenticated client
- `Ids(c)` denote the set of identities associated with a client `c`
- `Groups(i)` denote the set of client groups that an individual SAN identity `i` belongs to
- `UGroups(g)` denote the set of upstream groups that a client group `g` is authorised to forward to
- `Upstreams(u)` denote the set of upstreams that are members of the upstream group `u`
    
Let `AuthorisedUpstreams(c)` denote the set of upstreams that an authenticated client `c` is authorised to access.

Then, `AuthorisedUpstreams(c)` is defined as:

AuthorisedUpstreams(c) := union_{i ~ Ids(c)} union_{g ~ Groups(i)} union_{u ~ UGroups(b)} Upstreams(u)

Clients will be identified for authorisation purposes using Subject Alternative Name (SAN) extention to the client's x509 certificate.
This means that `Ids(c)` is defined as the set of SAN identities bound to the client's public key through the client's x509 certificate.

The server must validate the certification path from a trust anchor to the client's certificate during authentication, prior to the
authorisation layer. Recall that server has no responsibility to verify these subject alternative names:

> Because the subject alternative name is considered to be definitively bound to the public key, all parts of the subject alternative name MUST be verified by the CA.
see rfc5280.

At a minimum, a server must support reading the following kinds of SANs out of the client's x509 cert to define `Ids(c)`:
-   email addresses
-   DNS names
A server may choose to implement support additional kinds of SANs defined in [rfc5280](https://datatracker.ietf.org/doc/html/rfc5280#section-4.2.1.6).

Server implementations are encouraged to consider normalisation of SAN data when implementing
authoration component.

A single x509 client certificate for an authenticated client may bind 0, 1 or many SANs to the
client's public key. The set of upstreams that a client is authorised to be forwarded to is
defined as the union, over the SANs bound to the client's certificate, of the sets of upstreams
authorised for each SAN.

In the trivial case where a client certificate binds 0 SANs, then the set of upstreams that the
client is authorised to be forwarded to will be the empty set, i.e., the client will not be
authorised to be forwarded to any upstream.

The remaining pieces of input data for authorisation, `Groups(.)`, `UGroups(.)` and `Upstreams(.)`,
may be sourced by the server in an implementation-defined way. Proof-of-concept servers may
choose to embed this data directly into application code. More sophisticated servers may choose
to load this data from an external configuration file or reading it from some external integration.

Rejected alternatives:

-   embed claims such as client group membership in the client certificate. This design would couple
    authorisation decisions with identifying and issuing certificates to clients, and make it difficult to
    adjust or revoke group membership, without a reliable mechanism for issuing short-lived certificates

-   embed full executable client-specific routing policies in the client certificate. This alternative
    is amusing but has the downsides of coupling together many unrelated concerns, and would require
    servers to embed script interpreters (javascript a-la web browser http proxy PAC? BPF bytecode?),
    define a common protocol for APIs exposed to routing scripts by the server, and concern themselves
    with client-specific routing scripts that might crash, fail to halt, contain malware, etc. Arguably
    the root CA could be lumped with the responsibility of verifing that client-specific routing scripts
    are sensible, malware free, and likely to halt.

