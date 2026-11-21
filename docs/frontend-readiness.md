# Frontend UX and hardening status

Updated 2026-11-18. This is critical-path hardening of the existing frontends,
not approval for live payments, deployment, or an app-store launch. Existing
backend authentication, role enforcement and payment feature gates are unchanged.

## Mobile workflow completion follow-up — 2026-11-18

- Customer: saved-order review includes IDs, store, item count, merchandise total
  and returned state. Delivery windows are editable before saving orders and
  validated against API limits. Saved-order windows are preserved for checkout.
  Uncertain order/basket requests lock edits and retain their original key/body;
  definite 400/401/403/404/422 rejections allow correction. Conflicts, rate limits,
  server errors and transport failures retain the attempt for reconciliation.
- Successful native-sheet submission opens Status as **confirmation pending**,
  never paid. Unknown outcomes block repeat payment until explicit support
  reconciliation; a cancelled sheet can reopen the same basket's payment.
  Starting another basket requires acknowledging that local clearing cancels
  nothing on the server. No customer status-read/history/tracking endpoint exists.
- Driver: ordered arrival/completion actions require an in-progress route;
  terminal progress/refresh stops GPS, and terminal refresh releases a pending
  stop lock. External directions target only the next actual stop. GPS shows the
  last acknowledged position time and safe reporting errors, not claimed tracking.
- Merchant: API-aligned text/price/volume validation, locked pending create
  drafts and stable retries, busy form/selection locks, canonical saved drafts,
  and billing confirmation messages retained after refresh. Availability changes
  still do not silently submit unsaved item edits.
- Verified **47/47 mobile helper/transport tests**, **16/16 rendered mobile
  workflow acceptance tests**, and TypeScript/whitespace checks. Acceptance loads
  the actual React screen using test-only DOM/native-location/Stripe adapters,
  simulated APIs and installed Chrome. It is not a native layout, accessibility,
  permission or payment certification. No local installs/native builds, backend
  changes, migrations, cloud operations, commits or deployment were performed.
  The existing unprivileged CI gate now includes this suite; CI has not run here.

See [mobile acceptance scope and command](../apps/mobile/acceptance/README.md).
Real identity integration, authorized service E2E and iOS/Android validation
remain required before launch.

## Guided UX implemented on 2026-11-17

- Mobile keeps the existing real Customer, Driver and Merchant API flows.
  Customer now has Browse / Cart / Checkout / Status navigation, an exact-cent
  merchandise summary, automatic progression after catalog/order responses, and
  advanced order IDs behind a disclosure. Empty catalogs and nearby searches
  distinguish not-loaded from a successful empty response. Coordinates and
  required UUIDs are validated before sending; negative longitude is enterable.
- Status deliberately displays the **last basket response**, not a fabricated
  delivery timeline. There is no customer history or live-tracking endpoint wired
  here. Payment-sheet submission still awaits server/webhook confirmation.
- Mobile native Field, ActionButton and StateCard controls are reusable. Cart
  accumulation uses integer BigInt cents; displayed amounts include their currency.
  Merchant subscription amounts come from the billing response rather than a
  hardcoded price. Availability toggles no longer silently submit unsaved edits.
- Driver shows returned stop completion counts and the next pickup/handoff,
  dispatch prerequisites, honest blocked-route guidance and foreground GPS
  permission/lifecycle instructions. No map, route discovery or delivery is faked.
- Merchant setup explains location → catalog → billing and distinguishes an
  unrequested catalog/location list from a returned empty list. Worker shopping
  is explicitly managed by partner store and unavailable as an app workspace.
- Admin opens on a purpose-led overview, not a refund form. Hash navigation and
  active links agree, including direct links and browser history. Inactive panels
  stay mounted so drafts survive navigation; credential changes still abort and
  remount private panels. Operator connection is one shared disclosure, not SSO.
- Finance shows uncalculated/estimated summary cards first. All 29 assumptions
  remain available in four logical disclosures, with detailed rounding policy and
  cost breakdown separately expandable. Results are forecasts, never actual income
  or approved worker pay. Validation identifies the offending assumption.
