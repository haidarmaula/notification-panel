# Dashboard UI consistency audit

Date: 2026-11-16

## Resolution

The findings below describe checkpoint `a8356a2`; their original line numbers
refer to that source snapshot. The subsequent implementation addresses all
twelve findings:

| Finding | Resolution                                                                                                              |
| ------- | ----------------------------------------------------------------------------------------------------------------------- |
| 1       | Campaign editor bounds its body only; shared named header regions align with PageFrame.                                 |
| 2       | Campaign editor, detail, and template editor share AndroidNotificationPreview with context-specific guidance.           |
| 3–5     | Shared CreateAction/SubmitAction/BackAction define icons and hierarchy; write actions expose loading state.             |
| 6–7     | The long editor keeps its intentional sticky footer; both back actions use the same label and presentation.             |
| 8       | Pagers hide during loading/error and on initial empty data, retaining return navigation on later empty pages.           |
| 9       | All record tables use balanced density and hover; readable column budgets and secondary identity metadata are retained. |
| 10      | DESIGN.md specifies spacing by relationship; section and preview composition now follow those rules.                    |
| 11      | Appearance values have display labels and unknown status labels use readable words.                                     |
| 12      | Destination title is My account; the two intentional navigation entry points and desktop theme selector are documented. |

Existing browser coverage and the new consistency suite check the changed
patterns, including narrow layouts, three themes, long notification content,
header alignment, pending operations, and existing safety guards. Validation
results are recorded in the implementation handoff; fixture tests do not prove
production behavior on real devices.

Final validation: production build, ESLint, Prettier, 11 unit tests, and all 60
browser tests passed. Initial JavaScript is 138.6 KiB gzip against the 160 KiB
budget. Astryx doctor reports zero failures and three warnings: theme font proof
checks and the documented device-simulation styling exception. Desktop and
mobile screenshots from the consistency suite were visually inspected.

## Scope and evidence

Read-only source audit of the dashboard shell, shared UI, login, campaign list,
editor and detail, results, overview, audience/devices, segments, templates,
settings/password, upload history, audit, staff, registry, and onboarding.
Existing screenshots from this session informed the review. This audit did not
run a new browser matrix, accessibility audit, or CI suite. Responsive risks below
are source findings, not claims of reproduced browser failures. Application code
was not changed by the audit.

## Findings

### 1. High: campaign editor bypasses the shared header alignment rule

`web/dashboard/src/features/campaigns/Editor.tsx:244` supplies `contentWidth={1120}`
to its own Layout. Unlike PageFrame, this constrains the header and footer too.
`src/components/ui/PageFrame.tsx:54` now limits only the body. The editor can still
shift toward the center on a wide screen after the other forms were fixed.
Use the same full-width header invariant while retaining the editor's sticky
footer and bounded two-column body.

### 2. High: the same notification has three preview presentations

Editor uses AndroidNotificationPreview (`campaigns/Editor.tsx:521`), campaign detail
uses headings in a generic Card (`campaigns/Detail.tsx:221`), and template editor
uses another generic Card (`management/Templates.tsx:185`). Creating, reviewing,
and preparing the same message therefore use different visual representations.
Share the notification rendering; keep draft/template workflow guidance outside
the simulated device, with wording appropriate to each context.

### 3. Medium: identical create actions use different icon policies

Create campaign has a Plus in `campaigns/List.tsx:69`, but the exact same action
has none in `management/Overview.tsx:43`. Create segment, Create template, and
Add staff likewise omit the icon (`Segments.tsx:38`, `Templates.tsx:40`,
`admin/Staff.tsx:108`). Adopt the approved Plus-for-create/add rule across header
and empty-state actions. Do not require icons on every button.

### 4. Medium: primary form submissions do not share visual hierarchy

Staff Set password (`admin/Staff.tsx:555`) omits variant and therefore uses the
secondary default. Staff account save and the account PasswordForm explicitly use
primary. Classify the main action per form; ordinary submit actions should use
the same primary treatment. Classify hold/deactivation separately according to
their consequences rather than blindly making every operational action primary
or destructive.

### 5. Medium: pending submissions have different feedback

Settings, segments, templates, and account password pass `isLoading` to submits;
staff account save (`admin/Staff.tsx:460`) and campaign Save draft
(`campaigns/Editor.tsx:275`) change the label/disable the control without that
loading state. Onboarding save actions have another implementation. Standardize
the visual busy state without changing uncertain-outcome blocking or retry rules.

