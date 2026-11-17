# Identity gateway: implemented boundary, not production approval

The Go gateway is implemented in [internal/gateway](../internal/gateway/gateway.go)
and [cmd/shopstops-gateway/main.go](../cmd/shopstops-gateway/main.go). It is not
deployed. No domain, TLS certificate, IdP tenant, actual JWT, user binding, or
production end-to-end flow has been provisioned or verified by this change.
The root infrastructure scaffold is unchanged.

## Runtime configuration

Supply configuration at runtime, never in image build arguments or source control.
The gateway does not perform login, exchange authorization codes, mint tokens,
or accept the backend service credential as a public identity.

| Environment variable | Requirement |
|---|---|
| `SHOPSTOPS_OIDC_ISSUER` | Required exact HTTPS issuer URL. Discovery must succeed before listening. No credentials, query, or fragment. |
| `SHOPSTOPS_OIDC_AUDIENCE` | Required API-specific audience included in the signed bearer JWT's `aud`. Do not reuse an unrelated client ID. |
| `SHOPSTOPS_BACKEND_URL` | Required private HTTP(S) **origin**, without path, credentials, query or fragment. Private/loopback IP, single-label service DNS, `.internal`, `.svc`, or `.svc.cluster.local`. Every DNS result is rechecked at dial time; public/link-local IPs are refused. |
| `SHOPSTOPS_INTERNAL_API_TOKEN` | Required 32–4096 non-whitespace printable ASCII characters, generated securely and shared only with the API. Length validation does not establish entropy. |
| `SHOPSTOPS_OIDC_SIGNING_ALGS` | Optional comma-separated asymmetric allowlist; default `RS256`. Supported: RS256/384/512, ES256/384/512, PS256/384/512. No `none`, HMAC, or signature-check bypass. |
| `SHOPSTOPS_OIDC_TRUSTED_CLAIMS` | Default false. Set true **only after** reviewing issuer-managed ShopStops claims and privilege provisioning. This is a trust-contract switch, not a development auth bypass. |
| `SHOPSTOPS_CORS_ORIGINS` | Optional comma-separated exact HTTPS browser origins, no trailing slash or wildcard. HTTP is allowed only for loopback client origins. Unlisted origins (including `null`) are denied; omitted setting disables cross-origin browser access. Requests with no Origin remain possible and still require identity where protected. |
| `HTTP_ADDR` | Default `:8080`; private HTTP listener behind an HTTPS edge. |

Verification uses maintained `github.com/coreos/go-oidc/v3/oidc` v3.21.0
(Go 1.25 minimum) with discovery and remote JWKS verification. Signature,
algorithm, exact issuer, audience, expiry and subject are checked. The library
also checks `nbf`, with its five-minute clock-skew allowance. JWTs must be compact
signed tokens. Discovery/JWKS use HTTPS, five-second HTTP timeouts, bounded
responses and no redirects. Plain HTTP discovery is available **only to package
tests for a loopback synthetic issuer**, never through the executable or env.
Neither token headers nor request input choose a JWKS endpoint.

Use an issuer contract explicitly authorizing these JWTs as API bearer credentials.
The verifier is not an OAuth introspection endpoint and will not accept opaque
access tokens. Prefer a dedicated API audience; an ID token for another client is
not an API credential. Login/PKCE/nonce handling, revocation, short token lifetimes,
and refresh remain responsibilities of the IdP and clients.

## Identity and privilege contract

Default identity is the verified `sub`, with **customer only** privileges. Generic
`role`, `roles`, groups, email, request headers, body fields and query parameters
never grant privileges. Customer actor references always equal `sub`.

With `SHOPSTOPS_OIDC_TRUSTED_CLAIMS=true`, these two fixed namespaced claims may be
consumed from the **verified** token:

- `https://shopstops.example/claims/role`: a single string, exactly `customer`,
  `merchant`, `admin`, or `driver`; missing means customer. Null, arrays, unknown
  roles and malformed enabled claims fail closed.
