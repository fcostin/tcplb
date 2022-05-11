## What

tcp load balancer

## Why

## Details

### Design approach

Logical network cartoon illustrating a minimal deployment:
```
[client] <-- mTLS --> [lb] <-- TCP --> [upstream]
```
- clients: it is assumed there are zero or more clients
- upstreams: it is assumed there are zero or more upstreams
- lbs: it is assumed there are one or more load balancers

#### goals

- correctness
- proof-of-concept of key parts of design

#### non-goals

- performance
- completeness
- compatibility with deployment environments where clients cannot be configured
  for mTLS as per design -- we assume custom client certs can be issued per 
  authentication design & that modern TLS protocol & cipher suites are supported
- server configurability without recompiling application code

### Scope

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
2. an authorization scheme defining what upstreams are available to each client
3. accept and forward decrypted connections to upstreams using library


### Proposed API (server)

"alpha" scope:

Server listens on a configured interface & TCP address for incoming connections
from clients. It attempts to negotiate mTLS connections & then reverse proxy
the application layer connections from client to an upstream that the client is
authorised to access. That's the API.

"future" scope:
- server supports being put into "drain" mode, either by SIGINT or by call to
  admin API (TBC)
- server supports reloading configuration without disrupting in-flight forwarded
  connections, either by SIGHUP or by call to admin API (TBC)

### Security considerations

### CLI UX

"alpha" scope:
- some server configuration may be hardcoded & require editing source and
  recompilation to vary

"beta" scope:
- support being configured in natural way if being run as service in k8s
  (environment variables, config files, maybe flags)
- support basic CLI usability for humans fiddling manually on command line
  (flags, env vars, config files)

### Key details

#### forwarding connections

The load balancer server will accept and terminate TLS connections from the
client, and forward the decrypted application-layer protocol to an upstream
over a TCP connection. The load balancer design assumes that the clients and
upstreams have agreed on some particular application-layer protocol, but the
load balancer does not know what that application protocol is. This has some
consequences for the load balancer server design:

- We might wish to attempt to reuse an established TCP connection between
  the load balancer and an upstream to forward multiple application-layer
  "requests" from one or more clients, in an effort to reduce latency from
  the overhead of reestablishing TCP connections. However, since the load
  balancer doesn't know how (or if) the application protocol denotes request
  boundaries, and TCP doesn't offer a way to encode request boundaries, we
  cannot do that. This means that the load balancer cannot attempt to reuse
  TCP connections to upstreams across multiple "requests", so instead each TCP 
  connection to an upstream will be used at most once to forward a single client
  connection, and then closed. This may increase latency due to additional
  roundtrips to establish new TCP connections compared to a more specialised
  application-protocol aware design, but has the advantages of being simpler
  (less chance of being defective) and less effort to implement.
- Arguably, clients and upstreams might agree to use a custom application
  protocol that involves waiting for arbitrarily long periods of time between
  sending any bytes. However, one downside of supporting such a protocol is
  that it increases the chance that resources are wasted supporting idle
  connections caused by failures in clients, upstreams, networks, etc. The 
  load balancer will enforce an application-protocol level idle timeout,
  defaulting arbitrarily to 300 seconds, after which it will close both the
  TLS connection to the client and the TCP connection to the upstream.
- The load balancer may attempt to use TCP-level keepalives

tradeoff: the server will make no attempt to maintain a pool of upstream connections that can be reused across multiple client connections. the server will make no attempt to maintain a queue of warm upstream connections. this has the consequence the connection forwarding logic simpler, but will increase the latency for client communications to reach upstreams and upstream responses to reach clients.

it is likely that this could be improved in future releases without needing to rework the rest of the design.

if in future the server is enhanced to support (m)TLS connection forwarding between the server and upstreams, then the latency impact of failing to maintain a pool of pre-negotiated "warm" TLS connections will be even more severe due to the additional roundtrips required for a TLS handshake.

#### even more detail about forwarding connections

sketch of reliable message forwarding from a source conn (src) to a destination
conn (dst)

