# Task for qwen-worker

Small frontend change in /home/brian/dev/proj/racket/frontend/src/views/MachineDetailView.vue.

Context: the "Add slot" card at the end of the slots grid currently has only a free-text input + Add button (see `addSlot()`, `newSlotCategory`, `savingNewSlot` in the script, and the `.add-slot-card` markup near the end of the slots grid). Creating a machine already seeds 9 default slots server-side from `defaultSlots` in internal/handlers/machines.go ("Motherboard", "CPU", "RAM", "RAM", "Storage", "Storage", "GPU", "PSU", "Case").

Task: add one-click chips for the common slot categories to the add-slot card, so users don't have to type them:

1. In the script: `// common presets, mirrors defaultSlots in internal/handlers/machines.go` then `const commonSlots = ["Motherboard", "CPU", "RAM", "Storage", "GPU", "PSU", "Case"];` (deduped — chips can be clicked repeatedly to add e.g. a second RAM slot).
2. Refactor `addSlot` so both the form submit and a chip click share one code path (e.g. `addSlot(category?: string)` — fall back to `newSlotCategory` when not given; only clear the input when the typed path was used). Same behavior as now: trim, guard empty/disabled, `createSlot(detail.value.machine.id, cat)`, refetch via `fetchMachine()`, error into `error.value`, `savingNewSlot` flag.
3. In the template, render the chips inside the add-slot card (outside the `<form>` so no submit interference, or use `type="button"`): a `v-for` of small buttons showing the category name, disabled while `savingNewSlot`. Reuse existing `.btn`/`.btn-small` classes; add minimal scoped CSS only if needed for chip spacing/wrapping (a flex row with gap).
4. Keep the existing text input + Add button for custom categories, unchanged behavior.

Verify: `mise run build` passes (frontend type-check included). No backend changes, no new tests. Report exactly what changed.

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