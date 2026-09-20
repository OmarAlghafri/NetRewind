package harness

import "github.com/OmarAlghafri/netrewind/internal/ai"

// HandleMap, BuildHandles: moved to internal/ai so the evaluation runner,
// the CLI, and the desktop shell share one implementation - see
// internal/ai/handles.go for the full doc comment and every other user of
// this type. Kept here as aliases so ai/eval/harness's own exported surface
// (and every existing caller of harness.BuildHandles/harness.HandleMap)
// does not need to change.
type HandleMap = ai.HandleMap

var BuildHandles = ai.BuildHandles
