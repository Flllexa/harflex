import { useState } from 'react'
import { ShieldAlert } from 'lucide-react'
import { IonPicker, type IonOption } from '../../components/IonPicker'
import { errorMessage, type Backend, type Workspace, type WorkspaceProfile } from '../../lib/backend'
import { useT } from '../../i18n'
import { FullAccessDialog } from './FullAccessDialog'
import { profileChoices, profileLabel } from './profiles'
import './permissions.css'

type Props = { backend: Backend; workspace: Workspace; onWorkspaceUpdated: (workspace: Workspace) => void }

/** The project's permission profile, one click away from the conversation. */
export function PermissionPicker({ backend, workspace, onWorkspaceUpdated }: Props) {
  const t = useT()
  const [pending, setPending] = useState(false)
  const [confirming, setConfirming] = useState(false)
  const [error, setError] = useState('')
  const known = profileChoices.some(item => item.value === workspace.profile)
  const options: IonOption[] = profileChoices.map(item => ({ value: item.value, label: t(item.label) }))
  if (!known) options.push({ value: workspace.profile, label: profileLabel(workspace.profile), disabled: true })

  async function apply(profile: WorkspaceProfile, confirmFullAccess = false) {
    if (pending) return
    setPending(true)
    setError('')
    try {
      onWorkspaceUpdated(await backend.setWorkspaceProfile(workspace.id, profile, confirmFullAccess ? { confirmFullAccess } : undefined))
      setConfirming(false)
    } catch (failure) { setError(errorMessage(failure)) }
    finally { setPending(false) }
  }
  function choose(value: string) {
    if (value === workspace.profile) return
    setError('')
    if (value === 'full_access') setConfirming(true)
    else void apply(value as WorkspaceProfile)
  }

  return <div className="permission-picker">
    <IonPicker id="permission-profile" label={t('Permissões do projeto')} compact value={workspace.profile} onChange={choose} options={options} disabled={pending} />
    {workspace.profile === 'full_access' && <span className="permission-chip" title={t('O agente não pede aprovação neste projeto.')}><ShieldAlert aria-hidden="true" />{t('Sem pedir aprovação')}</span>}
    {error && !confirming && <p className="form-error" role="alert">{error}</p>}
    {confirming && <FullAccessDialog pending={pending} error={error} onConfirm={() => void apply('full_access', true)} onClose={() => { setConfirming(false); setError('') }} />}
  </div>
}
