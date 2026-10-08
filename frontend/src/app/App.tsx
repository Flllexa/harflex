import { AppShell, type AppShellProps } from '../components/AppShell'

// THESIS: Keep stage, workspace and evidence visible in an auditable control room.
// OWN-WORLD: Flat Ion Void and Forge surfaces, mint selection, cyan evidence.
// STORY: Identify the project and phase, then inspect conversation or activity.
// FIRST VIEWPORT: 240px navigation, flexible workspace, 320px activity; pipeline above.
// FORM: Approved composition 2, conversation/evidence workbench, limited to its shell.
export default function App(props: AppShellProps) {
  return <AppShell {...props} />
}
