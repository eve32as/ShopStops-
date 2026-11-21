# Rendered mobile screen acceptance

Run from the repository root, using the existing installed admin tooling:

```sh
PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH='/Applications/Google Chrome.app/Contents/MacOS/Google Chrome' npm --prefix apps/mobile run test:acceptance
```

Both apps' locked dependencies must already be installed; this suite reuses the
admin Playwright/Vite packages rather than adding a second test-runner tree.
The frontend acceptance CI job installs both lockfiles without scripts and runs
this suite with runner-installed Chromium. No local packages or browser binaries
were installed for this work. Playwright starts and stops a private Vite server on 127.0.0.1:4176;
it refuses to reuse an existing server. The separate admin preview is untouched.

## Scope and isolation

- Imports the actual [mobile screen](../app/index.tsx) and its real helpers. No
  application workflow, event handler or state implementation is copied.
- [Native adapters](native.tsx) translate native controls into semantic DOM with
  real React events, read-only fields, disabled buttons, selected tabs/radios,
  explicit alert choices and modal visibility. Layout styles are intentionally
  not emulated: this is not visual/native accessibility certification.
- [Location adapter](location.ts) simulates permission, position acquisition,
  watches and removal. The bridge exposes deterministic positions and AppState
  transitions. Fake time advances the application's actual five-second timer.
- [Stripe adapter](stripe.ts) records initialization and presents a selectable
  simulated native sheet. Success, cancellation and uncertain outcomes require
  an explicit test click; alerts never auto-confirm. No real card data is used.
- Gateway settings are entered through the actual ConnectionPanel UI. Only
  synthetic tokens and fixture client secrets are used. Build configuration and
  browser globals override developer API URLs and Stripe keys.
- The context router permits only exact-preview-origin GET requests to reach
  the local server. Known HTTPS mobile-gateway.example API routes are fulfilled
  in-process, including known-resource CORS preflights. Unknown routes, methods,
  origins and authorization values fail the test. WebSockets are blocked and
  fail the test; Vite's injected client is removed. Service workers are blocked.
- Screenshots, traces and videos are off. Text-only test artifacts stay under
  this directory's ignored test-results subdirectory.
- The PNG require is replaced only in the served screen transform, never on
  disk. React and React DOM resolve to the installed mobile dependencies;
  Playwright and Vite resolve to the verified installed admin dependencies.

These are browser acceptance tests of rendered React components with simulated
native adapters, **not native/device E2E**. They do not prove native permissions,
navigation handoffs, real Stripe payments, webhooks, backend authorization,
database behavior or production readiness.

## Coverage

- Customer nearby browsing, quantity controls, saved-order review, delivery-window
  validation/locks, basket creation, native-sheet submission, pending status,
  duplicate-payment blocking, cancellation/uncertainty and selectable alerts.
- Order 503 retains exact serialized body/key, including after the requested
  window elapses; 404 unlocks editing and permits a fresh changed request.
  Basket 503 retains IDs/body/key and locks cross-stage mutations.
- Driver unsorted stops render in sequence; directions target the next stop;
  arrival precedes completion through the final drop-off. GPS acknowledgement,
  no duplicate position reports, permission denial, backgrounding, terminal
  refresh and blocked/cancelled guards are exercised.
- Merchant location choice, UTF-8/price/volume validation, uncertain-create locks
  and retries, PATCH save, availability toggles without unsaved-draft leakage,
  billing refresh, native SetupIntent sheet and subscription visibility/readiness.

## Verified result

Verified locally on 2026-11-18: **16/16 rendered workflow tests passed**, no
skips or expected failures. Mobile helper/transport tests passed **47/47**,
TypeScript checking and whitespace validation passed. Billing setup and
subscription confirmation now survive the automatic billing refresh, with a
required regression test. Saved-order windows remain unchanged when checkout
starts after the requested start; terminal route refresh releases an uncertain
stop lock so another assignment can be loaded. GitHub CI has not been run here.
