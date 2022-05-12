## What

TCP load balancer

## Scope

Library scope

1. Primitives for connection forwarding, including:
    1. a least-connections forwarding policy, that tracks the number of
       connections per upstream.
    2. a health-checking forwarding policy, that removes unhealthy upstreams
2. A per-client connection rate limiter that tracks the number of client
   connections

Server scope

1. mTLS support between the client and the upstream, to support encryption,
   integrity and mutual authentication
2. an authorisation scheme defining what upstreams are available to each client
3. accept and forward decrypted connections to upstreams using library


## Details


- Protocols supported for forwarding
- CLI UX
- Forwarding flow
- Timeouts
- Communicating errors to the client
- Implementation detail: closing forwarded connections
- TLS
- Authentication
- Client rate limiting
- Authorisation
- Monitoring upstream health
- Future extension: Monitoring upstream health improvements
- Future extension: Performance
- Future extension: TLS between server and upstreams
- Future extension: High availability

### Protocols supported for forwarding

The server will accept TLS connections from clients and forward them over 
TCP connections to upstreams.

The server assumes that the clients and upstreams have agreed on a common 
application-level protocol, but it does not know or need to know what that
protocol is. This has the advantage of broadening the range of situations 
where the load balancer can be applied, and reduces the complexity and 
effort of implementation but restricts the server's ability to perform
very well in any particular situation, compared to specialised load 
balancers that include application protocol specific design optimisations.

### CLI UX

Proof-of-concept load balancer application will support configuration of 
minimal set of options using flags:

- bind address to listen on
- where to find its certificate & private key
- list of 0 or more upstream addresses

Load balancer will display help when invoked with no arguments and exit with 
nonzero status on fatal errors.

Other parameters (e.g. timeouts) will be defined as constants in the code and 
will require rebuilding from source to tweak.

Beyond proof-of-concept, the application aspires to:

- expose parameters users are likely to want to tune in configuration
- support reading configuration from file
- support reading configuration from environment variables

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

### Timeouts

The server will enforce timeouts when establishing TLS connections with 
clients, establishing TCP connections with upstreams. It should also 
implement an idle timeout for established application-level connections 
between a client and upstream: if an idle timeout period elapses without a 
byte being successfully written in one direction or the other, the server 
will close both connections. The default idle timeout will be 5 minutes.
This is designed to protect client, server and upstream resources in the 
event that a node, application or part of the network gets into a broken 
state. A minor downside of enforcing an idle timeout is that it prevents the 
use of weird application protocols where both parties do not talk for long 
periods of time.

### Communicating errors to the client

Since the server is unaware of the application-layer protocol that the client
is using to communicate with an upstream, it is unable to communicate the
cause of any severe error to the client using an application-layer protocol.

Any severe errors encountered by the server that impede a client from being
forwarded to an upstream will be logged and then communicated by the server
closing the TLS connection with the client, without further explanation.

This aspect of the design is not ideal. It is often helpful for a client to 
be able to differentiate temporary errors (backoff then retry) from permanent
errors (give up).

### Implementation detail: closing forwarded connections

Using the go standard library, forwarding can be implemented as a pair of
single-direction copy operations, each between the same pair of `net.Conn`
connections.

Consider a single copy operation from a src conn to a dst conn. Assume
the copy operation has a timeout set and may also complete after copying a
fixed number of bytes. There are a variety of reasons a copy might complete,
and some require the server to take different actions:

```
reason for completion       outcome      server action
---------------------       -------      -------------------------
buffer copied, no EOF       okay         reset timeout, continue another copy
src conn EOF                done         shutdown writing dst conn, return OK
dst read error              fail         ensure both conns closed, return error
src write error             fail         ensure both conns closed, return error
timeout, no bytes copied    fail         ensure both conns closed, return error
timeout, 1+ bytes copied    okay         reset timeout, continue another copy
```

Ensuring writing is shutdown on the dst conn after seeing an EOF from the
src conn is intended to send all data read from src before EOF to the dst, and
then inform the dst that no more messages will be coming.  The `net.Conn`
abstraction doesn't quite suffice, we also need `CloseRead` to handle this
scenario.

```
dst conn protocol   message sent to inform other party of EOF
-----------------   -----------------------------------------
TCP                 FIN
TLS                 alert close_notify
```

