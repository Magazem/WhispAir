# Orchestrator protocol

Paste this into `CLAUDE.md`, or point at it, so Opus drives the fleet consistently.

You are the **brain**. LongCat-2.0 is the **hands**. Your scarce resource is context; its
scarce resource is judgement. Spend accordingly.

## Rules

1. **Do not read the codebase to understand it.** Run `hive index` once, then ask:
   `hive ask "<question>"`. A dense answer with `path:line` citations comes back. Read source
   directly only when you are about to reason about a specific function you already located.
2. **Decompose into disjoint file scopes.** Two concurrent tasks that write the same file will
   conflict. `hive plan validate` refuses that by default — treat a refusal as a planning bug,
   not an obstacle to override. Fix it by narrowing scopes or adding `depends_on`.
3. **Never block a turn on a long job.** `hive run` returns immediately. Poll with
   `hive wait --json`, which returns at its tick deadline whether or not work finished.
4. **Read summaries, not diffs.** `hive report` gives you each agent's summary, public API
   changes, risks, and follow-ups. Pull an actual diff only when a summary reports something
   surprising.
5. **Task descriptions are prompts.** The executor sees the whole repo but none of your
   reasoning. State the goal, the constraint, and what "done" means. Vague tasks are the single
   biggest cause of bad output — a 1M window does not fix an underspecified ask.

## Plan format

```json
{
  "base": "main",
  "tasks": [
    {
      "id": "rate-limit-mw",
      "title": "Add a token-bucket rate limiter middleware",
      "description": "Add middleware at src/http/middleware/rateLimit.ts using a token bucket keyed by API key. Read limits from config (requests_per_minute, burst). Return 429 with a Retry-After header. Follow the error shape used by the existing auth middleware.",
      "files": ["src/http/middleware/**"],
      "acceptance": "Middleware exists and is exported; 429 carries Retry-After; limits come from config, not constants.",
      "depends_on": [],
      "allow_run": false,
      "max_steps": 40
    }
  ]
}
```

| field | meaning |
|---|---|
| `id` | branch name (`hive/<id>`), must be filesystem/branch safe |
| `files` | **enforced write scope.** Writes outside are rejected. Omit only if you truly can't scope it — an undeclared scope disables conflict prediction |
| `depends_on` | serialises tasks into waves; also suppresses false overlap warnings |
| `allow_run` | lets the agent run allowlisted commands (tests/build). Off by default |
| `acceptance` | what the agent should check before declaring done |

## Turn-by-turn

```bash
hive index                                   # once per session, or after big changes
hive ask "where is X handled and what depends on it?"
# ... write plan.json ...
hive plan validate plan.json
hive run plan.json --concurrency 3
hive wait --json                             # repeat until "complete": true
hive report
hive merge
```

`hive wait` returns a compact object. Act on two fields:

- `complete` — `false` means call `hive wait` again; `true` means go to `hive report`.
- `cache.note` — plain-English guidance on whether continuing to poll is still economical.
  When it tells you to stop polling, stop: let the cache expire, do other work, and check back
  with `hive status` later.

## What to do when things go wrong

| symptom | action |
|---|---|
| `plan validate` reports overlaps | narrow `files`, or add `depends_on`. Only use `--allow-overlap` when you accept a merge conflict |
| a task is `failed` | `hive report` shows the error. Dependents are skipped automatically |
| `confidence: low` in a summary | read that branch's actual diff before merging it |
| `hive merge` leaves a CONFLICT | the resolver refused or produced markers. Resolve by hand on `hive/integration`, or re-run with `--no-resolve` and do it yourself |
| agent hit the step limit | the task was too big. Split it, or raise `max_steps` |

## Cost

`hive ledger` totals LongCat spend. At $0.75/$2.95 per 1M tokens, a 200K-token repo packed into
one executor call costs about $0.15 in input. Packing the same repo into your own context costs
far more and displaces the reasoning you are actually there to do — that asymmetry is the whole
point of this harness.
