# Agent Instructions

This project uses **br** (beads) for issue tracking.

## Quick Reference

```bash
br ready              # Find available work
br show <id>          # View issue details
br update <id> --status in_progress  # Claim work
br close <id>         # Complete work
br sync --flush-only  # Export issue changes before jj commit
```

## Robot-Next Compatibility

`bv --robot-next` is useful for recommendation context, but its JSON command fields may still name the legacy `bd` executable. Treat those command fields as hints only: use the returned issue `id` with the equivalent `br show`, `br update`, and `br close` commands from the quick reference.

When the user asks for work with a specific label, select from `br ready --label <label>` before acting on the generic `bv --robot-next` recommendation. If the prompt names required context files such as `README.md`, `AGENTS.md`, or `MIND_MAP.md`, read those files first, then resolve the label-scoped queue. For a lightweight smoke check or generic recommendation context, run `bv --robot-next`, then verify the returned `id` with `br show <id>`; never run a legacy tracker executable from the robot output.

## br Stats Caveat

`br stats` and `br status` are useful dashboard checks, but the installed tracker may still print a stale footer that points to `bd list`, and its Recent Activity issue counters may report zero even when `.beads/issues.jsonl` has recent issue changes. Treat those as external CLI hints only; use `br list`, `br ready`, `br show`, and `br count` for issue details in this repository.

## Session Completion

**MANDATORY WORKFLOW**

1. **Only work on one issue at a time**
2. **Create tests** - If appropriate. Prefer cucumber tests for scenario type work.
3. **File issues for remaining work** - Create issues for anything that needs follow-up
4. **Run quality gates** (if code changed) - Tests, linters, builds
5. **Commit to version control, and only commit one issue at a time**
6. **Update issue status** - Close finished work, update in-progress items
7. **Hand off** - Provide context for next session

- Version control uses Jujutsu (jj); never use git commands

### Best Practices

- Read any project context files explicitly named by the user before selecting work
- For label-scoped requests, use `br ready --label <label>` before generic `bv --robot-next` recommendations
- For unscoped requests, check `bv --robot-next` at session start for recommendation context
- Update status as you work, from in_progress to closed
- Create new issues with `br create` when you discover tasks
- Use descriptive titles and set appropriate priority/type, and dependencies between related items
- Always `br sync --flush-only` before committing
- Commit between finishing one beads issue and starting another
- Use `jj desc -m <description>` with a description, when finished use `jj new`
- Add comments to newly created methods so their purpose is easy to understand later
- Track agent mistakes in `MIND_MAP.md` during each session (what went wrong, why, and prevention step)
- Track user preferences in `MIND_MAP.md` during each session (things the user explicitly likes/dislikes)
- Keep these mind-map notes concise and actionable so future sessions can apply them immediately
- Never touch files outside the project directory
