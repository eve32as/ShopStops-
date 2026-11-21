# Repeatable simulated API frontend acceptance

This is **simulated API frontend acceptance**, not server security verification,
real identity/role authorization, database idempotency verification, payment E2E,
or production approval. Existing Go/gateway/SQL suites remain separate gates.

## Mobile rendered workflow follow-up

A clean local rerun on 2026-11-21 passed **16/16 mobile rendered workflow tests**
using the installed Chrome executable. The admin acceptance rerun passed **11/11**,
with **33/33 admin Node tests**, **47/47 mobile Node/transport tests**, admin lint/build,
and mobile TypeScript checking passing. These results remain simulated API checks,
not native-device or live-integration evidence.

The [mobile acceptance suite](../apps/mobile/acceptance/README.md) loads the actual
React screen with test-only native/Stripe/location adapters and intercepted APIs.
Verified 2026-11-18: **16/16 rendered workflow tests**, **47/47 mobile
helper/transport tests**, TypeScript and whitespace checks passed. There are no
skipped or expected-failure cases. The unprivileged frontend CI job now installs
both locked frontend dependency trees without scripts and runs both suites;
that CI change has not been executed from this editor. No local install or native
build was needed. These tests are neither native/device E2E nor proof of live
payments, tracking or backend authorization. Earlier November 17 results below
describe the preceding admin acceptance work.

## Run

Use Node 22.16+ and the locked admin dependencies. For a fresh checkout:

```sh
PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD=1 npm --prefix apps/admin ci --ignore-scripts --no-audit --no-fund
```

Use an **already installed** Chromium/Chrome executable locally. No local browser
download or native/mobile install is required. On this Mac the verified command is:

```sh
PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH='/Applications/Google Chrome.app/Contents/MacOS/Google Chrome' npm --prefix apps/admin run test:acceptance
```

The optional executable-path variable is nonsecret, checked for executability,
and only changes the browser binary, not the test target. An installed Playwright
Chromium can instead be used by omitting the variable. Tests fail rather than
silently skip if no usable browser is available. System Chrome versions can differ
from CI's Playwright-pinned Chromium; a local pass does not replace the CI run.

The complete frontend checks are:

```sh
PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH='/Applications/Google Chrome.app/Contents/MacOS/Google Chrome' npm --prefix apps/admin run test:release
npm --prefix apps/mobile test
npm --prefix apps/mobile run typecheck
git diff --check
```

`test:release` runs admin Node tests, lint, production build and acceptance tests.
It is a local QA gate, **not deployment authorization** and not a substitute for
`build:release` with approved release configuration. Each acceptance invocation
also builds current sources before starting its own Vite preview on
`http://127.0.0.1:4175`, with `--strictPort` and `reuseExistingServer: false`.
Leave that port free. The separately running 4173 preview is untouched. The test
server is shut down by Playwright, including on failure.

## Isolation and coverage

- The pinned `@playwright/test` dependency is **1.58.2**. The lockfile adds only
  its dependency tree, not upgrades to existing frontend dependencies.
- The preview build overrides `VITE_API_URL` to empty. Tests enter only a
  known synthetic admin token and reserved `.example` fixture gateway.
- Context-wide interception fulfills explicitly registered API fixtures. Only
  static GET resources on the exact local preview origin can reach a server.
  Other HTTP requests are aborted and fail the suite; the fixture hostname is
  never contacted. Service workers are blocked. No live gateway, identity
  provider, Stripe, backend, database, AWS, or other application service is used.
- Tests use real browser clicks, form entry, native required-field validation,
  hash/history navigation, Tab and Enter, rather than DOM activation or injected
  replacements for application handlers.
- Direct hashes, invalid-hash fallback, active links, history and draft survival
  across workspaces; no API call merely by opening a workspace.
- Invalid finance input blocks requests. Integer-cent scenario bodies go to the
  simulated API; displayed estimates come from the API response, including
  negative/null outputs. Editing assumptions invalidates the displayed estimate.
- Refund and manual-cost failures retain the same key/body for manual unchanged
  retries. Refund acceptance stays pending; manual costs remain separate from
  calculated forecasts and are not proof of payment.
- Shopper invalid UUIDs/evidence block submission; assignment progresses to a
  prefilled verification stage, completion to review. Failures/conflicts retain
  keys/body; a manually reconciled unchanged retry is simulated. Earnings stay
  `pending_review`, never paid. Lists distinguish populated/empty states.
- Simulated 401/403 responses show safe messages, no upstream details, and no
  automatic retries. These fixtures **do not prove backend role enforcement**.
- Clearing credentials, replacing the token, and changing the gateway remove
  private drafts/results/records and abort an intercepted pending request. A late
  fixture cannot repopulate the new session. Browser storage remains empty.
- At 390px the real keyboard can reach and activate every compact workspace link;
  every workspace is checked for document-level horizontal overflow. This is not
  a comprehensive accessibility audit or mobile-native browser test.

Response fixtures include required fields from the actual OpenAPI finance and
shopper schemas, including nested completion/earnings and list pagination. A Node
test checks required response-field coverage against the current OpenAPI text;
this intentionally small check is not a full JSON Schema validator. Refund
acceptance uses the documented 202 response and the current frontend result shape
(OpenAPI does not specify that response body). Deliberately distinctive finance
outputs test response rendering, not backend arithmetic.

Traces, screenshots and video are disabled; no artifact-upload step or token
logging is added. Playwright may produce ignored local failure-context metadata;
only synthetic test data is entered. Never use real credentials in this suite.

## CI and native limits

Verified locally on 2026-11-17: **11/11 browser acceptance tests**, **33/33 admin
Node tests**, **30/30 mobile Node tests**, admin lint/build, mobile typecheck and
`git diff --check` all passed. Browser tests used existing Google Chrome
157.0.8103.79; no browser was downloaded locally. Installation added four small
test-runner packages with scripts disabled; existing locked packages were not
upgraded.

The existing unprivileged workflow has a separate `frontend-acceptance` job after
`tests`, read-only contents permission, no secrets and no deployment/publish step.
It installs locked npm dependencies without scripts, installs **Chromium only on
the disposable GitHub runner**, and runs the same acceptance command against the
local Vite preview. Package/browser setup downloads are necessary CI tooling,
not live application integrations. CI has not been executed from this editor.

Mobile continues to run its existing native-helper behavioral tests and
TypeScript check. Added tests exercise the real `cartSummary`,
`deliveryCoordinates` and `requiredUUID` helpers through selection/removal/store
changes and invalid form values. They are **not rendered native UI or device
E2E**. iOS/Android, native Stripe sheets, GPS/permissions, assistive technology
and real authorized service journeys remain explicit device/integration gates.
