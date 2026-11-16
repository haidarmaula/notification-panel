# Hello dashboard design contract

The dashboard is an operational workspace for staff preparing, reviewing, and
monitoring push campaigns. The primary flow is to create a draft, review it, and
explicitly request delivery. Display actual API data and preserve empty, loading,
unavailable, and error states; never invent operational metrics.

## Astryx visual system

Use Astryx components, layout primitives, token spacing, semantic typography,
icons, and interaction patterns throughout. Stone is the initial design theme.
Users may select Stone, Y2K, or Matcha through the desktop header or
account settings. Appearance (Light/Dark/System) is independent
of the design theme. Import the generated theme registry once at the app provider.
Use `defineTheme({ extends: importedTheme, ... })` for future customization. Do not
eject a theme unless the user asks for an independent source fork.

AppShell owns global navigation. TopNav holds product identity and the real host.
The TopNav exposes the Stone/Y2K/Matcha design theme selector beside an icon button
that toggles Light/Dark; system color mode remains available in account settings. The
SideNav footer owns the desktop collapse toggle and account menu.
The account trigger shows an avatar, name, and email when expanded, and an avatar
with a tooltip when collapsed. Account settings and sign-out live in that menu.
SideNav groups application work,
management, administration, and account destinations. Header controls hug their
content and align to the end; do not reserve a fixed-width spacer for them.
Each route owns region padding and scroll containment through Layout or the shared PageFrame; the shell adds no second inset. Headers, content, and footers share one padding token. Tables fill their region;
Page headers use the full available width and align to the left content inset.
Forms and reading content have bounded widths and start at that same inset.
Form fields use FormLayout, and page or inline form submit actions align left with their inputs. Dialog confirmations and wizard step navigation keep their contextual alignment. Preview regions use explicit responsive Grid or Layout budgets. Use rows for record collections,
Token for status, and EmptyState for empty content. Center whole-page empty states
within the available content region; keep filtered-table empty states in the table
region. Use official Icon surfaces rather than unwrapped library icons. The campaign editor’s Android notification simulation may use a scoped CSS module and fixed neutral OS colors for device chrome only; keep its content in the campaign editor data flow and leave the surrounding dashboard theme untouched.

Preserve readable dark-mode surfaces, visible focus, named controls, keyboard
navigation, reduced-motion behavior, and mobile navigation. Confine horizontal
scrolling to actual data tables. Verify narrow viewports at 320px and 390px.
Fonts are self-hosted from Fontsource with Vite aliases for the packaged theme
font stylesheets, retaining the existing BFF Content Security Policy.

## Shared interface patterns

Use CreateAction for create/add entry points (primary with a leading Plus),
SubmitAction for the primary write of each form, and BackAction for parent-page
navigation. Save actions use text and the loading indicator without a decorative
check icon. Secondary search/filter/lookup submits remain secondary. Pending
writes use Astryx isLoading as well as the existing disabled/uncertain guards.
Operational holds and activate/deactivate remain secondary; deletion remains
explicitly destructive and retains confirmation.

All page headers start at the main left inset. Bound only the body to 720 for
single-column forms or 1120 for form-and-preview compositions. The long campaign
editor retains its sticky action footer and a repeated parent-navigation action.
Use the same Back to <destination> label and ghost treatment in both positions.

Use gap 1 for title/description pairs, gap 2 for related actions, gap 4 for
section-heading/content groups, and gap 6 between independent sections. Use
columnGap 8 and rowGap 6 for parallel form/preview regions. Field spacing belongs
to FormLayout. Dense metadata and the Android simulation have their own internal
rhythm. Do not stretch standalone tokens or action buttons in vertical stacks.

Record tables use balanced density and row hover. Identity is a readable name
with a supporting secondary ID; action columns retain explicit readable budgets
in data-driven tables. Both supported Table APIs are valid. Keep overflow inside
the table, preserve ID precision, and use the same Status display mapping.

Pagination is placed after its records, with the page summary before Previous /
Next. The long campaign list may pin that row to its footer. Hide it during
loading/error and on a first empty page; keep Previous available for an empty
later page. Cursor and numbered pagination retain their own API contracts.

Use AndroidNotificationPreview in campaign creation, review, and template editing.
Its context selects the workflow guidance; it never sends or saves data. My
account is the destination title in both navigation and the page header. The
sidebar link and footer-menu shortcut intentionally provide two entry points to
that same destination.

## React and data behavior

Use React 19, TypeScript strict mode, React Router, and React Query. Keep local
form state in hooks, effects for external synchronization, cleanup for timers and
requests, stable list keys, and explicit typed component boundaries. Comments,
maintained documentation, and interface copy are English. Comments explain
non-obvious constraints rather than narrating the implementation.

Persist only explicitly selected theme, appearance, and sidebar preferences.
Never persist account data, credentials, private keys, or campaign content in
browser storage. Keep same-origin BFF requests, CSRF, 401 handling, query scope,
ETag conditions, idempotency, uncertain-outcome blocking, unsaved-change prompts,
and protected operational actions intact.

Administration remains separate from granted application workspaces. Registry
access does not imply business access. Confirm destructive targets explicitly,
show stale revisions for review, and clear credential inputs after success or
unmount. Only drafts may be edited or deleted. Provider acceptance does not prove
device delivery. A held send must retain the saved draft.

Route-split large feature pages and avoid chart runtimes on the campaign list.
The proposed initial production JavaScript budget remains 160 KiB gzip, measured
from emitted assets. Report any regression honestly; do not raise the limit or
claim real-user performance from a unit or fixture test.

Production theme choices are limited to Stone (default), Y2K, and Matcha.
The separate preview retains all seven themes in `preview/astryx-themes.ts`.

The login page follows the bundled Astryx Login Split composition: a responsive
Card with horizontal `hello.` branding, a vertically centered form on the left,
and the official template photograph on the right. Below 768px, the photograph
is omitted so credentials remain the focus. Preserve the real BFF login flow
and the joined InputGroup password visibility control.
