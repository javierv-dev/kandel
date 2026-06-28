---
name: kandel-memory
description: "ALWAYS ACTIVE — Persistent memory protocol. You MUST save decisions, conventions, bugs, and discoveries to kandel proactively. Do NOT wait for the user to ask."
---

# Kandel Persistent Memory — Protocol

You have access to Kandel, a persistent memory system that survives across sessions and compactions.
This protocol is MANDATORY and ALWAYS ACTIVE — not something you activate on demand.

## AVAILABLE TOOLS

The kandel tools come from an MCP server and their schemas are **deferred**: you MUST load them
with `ToolSearch` before calling any `mem_*` tool (the SessionStart/UserPromptSubmit hooks remind
you). Load the core set with a SINGLE ToolSearch the first time memory is relevant:

```
ToolSearch select:mcp__kandel__mem_save,mcp__kandel__mem_search,mcp__kandel__mem_context,mcp__kandel__mem_session_summary,mcp__kandel__mem_get,mcp__kandel__mem_save_prompt
```

Core tools:
- `mem_save`, `mem_search`, `mem_context`, `mem_session_summary`
- `mem_get`, `mem_suggest_topic_key`, `mem_update`
- `mem_session_start`, `mem_session_end`, `mem_save_prompt`

**Fallback**: If tools are unexpectedly unavailable, run `kandel install claude-code`
again and restart Claude Code. It repairs the MCP config, hooks and skill.

Additional tools (deferred — load with ToolSearch only when needed):
- `mem_pin`, `mem_unpin`, `mem_review`, `mem_capture_passive`, `mem_current_project`
- `mem_doctor`, `mem_compare`, `mem_judge`
- `mem_delete`, `mem_restore`

> ⚠️ `mem_delete` removes a memory (LOGICAL delete — reversible with `mem_restore`). Use it
> ONLY when sure: a wrong/duplicate memory, or one the user asked to forget. Physical
> (irreversible) deletion is NOT available to the agent — only via `kandel delete <id> --hard`.

## PROACTIVE SAVE TRIGGERS (mandatory — do NOT wait for user to ask)

Call `mem_save` IMMEDIATELY and WITHOUT BEING ASKED after any of these:

### After decisions or conventions
- Architecture or design decision made
- Team convention documented or established
- Workflow change agreed upon
- Tool or library choice made with tradeoffs

### After completing work
- Bug fix completed (include root cause)
- Feature implemented with non-obvious approach
- Notion/Jira/GitHub artifact created or updated with significant content
- Configuration change or environment setup done

### After discoveries
- Non-obvious discovery about the codebase
- Gotcha, edge case, or unexpected behavior found
- Pattern established (naming, structure, convention)
- User preference or constraint learned

### After user confirmation or rejection
- User confirms a recommendation you made ("go with that", "let's do that", "sounds good", "agreed", "perfect", or the equivalent in the user's language)
- User rejects an option or approach ("no, better X", "not that one", or the equivalent in the user's language)
- User expresses a preference ("I prefer X over Y", "always do it this way", or the equivalent in the user's language)
- User makes a decision after you presented tradeoffs or options
- A discussion concludes with a clear direction chosen — even if the agent proposed it

### Self-check — ask yourself after EVERY task:
> "Did I or the user just make a decision, confirm a recommendation, express a preference, fix a bug, learn something non-obvious, or establish a convention? If yes, call mem_save NOW."

Format for `mem_save`:
- **title**: Verb + what — short, searchable (e.g. "Fixed N+1 query in UserList", "Chose Zustand over Redux")
- **type**: bugfix | decision | architecture | discovery | pattern | config | learning
- **scope**: `project` (default) | `personal`
- **topic_key** (optional but recommended for evolving topics): stable key like `architecture/auth-model`
- **content**:
  **What**: One sentence — what was done
  **Why**: What motivated it (user request, bug, performance, etc.)
  **Where**: Files or paths affected
  **Learned**: Gotchas, edge cases, things that surprised you (omit if none)

### Topic update rules (mandatory)

- Different topics MUST NOT overwrite each other (example: architecture decision vs bugfix)
- If the same topic evolves, call `mem_save` with the same `topic_key` so memory is updated (upsert) instead of creating a new observation
- If unsure about the key, call `mem_suggest_topic_key` first, then reuse that key consistently
- If you already know the exact ID to fix, use `mem_update`

## WHEN TO SEARCH MEMORY

Recent sessions and memories are ALREADY injected at session start by the hook, so you do NOT need
to call `mem_context` just to see them. Reach for the tools only when the injected context is not
enough. When the user asks to recall something — any variation of "remember", "recall", "what did
we do", "how did we solve", or the equivalent — or references past work not already in context:
1. Call `mem_search` with relevant keywords (semantic vector search by meaning)
2. If you find a match, use `mem_get` for full untruncated content
3. Use `mem_context` only to re-list recent items if the injected context was truncated or lost

Do NOT load or call these tools for trivial requests that need no prior memory.

## SESSION CLOSE PROTOCOL (mandatory)

Before ending a session or saying "done" / "that's it", you MUST:
1. Call `mem_session_summary` with this structure:

## Goal
[What we were working on this session]

## Instructions
[User preferences or constraints discovered — skip if none]

## Discoveries
- [Technical findings, gotchas, non-obvious learnings]

## Accomplished
- [Completed items with key details]

## Next Steps
- [What remains to be done — for the next session]

## Relevant Files
- path/to/file — [what it does or what changed]

This is NOT optional. If you skip this, the next session starts blind.

## AFTER COMPACTION

If you see a message about compaction or context reset:
1. IMMEDIATELY call `mem_session_summary` with the compacted summary content — this persists what was done before compaction
2. Then call `mem_context` to recover any additional context from previous sessions
3. Only THEN continue working

Do not skip step 1. Without it, everything done before compaction is lost from memory.
