import { useState } from 'react'
import { Button, Dialog, DialogTrigger, Popover, Radio, RadioGroup } from 'react-aria-components'
import { Check, ChevronDown, FolderOpen } from 'lucide-react'
import type { Backend, Workspace, WorkspaceSummary } from '../../lib/backend'
import { useT } from '../../i18n'
import './casualStart.css'

type Props = { backend: Backend; workspace: Workspace; disabled?: boolean; onPick: (path: string) => void }

const basename = (path: string) => path.split(/[\\/]/).filter(Boolean).pop() ?? path

/** The chat's project, beside the permission control: one click to start in another project. */
export function ChatProjectPicker({ backend, workspace, disabled = false, onPick }: Props) {
  const t = useT()
  const [open, setOpen] = useState(false)
  const [projects, setProjects] = useState<WorkspaceSummary[]>()

  function toggle(next: boolean) {
    setOpen(next)
    if (next) void backend.listWorkspaces().then(items => setProjects(items.filter(item => !item.archived && item.available)), () => setProjects([]))
  }

  const name = basename(workspace.path)
  return <DialogTrigger isOpen={open} onOpenChange={toggle}>
    <Button className="chat-model-trigger chat-project-trigger" isDisabled={disabled} aria-label={t('Projeto do chat: {name}', { name })}>
      <FolderOpen aria-hidden="true" className="chat-model-trigger-icon" />
      <span className="chat-model-trigger-text"><span>{name}</span></span>
      <ChevronDown aria-hidden="true" className="chat-model-trigger-chevron" />
    </Button>
    <Popover className="chat-model-popover" placement="top start" offset={8}>
      <Dialog className="chat-model-dialog" aria-label={t('Projeto do chat')}>
        <RadioGroup className="chat-model-section" aria-label={t('Projeto do chat')} value={workspace.id}
          onChange={id => { const picked = projects?.find(item => item.id === id); setOpen(false); if (picked && picked.id !== workspace.id) onPick(picked.path) }}>
          <span className="chat-model-section-title"><FolderOpen aria-hidden="true" />{t('Projeto')}</span>
          {!projects ? <p className="chat-model-fixed-note muted" role="status">{t('Lendo os projetos…')}</p>
            : <div className="chat-model-options">
              {projects.map(item => <Radio key={item.id} value={item.id} className="chat-model-option">
                {({ isSelected }) => <>
                  <span className="chat-model-option-name" title={item.path}>{basename(item.path)}</span>
                  <Check aria-hidden="true" className={`chat-model-check${isSelected ? ' is-on' : ''}`} />
                </>}
              </Radio>)}
            </div>}
        </RadioGroup>
      </Dialog>
    </Popover>
  </DialogTrigger>
}