Ref: "3.5. Closing a Connection" in [rfc793](https://www.ietf.org/rfc/rfc793.txt).

Complication: the "bytes copied" indicator used to express the application
idle timeout needs to consider bytes copied in both directions, from this
single copy operation and the anti-parallel one, otherwise it may 
application connections where one direction of the conversation is silent, but
the other is very busy. Maybe there's a cleaner way to express it.

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

It is possible that the decision to load trusted CA certs from the usual 
operating-system defined location may make it easier for users to 
accidentally misconfigure the server. A production server may wish to 
redesign this and require the location of trusted CA certs to be explicitly 
specified, to force users to think about the decision.

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

Least-effort option is to regard all upstreams as permanently healthy, 
regardless of evidence to contrary.

With more effort, we can regard health checking as problem of load balancer 
server estimating its belief-state of each upstream, and deciding if it is  
healthy to be forwarded new connections, or unhealthy.

Health checking can be active (e.g. probe each upstream according to some  
frequency) or passive (e.g. infer health from what is observed when 
attempting to forward each connection). Server will incorporate information from
both active and passive probes into health status.

The server will actively probe using the exact same address and protocol as 
is configured to forward a real client connection (ideally using the same code).
If the server successfully establishes a connection that could be used to send 
application data, regard probe as success. Otherwise, regard probe as fail.
If timeout, regard probe as fail. If probe fails, log the symptom.

An alternative is to probe a different configurable TCP address per upstream,
but that requires more server configuration and can result in failure modes 
where the special TCP address exposed by the upstream is healthy while the 
connections to the real TCP address fail.

More details:

- Active probe schedule: configurable, fixed duration (say hardcode at 15 
  seconds). For simplicity, active probes continue regardless of inferred 
  health status of upstream. This has downside of subjecting an unhealthy 
  downstream to additional load from active probes.
- State will be maintained per upstream to track current inferred health 
  state, any additional per-upstream state statistics (e.g. count of 
  consecutive failed or successful connections) could be stored here.
- Transition rule between health states will be the simplest thing: if 
  HEALTHY and observe one probe failure (either active or passive probe) 
  then successor state is UNHEALTHY. Similarly, if UNHEALTHY and observe one 
  probe success then success state is HEALTHY. Otherwise, state does not change.
- If observed probe outcome causes state change from HEALTHY to UNHEALTHY or 
  vice versa, this will not cause the server to preempt any existing 
  forwarded connections, once the decision has been made to forward them to 
  some given upstream, they will be left to complete or fail.

### Future extension: Monitoring upstream health improvements

In state transition rule could be enhanced to transition only after
observing some number of repeated failures or successes, respectively, or to
make the decision based on a short-time-window estimate of the connection
failure rate vs some defined objective.

Further ideas:

- probabilistic model - hidden Markov model with two hidden states, HEALTHY &
  UNHEALTHY. infer hidden state probabilities given observations
- review software circuit breaker state machines
- nginx TCP health check documentation
- in principle, groups of load balancers could pool observations for better
  collective estimates of upstream health

### Future extension: Performance

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

The server as designed could be used as the starting point to implement a
crude high-availability capability (e.g. to survive failure of a single
load balancer), provided:

- multiple load balancer servers are deployed with identical configuration
- some mechanism is used that allows clients to discover and attempt to 
  connect to alternative servers (BGP anycast? DNS round robin +/- cache 
  delay?)
- there needs to be a mechanism to detect if an individual load balancer 
  server has gotten into a broken state, and stop it from accepting new 
  client connections. this suggests a load balancer supervisor.
- in general, clients need to be responsible for retrying connections that 
  time out or error, using a backoff retry policy
- in the event that upstreams or load balancer servers are becoming 
  unhealthy due to overloading, there may need to be a mechanism to 
  communicate backpressure, which clients pay attention to
- for some "global" resource allocation problems such as number of 
  connections to a given upstream, or client rate limiting, the best 
  decision could be made using information from all the load balancers, but  
  this would require a way for them to share information. A much simpler,  
  cruder solution without communication would be to pre-configure each load  
  balancer with local limits that would be appropriate either if it was one 
  healthy server among the total n servers, or one healthy server among the 
  total n-1 servers, assuming one peer was unhealthy.
