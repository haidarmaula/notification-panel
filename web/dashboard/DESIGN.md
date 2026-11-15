# Hello dashboard design contract

The dashboard is an operational workspace for staff preparing, reviewing, and
monitoring push campaigns. The primary flow is to create a draft, review it, and
explicitly request delivery. Display actual API data and preserve empty, loading,
unavailable, and error states; never invent operational metrics.

## Astryx visual system

Use Astryx components, layout primitives, token spacing, semantic typography,
icons, and interaction patterns throughout. Stone is the initial design theme.
Users may select Stone, Y2K, or Matcha through
account settings. Appearance (Light/Dark/System) is independent
of the design theme. Import the generated theme registry once at the app provider.
Use `defineTheme({ extends: importedTheme, ... })` for future customization. Do not
eject a theme unless the user asks for an independent source fork.

AppShell owns global navigation. TopNav holds product identity, the real host,
appearance, and the account shortcut. Theme selection belongs in account settings. SideNav groups application work,
management, administration, and account destinations. Header controls hug their
content and align to the end; do not reserve a fixed-width spacer for them.
Each route owns region padding and scroll containment through Layout or the shared PageFrame; the shell adds no second inset. Headers, content, and footers share one padding token. Tables fill their region;
forms and reading content have bounded widths. Form fields use FormLayout, and save actions hug their content. Preview regions use explicit responsive Grid or Layout budgets. Use rows for record collections,
Token for status, and EmptyState for empty content. Center whole-page empty states
within the available content region; keep filtered-table empty states in the table
region. Use official Icon surfaces rather than unwrapped library icons.

Preserve readable dark-mode surfaces, visible focus, named controls, keyboard
navigation, reduced-motion behavior, and mobile navigation. Confine horizontal
scrolling to actual data tables. Verify narrow viewports at 320px and 390px.
Fonts are self-hosted from Fontsource with Vite aliases for the packaged theme
font stylesheets, retaining the existing BFF Content Security Policy.

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