- `https://shopstops.example/claims/actor`: a printable non-whitespace ASCII
  identifier of at most 200 bytes for elevated identities. Merchant requires this
  claim to be a UUID matching the backend merchant record. Admin defaults to `sub`
  when this claim is absent. Driver identities can be recognized but **no driver
  operations are enabled**. Customer ignores this override and keeps `sub`.

The namespace is a reserved contract identifier, **not evidence of a real owned
domain**. Do not enable these claims if users can edit them via profile metadata,
registration, account linking or token customization. The issuer administrator
must bind `(issuer, sub)` to the correct merchant UUID, restrict role assignment,
review admin provisioning and ensure actor stability. Tokens remain valid until
expiry; changes to roles are not instant revocation. Only one issuer is supported;
issuer migration must reconcile existing customer references before reuse.

## Exact operation policy

Both root paths below and the same paths with **one `/v1` prefix** are supported.
Path IDs must be literal UUIDs. No arbitrary paths, implicit HEAD, subtree match,
encoded paths, dot traversal, backslashes, duplicate slashes or redirects.
OPTIONS is handled locally only for an enabled exact operation and an approved
Origin/method/header combination; it never forwards service credentials.

| Method | Path | Gateway policy |
|---|---|---|
| GET | `/healthz` | Public; forwards backend/database readiness, no identity or auth headers |
| POST | `/webhooks/stripe` | Public; unchanged body and Stripe-Signature only, no identity/auth headers; backend verifies Stripe signature |
| GET | `/stores/{storeLocationId}/nearby-merchants` | Customer |
| GET | `/stores/{storeLocationId}/catalog` | Customer |
| POST | `/orders` | Customer; verified actor creates new owned order |
| GET | `/merchant/store-locations` | Merchant |
| GET, POST | `/merchant/catalog` | Merchant |
| PATCH | `/merchant/catalog/{itemId}` | Merchant |
| GET, POST | `/merchant/subscription` | Merchant |
| POST | `/merchant/subscription/setup-intent` | Merchant |
| POST | `/admin/checkouts/{checkoutId}/refund` | Admin; platform-wide refund privilege, audited actor, not customer ownership |
| POST | `/checkouts` | **403: disabled conservatively** |
| POST | `/checkouts/{checkoutId}/payment` | **403: disabled conservatively** |
| GET | `/driver/routes/{routeId}` | **403: blocked until verified driver binding** |
| POST | `/driver/routes/{routeId}/location` | **403: blocked until verified driver binding** |
| POST | `/driver/routes/{routeId}/stops/{stopId}/events` | **403: blocked until verified driver binding** |
| GET | `/driver/bundles/{bundleId}` | **403: legacy contract only, no backend handler; blocked** |
| POST | `/driver/bundles/{bundleId}/stops/{stopId}/events` | **403: legacy contract only, no backend handler; blocked** |
| POST | `/dispatch/bundles` | **403: blocked for every role, including admin** |

Admin-only finance routes are POST `/admin/finance/scenarios`, GET
`/admin/finance/costs`, and POST `/admin/finance/costs`. POST bodies are capped
at 8 KiB; GET has no body. See [profit-model.md](profit-model.md) for the exact
estimate/manual-cost boundaries, idempotency, pagination and migration requirements.
No finance operation approves wages, changes checkout fees or sends payouts.

Other path/method combinations return 404; ambiguous paths return 400. Protected
enabled operations require exactly one bearer header: invalid tokens return 401,
wrong roles return 403. Roles do not inherit each other's operations.

### Reviewed backend ownership boundaries

Do not infer ownership from the OpenAPI descriptions alone:

- Order creation derives `customerRef` exclusively from the injected actor and
  rejects unknown JSON fields in [server.go](../internal/httpapi/server.go).
  [006_catalog_orders.sql](../db/migrations/006_catalog_orders.sql) stores that
  actor and verifies the customer on idempotent retries. No other order route
  is exposed by this gateway.
- Merchant handlers require merchant role/UUID; catalog/store queries and catalog
  mutation SQL constrain ownership. Review [server.go](../internal/httpapi/server.go),
  [007_merchant_catalog_management.sql](../db/migrations/007_merchant_catalog_management.sql)
  and [lifecycle.go](../internal/httpapi/lifecycle.go). Billing addresses the
  bound merchant, not a merchant ID supplied in the URL.
