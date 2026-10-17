import type { ReactNode } from 'react'
import { LayoutTemplate, RefreshCw, Workflow } from 'lucide-react'
import { useT } from '../../../i18n'
import type { PipelineDesignDocument } from '../../../lib/backend'
import { parseBrainstormContent } from '../../../lib/pipelineDesign'
import { DesignMarkdown } from '../PipelineDesignDocument'
import { DesignView } from './DesignView'
import { FlowView } from './FlowView'
import './brainstorm.css'

type Props = {
  /** The Brainstorm document as the design workspace holds it. */
  document: PipelineDesignDocument
  /** The stage was skipped: the work was prepared before it existed. */
  skipped?: boolean
  /** The AI is drawing the Brainstorm right now, and how far it is. */
  generating: boolean
  progress?: ReactNode
  /** The preparation conversation and its composer, shown under the flow. */
  conversation: ReactNode
  canRefresh: boolean
  onRefresh(): void
  onAskFix(error: string): void
}

/**
 * The Brainstorm stage: the design of the screens in the highlight on the right, the application flow and the
 * conversation to adjust them on the left. Without screens the design box goes away and the flow takes the room.
 */
export function BrainstormStage({ document, skipped, generating, progress, conversation, canRefresh, onRefresh, onAskFix }: Props) {
  const t = useT()
  if (skipped && !document.content) return <section className="brainstorm-stage brainstorm-skipped" aria-label={t('Brainstorm')}>
    <p>{t('O Brainstorm foi pulado: {reason}.', { reason: t('a etapa não existia quando este trabalho foi preparado') })}</p>
  </section>
  const content = parseBrainstormContent(document.content)
  const screens = content?.design?.screens ?? []
  const hasDesign = !content || screens.length > 0
  const running = generating ? <div className="brainstorm-running">{progress}</div> : null
  return <section className={`brainstorm-stage${hasDesign ? '' : ' brainstorm-no-design'}`} aria-label={t('Brainstorm')}>
    {document.stale && <div className="brainstorm-stale" role="status"><p>{t('Este Brainstorm precisa refletir a última mudança da SPEC.')}</p><button type="button" className="touch-target secondary-button" onClick={onRefresh} disabled={!canRefresh}><RefreshCw aria-hidden="true" />{t('Atualizar o Brainstorm')}</button></div>}
    <div className="brainstorm-left">
      <section className="brainstorm-box" aria-label={t('Fluxo')}>
        <header><Workflow aria-hidden="true" /><strong>{t('Fluxo')}</strong></header>
        {running}
        {content ? <FlowView source={content.flow} onAskFix={onAskFix} /> : !generating && <p className="muted">{t('O fluxo aparece aqui quando a IA terminar de desenhá-lo.')}</p>}
      </section>
      {!hasDesign && content?.noDesignReason && <p className="brainstorm-no-design-reason" role="note">{content.noDesignReason}</p>}
      {content?.notes.trim() && <details className="brainstorm-notes"><summary>{t('Decisões e perguntas')}</summary><DesignMarkdown content={content.notes} /></details>}
      <div className="brainstorm-conversation">{conversation}</div>
    </div>
    {hasDesign && <div className="brainstorm-right">
      <section className="brainstorm-box brainstorm-design" aria-label={t('Design')}>
        <header><LayoutTemplate aria-hidden="true" /><strong>{t('Design')}</strong></header>
        {running}
        {screens.length > 0 ? <DesignView screens={screens} /> : !generating && <p className="muted">{t('As telas aparecem aqui quando a IA terminar de desenhá-las.')}</p>}
      </section>
    </div>}
  </section>
}
