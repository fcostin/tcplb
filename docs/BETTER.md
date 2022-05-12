## what

TCP load balancer

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

### Protocols supported for forwarding

The server will accept TLS connections from clients and forward them over 
TCP to upstreams.

The server assumes that the clients and upstreams have agreed on a common 
application-level protocol, but it does not know or need to know what that
protocol is. This has the advantage of broadening the range of situations 
where the load balancer can be applied, and reduces the complexity and 
effort of implementation but restricts the server's ability to perform
very well in any particular situation, compared to specialised load 
balancers that include application protocol specific design optimisations.

### Forwarding flow

1. Server begins listening for client TLS connections.
2. Client opens TLS connection to server.
3. Client & server negotiate TLS handshake & perform mutual authentication.
4. Server enforces a client rate limit based on identity of authenticated 
   client. If identity has too many connections, server closes this connection.
5. Server evaluates authorisation policy based on identity of authenticated 
   client to determine which upstreams the client may be forwarded to. If 
   the client is not authorised to be forwarded to any upstreams, the server 
   closes the connection to the client.
6. Server narrows the set of upstreams to the subset of upstreams it 
   currently believes to be healthy. If no upstreams are believed to be 
   healthy, the server closes the connection to the client.
7. If there are multiple healthy upstreams that the client is authorised to 
   use, the server selects one using the configured forwarding 
   prioritisation rule (e.g. least forwarded connections). Ties may be 
   broken arbitrarily.
8. The server attempts to establish a TCP connection with the selected 
   upstream. If this fails, the server closes the connection to the client.
9. The server begins forwarding data between the client TLS connection and 
   the upstream TCP connection.

Authorisation is enforced at the earliest possible stage after authentication
and rate limiting, to reduce risk of server defects unintentionally granting 
access to unauthorised clients, or allowing unauthorised clients to consume 
resources of other server subsystems.

Steps 4,5,6 and 7 could be evaluated locally within the server process and do 
not depend on making calls to external services over the network. These steps
can be evaluated quickly. Step 8, establishing a connection with an upstream, 
could add significant latency. See the performance section.

### TLS

There is a tradeoff between security and supporting backwards compatibility 
with clients that may only implement known-vulnerable protocols.

In ascending order of security and decreasing compatibility, we have:

1. server is willing to entertain TLS 1.0 connections, and a wide variety of 
   cipher suites
2. server accepts TLS 1.2 or TLS 1.3 only, but refuses to use particularly 
   broken cipher suites