- Shopper records have Assign / Verify / Review stages, automatic progression
  after successful writes, and distinct idle, busy, empty and error guidance.
  Completion records remain pending review; this UI makes no payments.
- Existing root error boundaries remain in both apps. Recovery clears the
  memory-only session and warns that an uncertain mutation may have completed.

### UX verification

Repeatable follow-up: [frontend acceptance](frontend-acceptance.md) documents the
pinned Playwright suite, isolated simulated API fixtures, local installed-Chrome
command and unprivileged CI gate. It replaces the prior manual-only admin checks
with real click/keyboard tests; it does not validate live gateway security or
payments. Current checks pass **admin browser acceptance 11/11**, **admin helper
tests 33/33**, **mobile helper tests 30/30**, with admin lint/build and mobile
typecheck passing. Browser acceptance ran against installed Chrome, with no local
browser download. Native device validation and the GitHub CI run remain open.

Earlier polish verification (before the repeatable acceptance suite):

Installed dependencies only: **admin 32/32 tests**, **mobile 28/28 tests** (60
total, no skips), admin lint/build and mobile typecheck pass. Nine new behavioral
tests cover real helper behavior: hash routing, idle/empty/busy/error records,
exact-cent totals including large integers, invalid catalog values, UUIDs,
coordinates and invalid dates. Existing cancellation, retry and GPS tests remain.

The running built admin preview was checked at 390px and 1280px with no horizontal
overflow. Simulated in-page fetches verified idle → busy → empty, safe 403 errors
(without upstream HTML), assignment → prefilled verification → pending-review
completion, retained finance drafts across workspaces, and clear-session removal
of credentials/private results. Local/session storage remained empty. Browser
pointer actionability stalled in this editor; these simulated checks used DOM
activation plus real React handlers, so they are not pointer/keyboard E2E proof.
An admin overview screenshot was captured. No real gateway/Stripe calls were
validated by these simulations. Mobile was **not browser-previewed or run on a
device**; native UX is typechecked and its helpers are tested, not visually or
accessibility certified. No installs, Expo exports, native builds, Go builds,
migration edits, backend/cloud changes, commits or pushes were performed.

## Implemented safeguards

- Both apps route requests through their own dependency-free transport. A 15s
  deadline includes response-body parsing. Deadlines and session cancellation
  abort the transport and reject callers even when a transport ignores abort.
  Every outcome clears timers and detaches the external abort listener.
- Requests omit browser cookies, reject redirects, disable cache, and send the
  user bearer token in a header. HTTP errors never display upstream HTML, error
  bodies, credentials or stack traces. Offline, timeout, cancellation, expired
  session, forbidden feature/role, conflict and rate-limit states are distinct.
- No automatic mutation retries. Existing order, basket, catalog, refund,
  finance, shopper assignment/completion and billing-setup keys survive failures
  for unchanged retries. Stop events retain the event ID and timestamp. Editing
  an uncertain mutation requires reconciliation first; a different key is not a
  way to bypass a conflict. No new backend idempotency contract was invented.
- Operator sessions are manual and memory-only, not an OIDC login substitute.
  Clearing/changing tokens or gateways aborts old scopes and clears private
  panels, actor results, drafts and retry keys. Remount keys are numeric revisions,
  never tokens. Neither app persists or logs session tokens.
- Mobile Customer means the buyer, not a worker shopper. Shopper worker records
  remain supervised admin operations; Driver and Merchant are separate workspaces.
  Role selection grants no permissions and clears role-specific private data.
- Location sharing is foreground-only, starts only for a loaded driver route,
  and stops on background, logout, role change and unmount. Late native watcher
  promises are removed, stale callbacks ignored, pending posts cancelled, and
  posts serialized. Failed positions remain eligible for a later report.
- Payment/setup buttons stay busy through native payment-sheet completion, not
  only the preceding HTTP call. Success still awaits backend webhook confirmation.
- Admin provides labelled navigation at narrow widths, focus-visible links,
  accessible error/status messages, disabled busy forms and duplicate-submit locks.

## Release configuration

Use Node 22.16+ (Node's experimental TypeScript stripping is used by tests and
configuration validation). Existing npm dependencies are sufficient.

