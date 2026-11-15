# Astryx migration preview

Approved layout: clean and compact. The preview offers Neutral, Y2K, Matcha, Stone, Butter, Chocolate, and Gothic themes,
with Y2K selected initially. Use the compact header theme menu to switch without
resetting campaign filters, local drafts, or the light/dark appearance. The full dashboard migration from
SolidJS to React is authorized; this first React screen is the visual review
checkpoint before changing production routes and API-connected behavior.

Run `pnpm preview:astryx` with Node >=22.13.0, then open the local URL. Run
`pnpm build:astryx` to type-check and build the preview.

Neutral, Y2K, Matcha, Stone, Butter, Chocolate, and Gothic are imported through the CLI-generated `src/astryx-themes.ts` module once
at the React root. The app uses actual Astryx components and built styles. No
theme fork or token overrides are needed for this initial direction. Future
customization must use `defineTheme({extends: importedTheme, ...})` outside the
generated module; do not eject unless an independent source fork is requested.

Sample records and local draft creation demonstrate layout and interaction only.
Other navigation destinations open explicit preview placeholders until their
production routes are migrated. Appearance settings work inside the preview. No delivery requests or persisted sample data are created.

Before production rollout, preserve the existing auth, grants, stale-version
review, unsaved-change protection, appearance preferences, and API behavior.
The initial preview bundle exceeds the dashboard's 160 KiB gzip JS budget;
resolve this during migration rather than weakening the budget. The Neutral
package loads Figtree from an external font stylesheet; verify availability or
self-host it before rollout.

The revised preview follows Astryx’s bundled AppShellTopNavWithSideNav and
table-page composition. It uses TopNav, SideNav, Layout slots, TabList, Table,
Token, Dialog, EmptyState, and official template Heroicons through Icon.
No Anti-slop or Impeccable guidance was applied to this revision, as requested.
References: https://astryx.atmeta.com/docs/layout and
https://astryx.atmeta.com/docs/migration.

With seven themes, the header uses a compact Astryx DropdownMenu with a radio
group for the active theme. Header controls use intrinsic widths and end
alignment; no fixed-width header spacer or horizontal scroller is needed.
