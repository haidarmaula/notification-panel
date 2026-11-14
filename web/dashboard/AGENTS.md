# Dashboard UI work

For UI work in this directory, use the project-local Impeccable skill at
`../../.agents/skills/impeccable/SKILL.md` for design guidance and the Anti-slop
skill at `../../.agents/skills/antislop/SKILL.md` as a quality filter. Load only
the relevant sections and companion references as the task requires.

Read the existing `DESIGN.md` before changing the interface. Preserve established
product behavior, accessibility, and responsive layouts. Resolve Anti-slop's
usage mode before starting UI changes, following its documented preference and
session rules.

These skills are installed locally for this repository, not globally. Upstream
sources:

- Anti-slop: https://github.com/miqdadbadjuber/anti-slop
  (`skills/antislop` and its five companion skills).
- Impeccable: https://github.com/pbakaus/impeccable
  (`.agents/skills/impeccable`, the Codex build).

This is a skill-only installation; automatic detector hooks are not configured.
