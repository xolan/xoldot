# Repository instructions

## Documentation

- Keep every document synchronized with the codebase. Changes to commands,
  configuration, behavior, constraints, setup, tests, or development workflows
  must update every affected document in the same change.
- Before completing a change, compare its behavior with every document that
  describes it. Make the documents agree with the codebase and with one another.
- Resolve contradictory documentation as part of the current change. Prefer one
  authoritative explanation and link to it when repeating the details would
  create another copy to maintain.

## Agent skills

### Issue tracker

Track issues and specs in GitHub Issues using `gh`. See `docs/agents/issue-tracker.md`.

### Triage labels

Use the five canonical triage labels without aliases. See `docs/agents/triage-labels.md`.

### Domain docs

Use the single-context layout with `CONTEXT.md` and `docs/adr/` at the repository root. See `docs/agents/domain.md`.