```
var result err = nil

for {
    deadline := time.Now() + IDLE_TIMEOUT
    _ = src.SetReadDeadline(deadline)
    _ = dst.SetWriteDeadline(deadline)
    n_copied, err := attempt to copy up to COPY_LIMIT bytes from src to dst

    switch err {
    case nil:
        continue // making progress. keep going.
    case io.EOF:
        ensureShutdownWrite(dst) // Success! communicate EOF to our peer
        break
    case errors.Is(err, os.ErrDeadlineExceeded):
        if n_copied == 0 {
            // No bytes were copied before IDLE_TIMEOUT deadline.
            // It is likely that progress is no longer being made.
            // Terminate forwarding to conserve resources.
            // TODO FIXME what if no bytes were copied src -> dst but
            // plenty are getting copied dst <- src? we shouldn't just terminate!!
            ensureConnClosed(src)
            ensureConnClosed(dst)
            result = err
            break
        } else {
            continue // making progress. keep going.
        }
    default:
        // Some error occurred during copy, either while reading
        // from src or writing to dst. Terminate forwarding.
        ensureConnClosed(src)
        ensureConnClosed(dst)
        result = err
        break
    }
}
```

reason for completion               interpretation      action
---------------------               --------------      ------
buffer of data data copied, no EOF  okay                reset deadline, iterate
src conn EOF                        done                ensure writing to the dst conn is shutdown, break with success
dst read error                      failure             ensure both conns shutdown, break with error
src write error                     failure             ensure both conns shutdown, break with error
deadline exceeded, no bytes copied  failure             ensure both conns shutdown, break with error
deadline exceeded, 1+ bytes copied  okay                reset deadline, iterate


ensuring writing is shutdown on the dst conn (aka shutdown SHUT_WR)
is intended to
1.  send all queued messages to the client
2.  inform the client that no more messages will be coming

If dst conn is a TCP conn, we inform client of EOF by sending TCP FIN
If dst conn is a TLS conn, we inform client of EOF by sending TLS alert close_notify


n.b.  on error, it might be better to ensure the error-ing conn is closed and
ensure the other one (if it is non-error-ing) is write-shutdown


ref: https://www.ietf.org/rfc/rfc793.txt

> 3.5.  Closing a Connection
>
>  CLOSE is an operation meaning "I have no more data to send."  The
>  notion of closing a full-duplex connection is subject to ambiguous
>  interpretation, of course, since it may not be obvious how to treat
>  the receiving side of the connection.  We have chosen to treat CLOSE
>  in a simplex fashion.  The user who CLOSEs may continue to RECEIVE
>  until he is told that the other side has CLOSED also.  Thus, a program
>  could initiate several SENDs followed by a CLOSE, and then continue to
>  RECEIVE until signaled that a RECEIVE failed because the other side
>  has CLOSED.  We assume that the TCP will signal a user, even if no
>  RECEIVEs are outstanding, that the other side has closed, so the user
>  can terminate his side gracefully.  A TCP will reliably deliver all
>  buffers SENT before the connection was CLOSED so a user who expects no
>  data in return need only wait to hear the connection was CLOSED
>  successfully to know that all his data was received at the destination
>  TCP.  Users must keep reading connections they close for sending until
>  the TCP says no more data.

#### timeouts

TODO
- set hard max time limit on each forwarded client connection?
- TCP keepalive?


#### accepting connections

Errors encountered while listening for connections will be logged, but not
regarded by the server as fatal.  The server will pause for a brief duration
and then resume listening.

If the server is unable to accept any connections, it is the  responsibility of
the server's supervisor to detect this symptom and take the appropriate action.

#### TLS

regard it as custom protocol design problem

may as well force TLS 1.3 & `TLS_CHACHA20_POLY1305_SHA256`

> In custom protocols, you don’t have to (and shouldn’t) depend on 3rd party CAs. You don’t even have to use CAs at all (though it’s not hard to set up your own); you can just use a whitelist of self-signed certificates
>
> Since you’re doing a custom protocol, you can use the best possible TLS cipher suites: TLS 1.2+, Curve25519, and ChaPoly. That eliminates most attacks on TLS.

ref: https://latacora.micro.blog/

#### mutual authentication

