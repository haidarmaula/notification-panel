# Local mobile test issuer

This loopback-only tool issues a short-lived mobile JWT and bounded, fresh
one-use enrollment assertions for an isolated Android smoke test. It is not a production issuer,
Laravel replacement, app-origin verifier, or platform attestation broker.

## Prerequisites

- An existing `ACTIVE` local application with a configured Android `FCM_FID`
  route.
- An existing active local user and `mobile_identities` mapping for the chosen
  namespace and subject.
- An isolated local server and delivery worker. The worker route and address
  protection keys must match the generated mobile policy.
- A test device authorized by the project owner.

The helper does not create, activate, or authorize an application; create users;
insert identity mappings; add a binding route; or configure a sender credential.
It intentionally cannot listen on a LAN address. Use ADB reverse forwarding from
the USB-connected Android device for ports 8080 and 8081.

## Generate local keys and policy

Choose the existing application UUID, identity namespace, and subject that is
already mapped in the local database:

```sh
go run ./devtools/local-mobile-issuer init \
  --dir "$HOME/.config/hello/local-mobile-test" \
  --app-id "<active-local-application-uuid>" \
  --namespace "<mapped-test-identity-namespace>" \
  --subject "<mapped-test-subject>"
```

The new directory and both JSON files are mode `0700`/`0600`. `issuer.json`
contains the Ed25519 private keys. `mobile-policy.json` contains the matching
public keys, address encryption and fingerprint keys, and one Android FID
route. Keep both files outside source control. The generated route defaults to
`hello-local-fcm` / `android-fid`; pass matching values to `--provider-namespace`
and `--provider-route-key` if the worker uses different names.

Set `MOBILE_POLICY_FILE` to the generated mobile policy in the local server
environment. Use the same encryption and fingerprint keys, app ID, Firebase
project ID, provider namespace, and provider route key in the local delivery
worker policy. Keep the sender service-account credential only on the backend
worker host. The worker still requires its fixed protected role policy.

## Run the issuer and connect the app

```sh
go run ./devtools/local-mobile-issuer serve \
  --config "$HOME/.config/hello/local-mobile-test/issuer.json"
```

The tool binds only to `127.0.0.1:8081` or `::1`, prints a random one-use
pairing code to stdout, and accepts at most one mobile session. The pairing code
expires after 10 minutes. The mobile JWT is valid for 10 minutes; each bound
origin assertion is valid for 90 seconds. Up to five distinct assertions can
be issued for that credential so failed enrollment can be retried with a fresh
nonce. Requests, credentials, FIDs, assertions, and proofs are not logged.

With Android USB debugging enabled and the authorized device connected:

```sh
adb reverse tcp:8080 tcp:8080
adb reverse tcp:8081 tcp:8081
```

The React Native smoke app defaults to those loopback URLs. The current app test
uses `/v1/session` with `{ "app_id", "pairing_code" }`, then `/v1/origin` with
the new credential in its Bearer header. ADB reverse forwards the device's
localhost traffic to the host without exposing the issuer on Wi-Fi.
