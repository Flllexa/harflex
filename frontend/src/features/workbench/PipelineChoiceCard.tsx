import { LayoutDashboard, MessageSquare, ListTree } from 'lucide-react'
import { useT } from '../../i18n'
import type { AppMode } from '../../state/appMode'
import type { PipelineChoice } from './pipelineChoice'

/** The agent found the request large: the person picks how to carry it, in the chat or on a pipeline, here or in Professional mode. */
export function PipelineChoiceCard({ viewMode, disabled, onChoose }: { viewMode: AppMode; disabled: boolean; onChoose: (choice: PipelineChoice) => void }) {
  const t = useT()
  const casual = viewMode === 'casual'
  return <div className="pipeline-choice" role="group" aria-label={t('Como você quer tocar isso?')}>
    <strong>{t('Como você quer tocar isso?')}</strong>
    <p className="muted">{t('Uma pipeline divide o trabalho em Discovery, SPEC, Plan, Code, QA e PRs, e você aprova cada fase.')}</p>
    <div className="pipeline-choice-options">
      <button type="button" className="touch-target primary-button" disabled={disabled} onClick={() => onChoose('casual')}>
        <ListTree aria-hidden="true" /><span>{casual ? t('Abrir a pipeline e fazer tudo por aqui, no Casual') : t('Abrir a pipeline')}</span>
      </button>
      {casual && <button type="button" className="touch-target secondary-button" disabled={disabled} onClick={() => onChoose('professional')}>
        <LayoutDashboard aria-hidden="true" /><span>{t('Abrir a pipeline e ir para o modo Profissional')}</span>
      </button>}
      <button type="button" className="touch-target secondary-button" disabled={disabled} onClick={() => onChoose('chat')}>
        <MessageSquare aria-hidden="true" /><span>{t('Só resolver aqui na conversa, sem pipeline')}</span>
      </button>
    </div>
  </div>
}