The authentication scheme will use x509 certs to bind identities to public keys.
Each cert with a node identity (client node, server node) will be certified by
a trust chain from some trusted root CA. Each client must be configured to trust
the root CA that anchors the server's cert chain. Each server must be configured
to trust the root CA that anchors the client's cert chain. Even more
importantly, client and server must be configured _not to trust_ root CAs that
are not regarded as valid sources of authentication information.

A minimal example of the above scheme could be to use a single self-signed cert
for the server, and a single self-signed cert per client, and configure each
node to trust the certificates of its peer(s).

The server will require all clients to present a client certificate.

#### subsets of upstreams

one way to model the consequences of authorisation, healthchecks and forwarding
policies on which upstreams a client may access is as a subset of upstreams.

initially, the full set of configured upstreams is regarded as feasible.
authorisation logic can narrow the set of feasible subsets to the authorised subsets

authorisation logic should run before additional filtering, as that defends later
filtering stages being swamped by unauthorised forwarding attempts

health-checking logic can further narrow the feasible subset of upstreams to those
that are deemed to be healthy given local information

after all filtering operations are run, if the choice of upstream is still nonunique,
it can be selected by a prioritisation policy, such as the least-connections policy.

if multiple feasible upstreams are regarded as having equal priority, ties can be broken arbitrarily at random.

if the set of feasible upstreams is empty, this is regarded as an error. see "communicating errors to the client".


#### authorisation scheme

- authentication scheme uses x509 certs bind identity to public key 
- TODO: need to define scheme for precisely how client identity is encoded in certs -- could be name, subjectaltname (FQDN), some custom attribute (proper name? node? service account email address?)
- extract identity from certs
- query authorisation module with client identity, it returns a (possibly empty) subset of upstreams that the client is authorised to access

```
client -- member_of? -- clientgroup

clientgroup -- can_access? -- upstreamgroup

upstream -- member_of? -- upstreamgroup 
```

rejected alternative:

- claims such as "this identity has the role Foo" could be baked into x509 certs and used to encode the binding between identities and roles. that would force certs to be recreated each time permitted roles change. that might be acceptable in some deployments where clients use short-lived certs and can easily obtain up to date certs.


#### communicating errors to the client

Any severe errors encountered by the server that impede a client from
being forwarded to an upstream will be logged and then communicated
by the server closing the TLS & TCP connection with the client.

Examples of severe errors include:

1. there exists no upstream that the client's connection can be forwarded to. this could be due to authorisation policy, misconfiguration of upstreams, all upstreams being unhealthy.
2. any error is encountered while attempting to read from or write to the TCP connection between the server and the upstream

Since the server is unaware of the specific application-layer
protocol that the client is using to communicate with an upstream,
it is unable to communicate the cause of the error to the client
using an application-layer protocol.

Any errors related to the connection between the client and the
server encountered at the underlying TLS or TCP protocol layers
will be communicated by those protocol modules in the standard way.

#### client rate limiting

Since the load balancer is application protocol agnostic, rate limiting will apply at the level of simultaneous client connections to the server.

To enforce rate limiting we need to identify the client. There are three obvious alternatives:

1. rate limit by IP
2. rate limit by client identity, after a successful authentication
3. rate limit by IP; also rate limit by client identity

Each option has downsides: the first fails to
prevent a client from establishing a huge number of TLS connections from many IPs using the same client cert, the second would prevent that but exposes the server to being overwhelmed by clients attempting large numbers of TLS handshakes.  The third option is superior but the most effort to implement. If we knew the main reason behind the requirement to rate limit (e.g. attempting to defend the server against DDoS attacks, enforcing billing limits for clients, ...) then that could influence our decision.

We will implement rate limiting based on client identity only, as that is lower effort and naturally fits with the authentication design. Support for additional rate limiting by IP could be added in future for a "beta" release.

#### least-connections forwarding

arguably the number of connections to an upstream is
a global resource, that depends not only on the behaviour of a single load balancer server, but other servers as well (consider multiple identically configured load balancers deployed in parallel).

for simplicity and robustness we make the tradeoff that the least-connections decision can be made locally using what is known by the load balancer server process, without communication.

