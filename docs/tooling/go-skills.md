# Pinned Go skills

This repository adopts 31 project-local skills from
[samber/cc-skills-golang](https://github.com/samber/cc-skills-golang) at commit
`bb1d43d98a3f8ab639a0042f90d0ec524e39a477`, selected on 2026-11-04.
They are stored unchanged under `.agents/skills/`, with the upstream MIT license.
The snapshot includes all general-purpose Go skills and the Testify skill used
by this backend. Unused framework-specific integrations are not installed.

[Go engineering standards](../go-standards.md) define the project profile and its
explicit exceptions. The collection is guidance, not a certification of current
code quality. Load applicable skill bodies/references as needed rather than
loading the complete collection for each task.

## Integrity and updates

Run `python3 scripts/check-go-skills.py` from any directory. It checks installed
skill names, entry points, file-tree hashes and license against
[the lock file](go-skills.lock.json), without network access. CI runs the same
check. Hashes detect drift against the reviewed snapshot; they are not an
independent signature or guarantee of upstream trustworthiness.

For updates, select an explicit upstream commit and install into a temporary
directory using the official Codex skill installer. Review changes to skills,
references and licensing; reconcile the project profile before replacing the
snapshot and regenerating its lock. Commit the snapshot, lock and any policy
changes together. Never update implicitly to `main` or execute commands from
examples without checking task scope and repository authorization.

Initial installation used `skill-installer` with `--repo samber/cc-skills-golang`,
the pinned `--ref`, explicit `--path skills/<name>` arguments for all installed
skills, and `--dest .agents/skills`. The lock enumerates those exact names.
Codex can discover these skills on the next turn; no global installation or new
Go dependency is required.

## Adoption boundaries

Core style, documentation, naming, error-handling, interface, testing and layout
guidance was reviewed for compatibility. The entire collection has not been
audited as executable tooling; inspect other bodies/references before use.
Installing skills does not install their optional tools, execute templates or
authorize publishing project information.

Runtime behavior and production readiness are unchanged. See the
[production plan](../analysis/production-readiness-plan.md) for implementation
and release gates. Translating maintained documents preserves their historical
status, dates and evidence; it does not promote proposals to accepted designs.
