# Agent Instructions

This project uses **br** (beads) for issue tracking.

## Quick Reference

```bash
br ready              # Find available work
br show <id>          # View issue details
br update <id> --status in_progress  # Claim work
br close <id>         # Complete work
br sync               # Sync issues with version control
```

## Session Completion

**MANDATORY WORKFLOW**

1. **Only work on one issue at a time**
2. **Create tests** - If appropriate. Prefer cucumber tests for scenario type work.
3. **File issues for remaining work** - Create issues for anything that needs follow-up
4. **Run quality gates** (if code changed) - Tests, linters, builds
5. **Commit to version control, and only commit one issue at a time**
6. **Update issue status** - Close finished work, update in-progress items
7. **Hand off** - Provide context for next session

- Version control using jututsu (jj), never use git commands

### Best Practices

- Check `bv --robot-next` at session start to find available work
- Update status as you work (in_progress → closed)
- Create new issues with `br create` when you discover tasks
- Use descriptive titles and set appropriate priority/type, and dependencies between related items
- Always `br sync` before committing
- Commit between finishing one beads issue and starting another
- Use `jj desc -m <description>` with a description, when finished use `jj new`
- Add comments to newly created methods so their purpose is easy to understand later
- Track agent mistakes in `MIND_MAP.md` during each session (what went wrong, why, and prevention step)
- Track user preferences in `MIND_MAP.md` during each session (things the user explicitly likes/dislikes)
- Keep these mind-map notes concise and actionable so future sessions can apply them immediately
- Never touch files outside the project directory