- Checkout SQL **does** check `orders.customer_ref` against the injected customer,
  checks the existing checkout's customer on retries, and payment SQL constrains
  checkout ownership. See [002_unified_marketplace.sql](../db/migrations/002_unified_marketplace.sql)
  and [payments.go](../internal/httpapi/payments.go). Nevertheless, this gateway
  deliberately enables only catalog reads and order creation for customers.
  Re-enabling checkout/payment requires a separate policy review and cross-user
  integration tests, not merely adding the shared token to those routes.
- Driver repository/SQL already compares `driver_ref` with the actor, but there
  is no verified identity-to-assignment provisioning contract. An arbitrary
  subject must not stand in for an assigned driver. Keep blocked until immutable
  subject/driver binding, trusted role issuance, assignment lifecycle and
  cross-driver negative tests are verified.
  The two legacy driver bundle operations in OpenAPI have no registered HTTP
  handlers; they are explicitly blocked rather than interpreted as route aliases.
- Dispatch currently requires the shared backend token, **not** a backend role
  check. It remains private and blocked at this gateway for everyone.

## Forwarding and operational limits

The gateway rebuilds request headers. It never forwards client Authorization,
cookies, internal/identity headers, Host, Forwarded, X-Forwarded-*, proxy auth or
hop-by-hop headers. Protected operations receive only the gateway service token
and sanitized verified actor/role; public operations receive neither. Safe
application headers are Accept, Content-Type and Idempotency-Key; Stripe-Signature
is copied only for the webhook. Backend requests use the incoming cancellation
context and a 30-second overall operation deadline. No environment HTTP proxy is
used for backend or OIDC connections.

Bodies are bounded before forwarding: order 32 KiB, catalog mutation/refund 8 KiB,
merchant billing POST 8 KiB, webhook 1 MiB; GET bodies must be empty. Upstream
responses are bounded to 4 MiB. Redirects/upgrades and upstream 5xx become generic 502
(health readiness 503 is preserved with a sanitized message);
network/verification errors and upstream debug headers are not disclosed.
Upstream response headers are discarded except a safe JSON content-type decision.
Responses containing the gateway service token or the protected request's bearer
token (including JSON-escaped strings) are refused. Legitimate merchant SetupIntent client secrets remain part of
the authenticated billing response by design. Backend data is still trusted:
these filters do not provide general DLP or sanitize every possible secret.

The HTTP server has 5-second header, 15-second read, 35-second write, 60-second
idle timeouts and a 32 KiB header cap. Images use scratch, CA certificates and UID
10001:10001; the Go builder patch is pinned and meets the module minimum. Review
immutable base-image digests before release. CI builds the gateway without
publishing and checks its non-root image identity.

Production must terminate **HTTPS at an ALB** with a reviewed certificate and
domain, then reach this gateway over private HTTP. Restrict gateway ingress to
the ALB security group and API ingress to the gateway security group; the API
must never be publicly reachable or bypassable. Restrict egress and DNS, inject
the internal token from a secrets manager, rotate it in both services, and set
HSTS at the HTTPS edge (not based on spoofable forwarding headers). Backend
private URL validation alone is not proof of network isolation.

Open production gates include real IdP integration/user bindings, ALB/TLS and
network validation, rate limiting/WAF, abuse controls, revocation strategy,
audit/metrics, database migration/integration evidence, gateway image runtime
verification and end-to-end browser/mobile authentication. This is **not a fully
production-safe system or a deployment approval**.

## Tests

`go test ./...` includes fake-verifier HTTP tests and an ephemeral loopback issuer
with real remote JWKS signature verification. Coverage includes bad/unsigned/
forged/expired/wrong-audience/wrong-issuer/wrong-algorithm tokens, forged identity
headers, admin escalation, disabled routes, ambiguous paths, CORS, unchanged
webhook bytes with absent auth headers, bounds, redirects, response credential
filtering, private dialing, startup validation and request cancellation. No actual
IdP or production JWT is contacted or used. Existing SQL integration suites
remain separate from these gateway tests.