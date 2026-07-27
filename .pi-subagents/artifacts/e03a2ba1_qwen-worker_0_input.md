# Task for qwen-worker

Implement the plan in /home/brian/dev/proj/racket/PLAN-slot-management.md (note: dash, not dot, in filename). Read the plan file first, then read every file it references before editing. The plan covers:

1. Append DeleteSlot and UnassignPartsBySlot queries to internal/db/queries.sql, then run `sqlc generate` (never hand-edit generated files).
2. New file internal/handlers/slots.go with CreateSlot and DeleteSlot handlers, following existing conventions in machines.go/parts.go/handlers.go exactly (parseID helper, SlotResponse/newSlotResponse exist in handlers.go). Remove the stale `// ponytail:` comment on defaultSlots in machines.go.
3. Routes in cmd/server/main.go: POST /api/machines/{id}/slots, DELETE /api/slots/{id}.
4. Frontend: new frontend/src/api/slots.ts (same fetch style as parts.ts), and update frontend/src/views/MachineDetailView.vue — small × remove button per slot card header (no confirm), plus an "Add slot" card at the end of the grid. Reuse existing .card/.btn-small styles.
5. Tests: extend internal/handlers/machines_test.go — add the two routes to newTestRouter, add TestCreateSlot and TestDeleteSlotUnassignsPart exactly as described in the plan.
6. Verify with: sqlc generate && mise run fmt && mise run lint && mise run test && mise run build

Working directory: /home/brian/dev/proj/racket

Constraints:
- Follow the plan exactly; it is approved. Do not add anything beyond it (no confirm dialogs, no rename/reorder).
- Keep the lazy/minimal style of the codebase. Reuse existing helpers and styles.
- If any step of the verification chain fails, fix it and re-run until all pass.
- Report back: files changed, test/lint/build results, and any deviations from the plan.

## Acceptance Contract
Acceptance level: verified
Completion is not accepted from prose alone. End with a structured acceptance report.

Criteria:
- criterion-1: Implement the requested change without widening scope

Required evidence: changed-files, tests-added, commands-run, validation-output, residual-risks, no-staged-files

Finish with a fenced JSON block tagged `acceptance-report` in this shape:
Use empty arrays when no items apply; array fields contain strings unless object entries are shown.
`criteriaSatisfied[].status` must be exactly one of: satisfied, not-satisfied, not-applicable.
`commandsRun[].result` must be exactly one of: passed, failed, not-run.
`manualNotes` and `notes` are optional strings; an empty string means no note and does not satisfy `manual-notes` evidence.
```acceptance-report
{
  "criteriaSatisfied": [
    {
      "id": "criterion-1",
      "status": "satisfied",
      "evidence": "specific proof"
    }
  ],
  "changedFiles": [
    "src/file.ts"
  ],
  "testsAddedOrUpdated": [
    "test/file.test.ts"
  ],
  "commandsRun": [
    {
      "command": "command",
      "result": "passed",
      "summary": "short result"
    }
  ],
  "validationOutput": [
    "validation output or concise summary"
  ],
  "residualRisks": [
    "none"
  ],
  "noStagedFiles": true,
  "diffSummary": "short description of the diff",
  "reviewFindings": [
    "blocker: file.ts:12 - issue found, or no blockers"
  ],
  "manualNotes": "anything else the parent should know"
}
```