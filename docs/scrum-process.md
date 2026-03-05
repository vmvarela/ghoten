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

## Milestones = Sprints

Each sprint is a GitHub Milestone.

**Naming:** `Sprint N` (e.g., `Sprint 1`, `Sprint 2`)

**Sprint Goal format** (milestone description):
```
Sprint Goal: <one sentence describing the most valuable outcome of this sprint>
```

Example:
```
Sprint Goal: Establish a working Scrum loop in GitHub with labels, templates, and sprint hygiene.
```

The goal must be short enough to fit in the milestone description and answer: *"Why is this sprint worth running?"*

---

## Labels in the development workflow

### Type — what kind of work it is

| Label | Use for |
|---|---|
| `type:feature` | New functionality |
| `type:bug` | Something broken that needs fixing |
| `type:chore` | Maintenance, refactoring, tooling |
| `type:spike` | Timeboxed research or investigation |
| `type:docs` | Documentation only |

### Priority — when to pick it up

| Label | Meaning |
|---|---|
| `priority:critical` | Must fix immediately — blocks everything |
| `priority:high` | Must be in the next sprint |
| `priority:medium` | Should be done soon |
| `priority:low` | Nice to have; pick up when capacity allows |

### Size — relative effort estimate

| Label | Rough effort |
|---|---|
| `size:xs` | < 1 hour |
| `size:s` | 1–4 hours |
| `size:m` | 4–8 hours |
| `size:l` | 1–2 days |
| `size:xl` | > 2 days — **split before starting** |

### Status — where the issue is right now

Apply **one status label** at a time. Change it as the issue moves through the workflow:

| Label | Apply when… |
|---|---|
| *(none)* | Issue is in the backlog, not yet refined |
| `status:in-progress` | Work has started (branch created, actively coding) |

**Workflow:**

```
Backlog → [assign to sprint] → status:in-progress → [PR merged / done] → closed
```

Remove the status label when closing. An issue with no open status label + closed state = done.

---

## Sprint Planning checklist

1. Review the backlog — pick issues with `priority:high` or `priority:medium`
2. Ensure every picked issue has acceptance criteria and a size label
3. Set the Milestone on each selected issue
4. Add `status:in-progress` as you start each issue during the sprint

## Sprint Review checklist

1. Close all completed issues (reference commit or PR in the closing comment)
2. Move incomplete issues back to the backlog (remove milestone)
3. Publish a GitHub Release if there is a usable increment
4. Close the milestone

## Sprint Retrospective

Create an issue with label `type:docs` titled `Retrospective: Sprint N` and answer:

- What went well?
- What could be improved?
- Action items for the next sprint (as checkboxes)

---

## Backlog rules

- Every issue must have: `type:*`, `priority:*`, `size:*`
- Issues labeled `size:xl` must be split before they can be assigned to a sprint
- Acceptance criteria (checklist) are required before an issue is sprint-ready
