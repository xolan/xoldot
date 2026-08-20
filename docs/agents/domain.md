# Domain docs

Read this repository's domain documentation before exploring code.

## Read before exploring

- Read `CONTEXT.md` at the repository root.
- Read ADRs under `docs/adr/` that affect the area being changed.

If these paths do not exist, proceed without raising their absence. The `/domain-modeling` skill creates them when the project resolves terms or architectural decisions.

## File structure

This repository uses a single-context layout: one `CONTEXT.md` and one
`docs/adr/` directory at the repository root.

## Use glossary terms

Use terms from `CONTEXT.md` when naming domain concepts in issues, proposals, hypotheses, and tests. Do not replace defined terms with synonyms.

If a needed concept is missing, reconsider whether the project uses that language. Record a real vocabulary gap for `/domain-modeling`.

## Flag ADR conflicts

Call out any proposal that contradicts an existing ADR. Name the ADR and explain why the decision should be reconsidered.