3. server accepts TLS 1.3 only, limited to the
   [set of cipher suites](https://datatracker.ietf.org/doc/html/rfc8446#section-9.1)
   that the TLS 1.3 standard dictates TLS-compliant applications MUST or
   SHOULD support
4. custom protocol: server accepts connections with TLS 1.3 only, using a 
   fixed choice of cipher suites (`TLS_CHACHA20_POLY1305_SHA256`) and curve  
   preferences (`X25519`), and will refuse to accept anything less, as 
   recommended by [a latacora blog post from 2018](https://latacora.micro.
   blog/2018/04/03/cryptographic-right-answers.html).

For the proof-of-concept, the server & a compatible client will restrict 
themselves to option 3, TLS 1.3, as that is simple to configure, and may 
implement option 4 if time permits.

### Authentication

Authentication will be implemented using TLS with mutual authentication 
between the client and the server.

The server must be configured with a certificate and corresponding private
key. This certificate will be presented to clients who wish to negotiate TLS
connections.

The server will load trusted CA certs from the environment in the usual 
operating-system defined location. These CA certs will be used to validate 
the trust chain of the certificate presented by the client. Since the client 
certificate will be used as the basis of authentication, the server MUST NOT 
be configured to trust CA roots that are not trusted to verify client 
identities for purposes of authentication.

Similarly, the client will validate the certificate presented by the server,
and use its owned trusted CA certs to evaluate if the server is authenticated.

For a minimal demonstration, a client and a server could be equipped to present
self-signed certificates, and configured to use each other's self-signed 
certificate as a trusted CA. This would not give much operational 
flexibility compared to using a CA.

For simplicity, the proof-of-concept server will make no attempt to support:
- SNI
- revocation of certificates in the client's trust chain

### Client rate limiting

Rate limiting is implemented used the client's identity, after authentication.
This has the advantage that we know which clients we are rate limiting, and  
fits naturally with authorisation, but has the disadvantage of consuming 
server resources during the TLS handshake. An alternative could be to rate  
limit by client IP address, but this requires making assumptions on the  
relationship between IPs and clients. A production server facing high volume 
hostile network traffic may need to rate limit by IP in addition to rate  
limiting by client identity, or rely on another component to perform this job.

A single client certificate may bind multiple identities (see authorisation
section). A simple approach is to track them all and rate limit them
individually. A client is rate limited if any identity it is associated with is
rate limited. The main downside of this is additional memory if clients 
often come bearing certificates binding many identities. 

### Authorisation

The server's authorisation subsystem decides which upstreams an 
authenticated client's connection may be forwarded to.

Let
- `c` denote an authenticated client
- `Ids(c)` denote the set of identities associated with a client `c`
- `Groups(i)` denote the set of client groups that an individual SAN 
  identity `i` belongs to
- `UGroups(g)` denote the set of upstream groups that a client group `g` is 
  authorised to forward to
- `Upstreams(u)` denote the set of upstreams that are members of the 
  upstream group `u`
    
Let `AuthorisedUpstreams(c)` denote the set of upstreams that an 
authenticated client `c` is authorised to access.

Then, `AuthorisedUpstreams(c)` is defined as:

```
AuthorisedUpstreams(c) := union_{i in Ids(c)} (
                            union_{g in Groups(i)} (
                                union_{u in UGroups(g)} Upstreams(u) ) )
```

Clients will be identified for authorisation purposes using Subject Alternative
Name (SAN) extension to the client's x509 certificate.  This means that
`Ids(c)` is defined as the set of SAN identities bound to the client's public
key through the client's x509 certificate.

The server must validate the certification path from a trust anchor to the
client's certificate during authentication, prior to the authorisation 
subsystem. Recall that the server has no responsibility to verify these subject
alternative names, as per
[rfc5280](https://datatracker.ietf.org/doc/html/rfc5280#section-4.2.1.6)
that is the job of the CA.

At a minimum, a server must support reading the following kinds of SANs out of
the client's x509 cert to define `Ids(c)`:
- email addresses
- DNS names
A server may choose to implement support additional kinds of SANs defined in
[rfc5280](https://datatracker.ietf.org/doc/html/rfc5280#section-4.2.1.6).

Server implementations are encouraged to consider normalisation of SAN data
when implementing their authorisation subsystems.

A single x509 client certificate for an authenticated client may bind 0, 1 or
many SANs to the client's public key. The set of upstreams that a client is
authorised to be forwarded to is defined as the union, over the SANs bound to
the client's certificate, of the sets of upstreams authorised for each SAN.

In the trivial case where a client certificate binds 0 SANs, then the set of
upstreams that the client is authorised to be forwarded to will be the empty
set, i.e., the client will not be authorised to be forwarded to any upstream.

The remaining pieces of input data for authorisation, `Groups(.)`, `UGroups(.)`
and `Upstreams(.)`, may be sourced by the server in an implementation-defined
way. Proof-of-concept servers may choose to embed this data directly into
application code. More sophisticated servers may choose to load this data from
an external configuration file or reading it from some external integration.

Comments & alternatives:

It isn't clear if the authorisation scheme proposed above is the best idea. 
A minimal proof-of-concept implementation could be to instead implement
`AuthorisedUpstreams(c)` in terms of a lookup table
`AuthorisedUpstreamsForIdentity` mapping identities to subsets of upstreams.

It might be clearer to rework the "group" concept as "role".

We could instead embed claims such as client group membership in the client 
certificate. This design would couple authorisation decisions with 
identifying and issuing certificates to clients, and make it difficult to
adjust or revoke group membership, without a reliable mechanism for issuing
short-lived certificates to clients.

### Monitoring upstream health

### Performance

Performance is a non-goal of the proof-of-concept server.

In some settings (e.g. ecommerce), reducing connection latency is highly
valuable, and users may want the server to set up a forwarded connection
between client and upstream as quickly as possible.

Being "application-protocol-agnostic" means the server has no way of detecting
message boundaries at the application level (they may not even exist). It is
therefore not possible for the server to reuse an established TCP connection
to an upstream for forwarding between multiple client connections. This
prevents use of a connection pool technique to reduce latency.

However, a server implementation could reduce latency by speculatively
establishing fresh connections to upstreams in advance of receiving client
connections, so in some cases it is possible to immediately begin forwarding
an authenticated and authorised client. For simplicity the proof-of-concept
server will not implement this technique, but it could be added to a
production server in future without impacting the overall design.

### Future extension: TLS between server and upstreams

Future production versions of the load balancer may choose to support
forwarding to some or all upstreams over TLS. TLS connections from clients will
still be terminated, in order to authenticate clients, enforce rate limiting and
authorisation. Switching to optionally support TLS for upstream connections
touches on the design in several places but does not radically change it:

- the server needs to expose a way for the user to indicate
  which upstreams will use TLS connections, and which will use TCP
- if the server actively probes upstreams to infer upstream heath, upstreams
  configured to use TLS connections need to be probed over TLS
- the latency required to establish a logical application connection between
  client and upstream will increase if the client has to wait for a TLS
  connection to be negotiated by the server with the upstream. It may be
  important for the server to compensate for this latency (see performance)
- to cleanly handle a forwarded connection where a client or an upstream has
  communicated that they have finished sending data at the transport protocol
  level, but have not finished receiving, the server will need to implement
  slightly different code paths for the TLS and TCP protocols.
- for interoperability in a wider range of situations, the server may need
  to support presenting a client certificate when connecting to
  upstreams over TLS, and in some deployments this might need to be a different
  certificate than presented to clients. It may also be necessary to
  support a wider range of TLS protocols, cipher suites to integrate with
  legacy upstreams. The main downside of this is the increased surface area
  and effort to support configuration, documentation and QA.

### Future extension: High availability

