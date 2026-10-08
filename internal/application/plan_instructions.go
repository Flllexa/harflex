package application

// claudePlanInstructions asks Claude Code, which has no plan tool in print mode, for the checklist the activity
// panel draws as the plan of execution.
const claudePlanInstructions = "When the task takes several steps, show your plan as a Markdown checklist (`- [ ] step` / `- [x] step`, short actions in the person's language) before you start, " +
	"and write the whole checklist again, updated, whenever a step starts or finishes; mark the step you are working on with \"(em andamento)\". Skip the checklist for quick answers."
