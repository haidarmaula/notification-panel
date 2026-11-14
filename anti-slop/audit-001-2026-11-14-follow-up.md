# Dashboard refinement follow-up

Date: 2026-11-14 (Asia/Jakarta)
Original audit: `audit-001-2026-11-14.md`
Authorization: the user approved fixing all five numbered findings.

## Findings addressed

1. Compact tables: desktop cells use 12px vertical padding instead of 18px.
   Campaign status/action columns have fixed anchors; title width adapts to the
   remaining space. Measured desktop rows are 68px in the inspected fixture,
   compared with approximately 80px before. Long titles can increase row height.
2. Consistent records: overview and template names share the semibold campaign
   title treatment. Technical IDs use compact monospace metadata. No API fields,
   per-row requests, factual copy, or metrics were introduced.
3. Content-aware panels: overview has a separate panel heading and flush table
   body. Preparation destinations form a compact navigation list with semantic
   icons, descriptions, and chevrons. Links retain existing destinations.
4. Stronger hierarchy: section/content headings are 18px; canvas, panel and header
   surfaces are more distinct in dark mode. The forest/off-white identity stays.
5. Sparse-list layout: populated campaign lists fit their actual content, with
   pagination inside the bordered panel. Loading and empty states preserve their
   existing reserved height. The loading child accounts for the new 2px parent
   border, retaining footer stability. Heading/footer spacing is reduced.

The updated design decisions are recorded in `web/dashboard/DESIGN.md`.

## Verification

- Production build passed with the project-pinned Node 22.23.4.
- ESLint and formatting checks passed for changed frontend files.
- Unit tests: 8 passed across 3 files. The localhost API-docs fixture required
  execution outside the restricted network sandbox.
- Four focused Playwright tests passed: mobile list/navigation, appearance
  persistence, narrow management pages, and loading/empty footer stability.
- Full Playwright suite: 35 passed, including login, editor, results, settings,
  responsive navigation, and draft protection. The existing login canvas-color
  assertion was updated to the intentional new dark canvas value; no assertion
  was removed or relaxed. Log: `/tmp/hello-ui-full-e2e.log`.
- Full dashboard formatting check passed. Initial JavaScript measured 62.9 KiB
  gzip against the 160 KiB budget.
- Independent source review found no actionable correctness, accessibility,
  or request-pattern regressions.
- Browser screenshots cover Campaign, Overview and Templates in both themes at
  1440px, 390px and 320px: 18 combinations, no document-level horizontal overflow.
- A separate 110-character campaign title at 320px wrapped without page overflow.
- Sampled ordinary-text contrast measured 4.70-12.26 in light mode and 5.95-11.83
  in dark mode. These samples are not a full WCAG conformance audit.
- Preparation links showed a 3px keyboard focus ring in both themes and visible
  hover backgrounds. The isolated fixture server was stopped after review.
- Evidence is temporary: `/tmp/hello-ui-after/measurements.json`,
  `/tmp/hello-ui-after/contrast-focus.json`, and screenshots in that directory.
- Impeccable's automated detector remains unavailable for the engine/cache
  limitation documented in the original audit. Manual/rendered checks were used.

## Delivery gate

- PASS, R-03/R-34/C-4: both themes fit all three reviewed viewport widths;
  long-title wrapping is verified.
- PASS, R-17/R-36/R-38: displayed data remains API-backed; no invented content.
- PASS, R-04/R-07/R-14/R-31: icon purpose, surface roles, panel structure and
  spacing decisions are recorded above and in the design contract.
- PASS, R-26/R-32/R-39: semantic links retain their destinations; workflow hover
  and keyboard focus are verified; existing mobile action targets remain 44px.
- PASS, R-35/C-5: built result inspected, measured evidence recorded, and
  limitations disclosed. This refinement does not establish real-provider
  integration or whole-product accessibility compliance.
