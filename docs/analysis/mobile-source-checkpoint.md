# First mobile consumer source checkpoint

On 2026-11-08 the documented React Native repository `kaiapps2`, branch
`feat/notifications`, resolved to `f49e85a018c138b1a7a656a1b02e8e5fb69b7bdb`.
The inspection used Git objects from that revision. No client files, provider
accounts or device registrations were changed.

| Source at that revision | Finding | Consequence for a pilot |
| --- | --- | --- |
| `package.json` | React Native 0.81.5, Expo ~54.0.36, react-native-onesignal ^5.4.5 and react-native-keychain ^10.0.0 are declared. | Verify physical-device release builds, address acquisition and protected installation proof storage on both platforms. |
| `src/app/App.tsx`, `src/notification/OneSignalService.ts` | The client initializes OneSignal. Its click handler logs the entire event using JSON.stringify. | Redact click data before adding any receipt capability to a client payload. Demonstrate that a pilot installation cannot receive the same campaign through both providers. |
| `ios/Podfile`, `ios/OneSignalNotificationServiceExtension/` | The Podfile defaults the minimum iOS target to 15.1. The OneSignal service extension is present. | Verify the built target, APNs entitlements, permission states, extension interactions and tap behavior in the selected Firebase environment. |
| Android Google services configuration filenames | Configuration files are present. Their contents and credentials were not needed for this inspection. | The rollout owner must confirm package/project binding, signaling permissions and quota against the installed test build. |
| Native and JS source search | A search for the modern installation and receipt paths, registration-version/owner-epoch fields and the React Native Firebase Messaging package found no implementation in the inspected source. | Enroll and report on the real app before activating coverage. The Laravel issuer and one-use origin broker must also present deployment evidence for their agreed claims. |

The backend's isolated clients already exercise project-specific JWTs, invalid
proofs, conflicts, refresh, account transfer, logout and report deduplication.
The release checklist still requires issued credentials, signed Android/iOS
builds, approved test devices and a measured pilot. Foreground, background,
terminated, offline recovery, account switching and second-device behavior
belong in the device evidence record. See the [installation API](../api/mobile-installations-api.md),
[receipt API](../api/mobile-receipts.md) and [cutover register](cutover-readiness.md).