### 6. Medium: page form actions use two placement models

Most forms put submits directly below their fields. Campaign editor uses a sticky
LayoutFooter containing Back and Save draft (`campaigns/Editor.tsx:265`). A sticky
footer is useful for this long editor, but the exception needs a defined rule and
consistent content alignment, action order, and mobile wrapping. Its form bounds
must not silently govern header placement.

### 7. Medium: back navigation has multiple presentations

PageFrame renders a small ghost button labelled Back to templates/segments or
All campaigns (`PageFrame.tsx:29`). Editor renders a Link with normal Text above
an uppercase NEW CAMPAIGN/DRAFT eyebrow (`Editor.tsx:250`), and repeats Back in
its footer. Standardize the back affordance and naming; preserve repeated footer
navigation only if intentionally useful for long forms.

### 8. Medium: pagination placement and empty-state behavior vary

Campaign list places pagination in a sticky footer (`campaigns/List.tsx:79`);
management lists use shared inline Pager (`management/shared.tsx:8`); staff and
registry implement their own rows. Staff hides pagination when no rows exist
(`admin/Staff.tsx:186`), Segments renders it after EmptyState
(`management/Segments.tsx:72`), and Registry renders it outside QueryState
(`admin/Registry.tsx:183`), including loading/error states. Define consistent
placement and visibility while preserving distinct cursor and numbered APIs.
Keep Previous available on an empty later page when returning is necessary.

### 9. Medium: table composition and density are configured per page

Staff/registry/campaign list use data-driven columns and explicit column budgets;
overview, audience, templates, and segments use composed TableHeader/TableCell
markup. Two APIs are supported, but there is no shared declaration for density,
identity columns, action widths, ID wrapping, or row metadata. This is a recurring
maintenance risk, not proof that every current table clips. Standardize table
presentation rules and inspect long labels/IDs at narrow widths before changing
individual tables.

### 10. Medium: spacing is locally chosen rather than tied to relationships

PageFrame body gap is 6, detail body/grid gaps are 5, editor sections use 6/4,
template grid gap is 6, and campaign grid now uses columnGap 8 / rowGap 6.
Valid tokens alone do not establish consistent rhythm. Define roles for field
groups, section heading-to-content, related controls, separate sections, and
parallel columns. Preserve compact spacing inside tables and the intentional
Android OS simulation; do not normalize every gap to a single value.

### 11. Low: display labels leak implementation values

Settings Appearance selector uses raw `system/light/dark` option strings
(`management/Settings.tsx:69`), while header/theme labels are human-readable.
Shared Status falls back to raw enum text (`components/ui/Feedback.tsx:81`), so
unmapped values can display differently from mapped statuses. Use explicit
display labels for supported values and retain honest fallback for unknown data.

### 12. Low: navigation labels and preference descriptions have drifted

My account appears both in SideNav and the footer account menu
(`Workspace.tsx:155`, `Workspace.tsx:227`), but opens a page titled Settings.
DESIGN.md still initially describes theme selection through account settings even
though desktop now exposes it in the header too. Decide whether the duplicate
entry is intentional, align the destination title, and update the contract.

## Preserve intentional differences

- Login is an authentication surface, not a dashboard page.
- Tables use broad regions; single-column forms use a bounded width; previews
  need a larger two-column budget. Current Registry create form is already
  bounded to 720 and is not a width defect.
- Destructive confirmation dialogs and wizard navigation can use end alignment.
- Mobile navigation is a drawer and desktop navigation can become an icon rail.
- Disabled self-account actions are required behavior, not visual errors.
- Android notification chrome intentionally has device-specific styling.
- Date formatters inspected use the same en-GB medium/short convention; duplicated
  helpers alone do not establish a visible inconsistency.

## Recommended implementation sequence

1. Establish shared page/header and action contracts; fix the editor exception.
2. Define reusable create actions, submit rows, pending feedback, and back links.
3. Unify notification rendering across editor, detail, and template workflows.
4. Standardize pagination states and table presentation without changing API
   pagination contracts.
5. Apply spacing and copy rules across each route; inspect desktop and 320/390px
   layouts, all three themes, long text, empty/loading/error and disabled states.

Completion requires a route/state checklist and browser evidence. Build, lint,
and type checks alone cannot establish visual consistency.
