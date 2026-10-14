# Scrum Process

How this repository runs planning, delivery, and review. Adapted for a solo developer or small team (1–3 people).

---

## Rhythm

| Event | When | Duration |
|---|---|---|
| Sprint Planning | Start of each sprint | 30 min |
| Daily check-in | Optional, async (comment in issue) | — |
| Sprint Review | End of sprint | 30 min |
| Sprint Retrospective | After review, same day | 20 min |

**Sprint length: 1 week** (solo) or **2 weeks** (small team).

---

## Milestones = Releases

Each planned release is a GitHub Milestone.

**Naming:** `vX.Y.Z` (e.g., `v1.13.2`), matching OpenTofu-compatible releases. `Sprint 1`–`Sprint 4` remain as closed, historical milestones.

An open version milestone holds the issues planned for that release; it closes when the release is published.

---

## Labels

Issues use GitHub's default labels, plus `area/*` for the affected subsystem and `stale` (managed automatically):

| Label | Use for |
|---|---|
| `enhancement` | New functionality or improvements |
| `bug` | Something broken that needs fixing |
| `documentation` | Documentation only |
| `area/*` | The affected subsystem |

`dependencies`, `github_actions`, `duplicate`, `good first issue`, `help wanted`, `invalid`, `question`, and `wontfix` are applied when relevant.

No status labels: work in progress is tracked by the branch name (`issue-N/short-description`).

---

## Sprint Planning checklist

1. Review the backlog — pick `bug` fixes and valuable `enhancement`s
2. Ensure every picked issue has acceptance criteria
3. Assign each selected issue to the version milestone

## Sprint Review checklist

1. Close all completed issues (reference commit or PR in the closing comment)
2. Move incomplete issues back to the backlog (remove milestone)
3. Publish a GitHub Release if there is a usable increment

## Sprint Retrospective

Create an issue titled `Retrospective: Sprint N` and answer:

- What went well?
- What could be improved?
- Action items for the next sprint (as checkboxes)

---

## Backlog rules

- Every issue needs a description and acceptance criteria before it becomes sprint-ready
- Issues tied to a subsystem get an `area/*` label
- Assign a version milestone only when the issue is planned for a release
