# AI Workflow Guide

This document explains how to use OpenCode skills with the project prompt and implementation phases.

## Concept

There are 3 layers:

1. Skill files
   - Define how AI should think.
   - Example: senior-product-engineer, implementation-planner, golang-backend, code-reviewer.

2. Product spec
   - Source of truth for the product.
   - File: docs/product-spec.md

3. Phase plan
   - Implementation roadmap.
   - File: docs/phases.md

Do not paste the full master prompt every time.
Keep it in docs/product-spec.md and ask AI to read it.

## Recommended Skills

Use these skill files:

- senior-product-engineer.md
- implementation-planner.md
- golang-backend.md
- code-reviewer.md
- debugging-engineer.md
- devops-deployment.md

## First-Time Setup Prompt

Use this prompt first:

```text
Use senior-product-engineer and implementation-planner skills.

Create project documentation files first.

Create:
- docs/product-spec.md
- docs/phases.md
- docs/decisions.md
- docs/ai-workflow.md

Put the provided Master Prompt into docs/product-spec.md.
Put the provided Phase 0 to Phase N into docs/phases.md.
Put initial key engineering decisions into docs/decisions.md.
Put the AI workflow guide into docs/ai-workflow.md.

Do not implement application code yet.

After creating docs, summarize the documentation structure and wait for my approval.
```

## Start Phase 0

```text
Use senior-product-engineer and implementation-planner skills.

Read:
- docs/product-spec.md
- docs/phases.md

Start with PHASE 0 only.

Do not write production code yet.

Produce:
1. Architecture summary
2. Component boundaries
3. Folder structure
4. Database schema draft
5. Config YAML draft
6. Domain model draft
7. MarketDataService design
8. PaperBroker design
9. Universe scanner design
10. Dynamic LLM routing design
11. Portfolio risk control design
12. Less-is-more strategy design
13. External signal source design
14. Risk Engine design
15. LLM Veto Agent design
16. Failure mode table
17. Implementation roadmap from Phase 1 to Phase N

After producing the plan, wait for my approval.
```

## Implement Phase 1

```text
Use senior-product-engineer, implementation-planner, and golang-backend skills.

Read:
- docs/product-spec.md
- docs/phases.md
- docs/architecture.md if it exists
- docs/decisions.md if it exists

Continue to PHASE 1 only.

Before coding:
1. Inspect the current codebase.
2. Summarize what already exists.
3. Identify gaps, bugs, or safety issues.
4. List files you will create/modify.
5. List tests you will add.

Then implement PHASE 1 only.

Do not implement future phases.
Do not implement trading logic yet.
Do not implement WebSocket yet.
Do not implement OpenRouter yet.
Do not implement PaperBroker yet.

After implementation:
1. Summarize changed files.
2. Explain important design decisions.
3. List tests added.
4. Tell me how to run the tests.
```

## Generic Continue Phase Prompt

Use this for Phase 2 and beyond.

```text
Use senior-product-engineer, implementation-planner, and golang-backend skills.

Read:
- docs/product-spec.md
- docs/phases.md
- docs/architecture.md if it exists
- docs/decisions.md if it exists

Continue to PHASE X only.

Before coding:
1. Inspect the current codebase.
2. Summarize what already exists.
3. Identify gaps, bugs, or safety issues.
4. List files you will create/modify.
5. List tests you will add.

Then implement PHASE X only.

Do not implement future phases.
Do not change unrelated code.
Do not skip tests for safety-critical logic.

Maintain these invariants:
- LLM cannot create trades.
- LLM cannot change side.
- LLM cannot set arbitrary size.
- Setup Engine calculates proposed trade.
- Risk Engine is final authority.
- Market data must be healthy.
- PaperBroker is default broker.
- If invalid/stale/unsafe/uncertain => skip and log.
- No position may exist without protective SL.
- Strategy must stay less-is-more.
- Dynamic LLM routing must be score-based.
- Portfolio risk limits must prevent overexposure.

After implementation:
1. Summarize changed files.
2. Explain important design decisions.
3. List tests added.
4. Tell me how to run the tests.
5. Mention remaining work for later phases.
```

## Code Review Prompt

Run this after each phase.

```text
Use code-reviewer skill.

Review the PHASE X implementation adversarially.

Read:
- docs/product-spec.md
- docs/phases.md
- docs/decisions.md if it exists

Focus on:
- correctness
- missing validation
- error handling
- missing tests
- state consistency
- production risks
- safety invariants
- unnecessary complexity

Do not modify code yet.

Output:
1. Summary verdict
2. Critical issues
3. High-priority fixes
4. Medium-priority improvements
5. Tests to add
6. Suggested fixes
7. Good parts worth keeping
```

## Fix Review Issues Prompt

```text
Use golang-backend skill.

Fix only the critical and high-priority issues found in the PHASE X review.

Do not implement the next phase.
Do not change unrelated code.
Add or update tests for the fixes.

After fixing:
1. Summarize changed files.
2. Explain the fix.
3. List tests added or updated.
4. Tell me how to run the tests.
```

## Debugging Prompt

```text
Use debugging-engineer skill.

Here is the error log:

<paste error log>

Debug systematically.

Output:
1. Symptom summary
2. Expected vs actual behavior
3. Most likely causes
4. What to inspect
5. Reproduction steps
6. Fix plan
7. Minimal code fix if possible
8. Verification steps
9. Regression tests to add
```

## Deployment Prompt

```text
Use devops-deployment skill.

Implement PHASE 17 deployment only.

Read:
- docs/product-spec.md
- docs/phases.md

Create:
- Dockerfile
- docker-compose.yml
- .env.example
- configs/paper.yaml
- configs/live.example.yaml
- systemd service
- VPS README
- local README
- backup script
- restore script
- migration command
- graceful shutdown notes

Do not implement live broker.
Do not change trading logic.
```

## Recommended Workflow Per Phase

Use this loop:

1. Plan phase
2. Implement phase
3. Run tests
4. Review phase
5. Fix review issues
6. Commit
7. Continue next phase

Never implement many phases at once.