least-connection forwarding requires
- state shared between different connection forwarders within a single load balancer server -- e.g. a map of upstreams to current connection counts, that is safe for concurrent access
- a minimal API offering options to increment and decrement the connection count of a given upstream, and an operation to query with a set of "candidate" upstreams and obtain the subset of upstreams with minimal connection count

#### health-checking of upstreams

Regard health checking as problem of load balancer server estimating its belief-state of each upstream, and deciding if it is healthy to be forwarded new connections, or unhealthy.

Health checking can be active (e.g. probe each upstream according to some frequency) or passive (e.g. infer health from what is observed when attempting to forward each connection). We will incorporate information from both active and passive probes into health status.

We need to decide how the "probe" would be implemented: what address do we probe, with what protocol, and what do we regard as success?

The server will actively probe using the exact same address and protocol as is configured to forward a real client connection (ideally using the same code). If we successfully establish a connection that could be used to send application data, regard probe as success. Otherwise, regard probe as fail. If timeout, also regard probe as fail. If probe fails, log the symptom (if known from error).

An alternative is to probe a different configurable TCP address per upstream, but that requires more server configuration and can result in failure modes where the special TCP address exposed by the upstream is healthy while the connections to the real TCP address fail.

More details:

- active probe schedule: configurable, fixed duration (say hardcode at 15 seconds). for simplicity, active probes continue regardless of inferred health status of upstream. this has downside of subjecting an unhealthy downstream to additional load from active probes.
- one helper goroutine will be allocated to active probe each configured upstream, launched when server starts
- state will be maintained per upstream to track current inferred health state, any additional per-upstream state statistics could be stored here.
- transition rule between health states will be simplest possible thing: if HEALTHY and observe one probe failure (either active or passive probe) then successor state is UNHEALTHY. Similarly, if UNHEALTHY and observe one probe success then success state is HEALTHY. Otherwise, state does not change.
- if observed probe outcome causes state change from HEALTHY to UNHEALTHY or vice versa, this will not cause the server to "pre-empt" any existing forwarded connections, once the decision has been made to forward them to some given upstream, they will be left to complete or fail.

Deferred scope:  in future the above transition rule be enhanced to transition only after observing some number of repeated failures or successes, respectively, or to make the decision based on a short-time-window estimate of the connection failure rate vs some defined objective.

c.f. hidden markov model with two hidden states, HEALTHY & UNHEALTHY. infer hidden state from observations

c.f. circuit breaker with OPEN, HALF-OPEN, CLOSED states

c.f. nginx TCP health check documentation

In a deployment configuration with multiple horizontally-scaled load balancer server, ideally we might want the load balancers to share locally observed information about upstream health, and incorporate information from fellow load balancers to form a more accurate estimate of the state of upstream health. But this could complicate things immensely, and potentially introduce new failure modes. Descoped and deferred.

#### high availability

load balancer as designed could be used to implement
a crude high-availability capability (e.g. to survive failure of a single load balancer), provided:

- multiple load balancer servers are deployed with identical configuration
- some mechanism is used that allows clients to discover and attempt to connect to alternative servers (e.g. maybe BGP anycast or perhaps DNS round robin, +/- DNS cache delay)
- for some load balancer server failure modes, the server could get into a broken state where it continues to accept client connections but doesnt forward them correctly. to achieve robustness against this kind of failure mode, either the server itself or a supervisor would need a mechanism to detect this symptom and then prevent the server from accepting additional client traffic
- in general, clients need to be responsible for retrying connections that time out or error, using a backoff retry policy
- in the event that upstreams or load balancer servers are becoming unhealthy due to overloading, there may need to be a mechanism to communicate backpressure
- for some "global" resource allocation problems such as number of connections to a given upstream, or client rate limiting, the best decision could be made using information from all of the load balancers, but this would require a way for them to share information. A much simpler, cruder solution without communication would be to pre-configure each load balancer with local limits that would be appropriate either if it was one healthy server among the total n servers, or one healthy server among the total n-1 servers, assuming one peer was unhealthy.
- not strictly required for high availability, but if we want to be able to do zero downtime deploys, the load balancers need to support being switched into a mode where they stop accepting new connections but continue to process any existing forwarded connections until those connections are completed and closed by the client or upstream. this "drain" mode could be activated by a special admin API call or SIGINT or so on.