# Hello dashboard design contract

Audience: staff preparing and reviewing push campaigns. The main action is
creating a draft, followed by explicit review before sending. Operational data
must be accurate, including empty and unavailable states.

Use a warm off-white workspace, dark forest navigation, restrained green actions,
tabular numerals and compact editorial headings. Campaigns belong in readable
rows; do not add decorative statistic cards, gradients, promotional heroes or
fabricated charts. Icons support labels. Strong focus rings, named form controls,
inline errors and reduced-motion support are mandatory.

Desktop: persistent 232px navigation, application context in the header and a
content column with a useful line length. Narrow screens: collapse navigation
into a labeled menu, stack the editor and preview, and retain campaign title,
status and detail access. Restrict overflow to actual data tables.

Consulted references: https://github.com/miqdadbadjuber/anti-slop and
https://impeccable.style/docs/. Apply their content-first hierarchy, purposeful
decoration and browser-review principles without installing external agent rules.

Performance: route-split detail/editor/results; no chart runtime on campaign list.
Initial production JS budget: 160 KiB gzip, measured from emitted files and
reported with test conditions. This is a proposed engineering budget, not a
claim about measured real-user performance. Do not cache private data on disk.
