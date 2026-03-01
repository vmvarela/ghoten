#!/usr/bin/env python3

import subprocess

LABELS = [
    ("type:feature", "1D76DB", "New functionality"),
    ("type:bug", "D73A4A", "Something is not working"),
    ("type:chore", "0E8A16", "Maintenance, refactoring, tooling"),
    ("type:spike", "D4C5F9", "Research or investigation (timeboxed)"),
    ("type:docs", "0075CA", "Documentation only"),
    ("type:breaking", "B60205", "Breaking change requires major version bump"),
    ("priority:critical", "B60205", "Must fix immediately blocks everything"),
    ("priority:high", "D93F0B", "Must be in the next sprint"),
    ("priority:medium", "FBCA04", "Should be done soon"),
    ("priority:low", "C2E0C6", "Nice to have do when possible"),
    ("size:xs", "EDEDED", "Trivial less than 1 hour"),
    ("size:s", "D4C5F9", "Small 1 to 4 hours"),
    ("size:m", "BFD4F2", "Medium 4 to 8 hours"),
    ("size:l", "FBCA04", "Large 1 to 2 days"),
    ("size:xl", "D93F0B", "Extra large more than 2 days split it"),
    ("status:ready", "0E8A16", "Refined and ready for sprint selection"),
    ("status:in-progress", "1D76DB", "Currently being worked on"),
    ("status:blocked", "B60205", "Waiting on something external"),
    ("status:review", "D4C5F9", "In code review or waiting for feedback"),
    ("mvp", "FEF2C0", "Part of the Minimum Viable Product"),
    ("tech-debt", "E4E669", "Technical debt address proactively"),
    ("retrospective", "C5DEF5", "Sprint retrospective issue"),
]

for name, color, description in LABELS:
    create = subprocess.run(
        ["gh", "label", "create", name, "--color", color, "--description", description],
        capture_output=True,
        text=True,
    )
    if create.returncode != 0:
        subprocess.run(
            ["gh", "label", "edit", name, "--color", color, "--description", description],
            check=True,
        )

print("Scrum labels ensured")
