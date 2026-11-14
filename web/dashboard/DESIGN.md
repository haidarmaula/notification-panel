# Hello dashboard design contract

Audience: staff preparing and reviewing push campaigns. The main action is
creating a draft, followed by explicit review before sending. Operational data
must be accurate, including empty and unavailable states.

Use a warm off-white workspace, dark forest navigation, restrained green actions,
tabular numerals and compact editorial headings. Campaigns belong in readable
rows; do not add decorative statistic cards, gradients, promotional heroes or
fabricated charts. Icons support labels. Strong focus rings, named form controls,
inline errors and reduced-motion support are mandatory.

Desktop: hideable 232px navigation, application context in the header and a
content column with a useful line length. Narrow screens: collapse navigation
into a labeled menu, stack the editor and preview, and retain campaign title,
status and detail access. Restrict overflow to actual data tables.

Appearance supports Light/Dark/System with semantic surface/text/status colors.
Persist only user-selected appearance and sidebar preferences. The header shows
the actual host outside loopback; do not infer production/staging from an IP.
Operational navigation includes templates, manual segments, audience/device
inspection, campaign-scoped logs, settings and a recent-campaign overview. Add
metrics or filters only when the API supplies their real scope and values.

Consulted references: https://github.com/miqdadbadjuber/anti-slop and
https://impeccable.style/docs/. Project-local copies are installed under
`../../.agents/skills/`; dashboard routing is documented in `AGENTS.md`.

Refinement direction: ENERGY 1 / RHYTHM 2 / MOTION 1. Use compact record rows
with semibold names and monospace identifiers, keeping status and actions aligned.
Identifiers are technical metadata; ordinary body copy stays in the system sans.
Use distinct canvas, panel, and header surfaces in both themes. Content panels
separate heading and body; preparation links form a compact navigation list with
semantic destination icons rather than three oversized cards. Keep action targets
at least 40px on desktop and 44px on mobile. Campaign loading and empty states
reserve stable space, while populated lists fit their actual content. Do not add
per-row requests or invent metadata beyond the list API's title, ID, and status.

Administration stays separate from the granted application workspace: registry
access does not imply business access. Keep credential actions in dedicated forms,
show destructive targets before confirmation, and require explicit review after
a stale resource version. Large staff directories may scroll inside their table;
the page itself must fit a 320px viewport, including screen-reader-only labels.

Performance: route-split detail/editor/results; no chart runtime on campaign list.
Initial production JS budget: 160 KiB gzip, measured from emitted files and
reported with test conditions. This is a proposed engineering budget, not a
claim about measured real-user performance. Do not cache private data on disk.
