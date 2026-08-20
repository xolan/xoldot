# Issue tracker: GitHub

Issues and specs for this repo live as GitHub issues. Use the `gh` CLI for all operations.

## Conventions

- **Create an issue**: `gh issue create --title "..." --body "..."`. Use a heredoc for multi-line bodies.
- **Read an issue**: `gh issue view <number> --comments`, filtering comments by `jq` and also fetching labels.
- **List issues**: `gh issue list --state open --json number,title,body,labels,comments --jq '[.[] | {number, title, body, labels: [.labels[].name], comments: [.comments[].body]}]'` with appropriate `--label` and `--state` filters.
- **Comment on an issue**: `gh issue comment <number> --body "..."`
- **Apply or remove labels**: `gh issue edit <number> --add-label "..."` or `--remove-label "..."`
- **Close an issue**: `gh issue close <number> --comment "..."`

Infer the repository from `git remote -v`. `gh` does this automatically inside a clone.

## Pull requests as a triage surface

**PRs as a request surface: no.** Set this to `yes` if the repo treats external pull requests as feature requests. The `/triage` skill reads this flag.

When set to `yes`, pull requests use the same labels and states as issues:

- **Read a pull request**: `gh pr view <number> --comments` and `gh pr diff <number>`.
- **List external pull requests**: `gh pr list --state open --json number,title,body,labels,author,authorAssociation,comments`, then keep `CONTRIBUTOR`, `FIRST_TIME_CONTRIBUTOR`, and `NONE` associations.
- **Comment, label, or close**: use `gh pr comment`, `gh pr edit`, or `gh pr close`.

GitHub shares one number sequence across issues and pull requests. For a bare reference such as `#42`, try `gh pr view 42`, then fall back to `gh issue view 42`.

## When a skill says "publish to the issue tracker"

Create a GitHub issue.

## When a skill says "fetch the relevant ticket"

Run `gh issue view <number> --comments`.

## Wayfinding operations

The `/wayfinder` skill uses one map issue with child issues as tickets.

- **Map**: an issue labelled `wayfinder:map` with Notes, Decisions-so-far, and Fog sections. Create it with `gh issue create --label wayfinder:map`.
- **Child ticket**: an issue linked to the map as a GitHub sub-issue through the sub-issues API. If sub-issues are unavailable, add the child to a task list in the map body and put `Part of #<map>` at the top of the child body. Apply a `wayfinder:<type>` label using `research`, `prototype`, `grilling`, or `task`. Assign the ticket to the driving developer once claimed.
- **Blocking**: use GitHub issue dependencies. Add an edge with `gh api --method POST repos/<owner>/<repo>/issues/<child>/dependencies/blocked_by -F issue_id=<blocker-db-id>`. Fetch the blocker database ID with `gh api repos/<owner>/<repo>/issues/<n> --jq .id`. If dependencies are unavailable, add `Blocked by: #<n>, #<n>` at the top of the child body.
- **Frontier query**: list the map's open children. Remove assigned tickets and tickets with open blockers. The first remaining ticket in map order wins.
- **Claim**: run `gh issue edit <n> --add-assignee @me`. This is the session's first write.
- **Resolve**: comment with the answer, close the ticket, then add a short context pointer and link to the map's Decisions-so-far section.
