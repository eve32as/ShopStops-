# Mobile native build and device checks

The mobile app is an Expo project in `apps/mobile`. JavaScript bundle exports
and TypeScript checks do not produce signed native apps or prove device
behavior. Native binaries are built remotely by EAS; device installation and
hands-on testing remain owner-managed release gates.

## Configure EAS builds

1. Create or select the ShopStops project in the authorized Expo account and
   link it to the app. Copy its project ID.
2. Create the GitHub Actions environments `mobile-build-preview` and
   `mobile-build-production`. Set `EAS_PROJECT_ID` as an environment variable
   and `EXPO_TOKEN` as a secret in each. Use a token scoped to the required
   Expo project/account, and require reviewers for the production environment.
3. If native Google Maps should be enabled, set
   `EXPO_PUBLIC_GOOGLE_MAPS_API_KEY` in the environments. This value is bundled
   into the app; restrict it to the ShopStops app identifiers and required Maps
   APIs. Do not put backend secrets in Expo public variables.
4. Configure iOS and Android signing credentials in EAS Credentials. For
   internal iOS builds, register the testers' device UDIDs and regenerate the
   provisioning profile as needed. Internal Android builds use an installable
   APK. Keep signing materials in EAS, not in this repository or workflow logs.
5. Dispatch **Mobile EAS build** from GitHub Actions. Choose `preview` for
   internal testing or `production` for store-distribution binaries. Production
   builds are not automatically submitted to an app store. EAS reports the
   build page and download links when the workflow runs.

The CLI used by CI is pinned in `.github/workflows/mobile-eas-build.yml`; update
that version deliberately and review its advisories when changing it.

## Device smoke checks

Test each platform's actual signed binary on supported physical devices before
release. Record device model, OS version, app/build version, test account role,
and results. At minimum verify:

- Fresh install, launch, upgrade, and clean sign-out/relaunch behavior.
- OIDC sign-in and recovery, secure session persistence, expiration, and logout.
- Shopper catalog/order flow and payment success, failure, and pending states
  against the approved test backend.
- Driver assignment, foreground/background location permission prompts, route
  updates, stop progression, and recovery after network loss or app restart.
- Map rendering and location behavior on cellular and Wi-Fi, including denied
  and revoked permissions.
- Accessibility basics, app lifecycle interruptions, and useful error handling
  on both platforms.

Do not use production customer or payment data for test runs. A green EAS build
or successful simulator/bundle export is not a substitute for these checks.
