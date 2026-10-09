import { en as shell, es as shellEs } from './shell'
import { en as agents, es as agentsEs } from './agents'
import { en as pipelines, es as pipelinesEs } from './pipelines'
import { en as pipelines2, es as pipelines2Es } from './pipelines2'
import { en as workbench, es as workbenchEs } from './workbench'
import { en as worktrees, es as worktreesEs } from './worktrees'
import { en as features, es as featuresEs } from './features'
import { en as backend, es as backendEs } from './backend'

// One file per area, so each area can be translated on its own. Every file exports `en` and `es`, keyed by the Portuguese text.
export const en: Record<string, string> = { ...shell, ...agents, ...pipelines, ...pipelines2, ...workbench, ...worktrees, ...features, ...backend }
export const es: Record<string, string> = { ...shellEs, ...agentsEs, ...pipelinesEs, ...pipelines2Es, ...workbenchEs, ...worktreesEs, ...featuresEs, ...backendEs }
