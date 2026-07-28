# Architecture decision records

Architecture decision records (ADRs) capture durable choices whose rationale
would otherwise be lost in code review, benchmark output, or dated planning
documents. Arise uses
[Michael Nygard's decision-record template](https://github.com/architecture-decision-record/architecture-decision-record/blob/main/locales/en/templates/decision-record-template-by-michael-nygard/index.md):
title, status, context, decision, and consequences. Tests and current code
remain authoritative for what is implemented.

## Decisions

| ADR | Status | Decision |
|---|---|---|
| [0001](0001-prioritize-measured-speed.md) | Accepted | Prioritize measured speed over memory reductions in performance-critical paths |
| [0002](0002-use-go-git-as-primary-transport.md) | Accepted | Use go-git as the primary Git transport |

## Process

1. Copy [`template.md`](template.md) to the next zero-padded number.
2. Record one durable decision rather than a general design essay.
3. Begin with `Proposed`; change to `Accepted` when the project adopts it.
4. Never rewrite a materially superseded decision. Add a new ADR and mark the
   old one `Superseded by ADR-NNNN`.
5. Link reproducible tests, benchmarks, or evidence in the context when they
   materially influenced the choice.

ADRs are not immutable historical artifacts: spelling, broken links, and
clarifications that do not change the decision may be corrected in place.