Admin public gateway configuration is `VITE_API_URL`. `npm run build:release`
validates it before the normal TypeScript/Vite build. The normal `build` remains
available for CI's unconfigured static artifact; it is **not** release approval.
Manual gateway entry remains supported, but every production operation rejects
HTTP and local/private literal endpoints. An invalid configured URL shows a
friendly screen instead of silently falling back.

Mobile requires `EXPO_PUBLIC_API_URL` and
`EXPO_PUBLIC_STRIPE_PUBLISHABLE_KEY` for release. Run `npm run check:release`
with the same public environment used for the native build. Runtime validation
runs before initializing Stripe. Missing/invalid values show a configuration
screen; `sk_`, `rk_` and webhook secrets are never valid publishable keys.
Development can omit payment configuration, but payment controls remain disabled.
Stripe test publishable keys remain allowed for non-live release QA; key shape
validation is not proof of account validity or permission to charge live cards.

Both release checks require a non-placeholder public HTTPS gateway. They scan
public-prefixed environment values for common Stripe secret patterns without
printing values. This is not a general secret scanner. Never place service
tokens, OIDC client secrets or Stripe secret keys in any frontend environment.
The host check is syntactic, not a DNS/public-routing or TLS reachability test;
operators must approve the actual endpoint, DNS and CORS policy separately.

## Verification

- Admin: `npm test`, `npm run lint`, `npm run build`.
- Mobile: `npm test`, `npm run typecheck`; no large dependency installation or
  native build is needed for these checks.
- CI runs all admin and mobile behavioral tests and mobile typechecking. Shared
  transport contract tests exercise both actual per-app implementations with
  fake fetches, deterministic time, cancellation and finally cleanup. Separate
  tests exercise session locking, role-scope transport cancellation, GPS lifecycle,
  gateway-prefix containment and runtime/static release config validation.
- Run each app's `check:release` in the intended release environment. Passing is
  a static configuration prerequisite, never permission to deploy.

### Local verification on 2026-11-17

Existing dependencies were used; no install, native build or Go change was
needed. Admin tests passed 29/29; mobile tests passed 22/22 (51 total, no
skips). Admin lint/build, mobile TypeScript and `git diff --check` passed.
Both release checks failed as intended with required configuration unset.
Browser checks against
the built admin preview verified 390px layout without horizontal overflow,
compact navigation and missing-connection errors. An injected in-page fake
fetch populated refund, finance and shopper records, then verified clear-session
removed all three panels' private results, token and checkout ID; browser local
and session storage stayed empty. This was simulated UI behavior, not gateway
or Stripe E2E. A browser network interception attempt timed out; the real UI
also displayed the expected safe timeout/uncertain-outcome message.

The installed Expo base config uses TypeScript `module: preserve`. CLI
typechecking accepts it; this editor's older JSON schema flags that inherited
value. The native compiler setting was not downgraded just to silence an editor
schema warning. Node also emits a harmless module-type detection warning when
running mobile TypeScript tests; the Expo package module mode was not changed.

## Still required before production

- Complete/approve backend OIDC integration, real sign-in/logout/token refresh,
  actor provisioning, role/ownership checks and permitted gateway routes. Manual
  tokens are an explicit operator/beta workflow, not consumer-grade sign-in.
- Verify SQL/PostGIS migrations and integration suites against the intended
  disposable test database. Frontend tests do not establish database readiness.
- Perform authorized end-to-end customer order/payment/webhook/refund,
  merchant subscription/catalog, driver dispatch/location/stop and supervised
  shopper flows. Default-disabled routes may legitimately return 403; UI does
  not bypass them or claim they are live.
- Validate real iOS/Android permission dialogs, background transitions, native
  Stripe sheet cancellation, process death, screen-reader/keyboard behavior,
  privacy disclosures and device-specific networking. Helper tests and an admin
  browser smoke check are not a native E2E suite or accessibility certification.
- Reconcile financial/worker compliance, live-key approvals and operational
  monitoring; perform release artifact inspection, security review and load tests.
  Native payment sheets may already be visible during logout; aborting a session
  cannot undo a server mutation or dismiss an OS/native sheet already presented.

No backend authentication, payment routes or cloud deployment were changed for
this hardening work. No commit, push or deployment is part of this verification.
