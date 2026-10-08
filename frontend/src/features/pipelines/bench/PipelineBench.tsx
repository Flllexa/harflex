import { useEffect, useState, type ReactNode } from 'react'
import { FlaskConical, GitPullRequest, Hammer, Lock } from 'lucide-react'
import type { Pipeline } from '../../../lib/backend'
import './bench.css'

export type BenchStage = 'code' | 'eval' | 'prs'

type Props = {
  run: Pipeline
  /** The bench the top stage bar asked to look at, if any. */
  viewStage?: string
  viewRequestId?: number
  /** A bench to show instead of the current stage's, such as QA while a fix round runs in Code. */
  focus?: BenchStage
  /** Tells the stage bar which bench is on screen, so its border marks the same stage. */
  onShownStage?: (stage: BenchStage) => void
  code: ReactNode
  qa: ReactNode
  prs: ReactNode
}

const benches: { stage: BenchStage; name: string; Icon: typeof Hammer; title: string; tagline: string; place: string }[] = [
  { stage: 'code', name: 'Code', Icon: Hammer, title: 'Bancada do Code', tagline: 'O Coder escreve a mudança. Aqui ela fica em destaque, arquivo por arquivo, antes de ir para a QA.', place: 'Cópia privada do projeto · o original não muda' },
  { stage: 'eval', name: 'QA', Icon: FlaskConical, title: 'Laboratório de QA', tagline: 'O QA roda de verdade o build, os testes, o E2E e o app no ar, e mostra cada etapa como numa esteira de CI.', place: 'Laboratório próprio · cópia do Code, com terminal e rede' },
  { stage: 'prs', name: 'PRs', Icon: GitPullRequest, title: 'Mesa de revisão', tagline: 'O PR sai da bancada para o repositório, e a vigia cuida dos comentários da revisão.', place: 'Pasta do projeto · branch própria do trabalho' },
]


/** Code, QA and PRs each get a screen of their own, opened from the stage bar at the top (the current stage by default). */
export function PipelineBench({ run, viewStage, viewRequestId, focus, onShownStage, code, qa, prs }: Props) {
  const current: BenchStage = focus ?? (run.currentStage === 'eval' ? 'eval' : run.currentStage === 'code' ? 'code' : 'prs')
  const [stage, setStage] = useState<BenchStage>(current)
  useEffect(() => { setStage(current) }, [run.id, current])
  useEffect(() => { if (viewStage === 'code' || viewStage === 'eval' || viewStage === 'prs') setStage(viewStage) }, [viewStage, viewRequestId])
  useEffect(() => { onShownStage?.(stage) }, [stage, onShownStage])
  const bench = benches.find(item => item.stage === stage)!
  const Icon = bench.Icon

  return <section className={`bench bench-${stage}`} aria-label={`Bancada ${bench.name}`}>
    <header className="bench-header">
      <span className="bench-plate" aria-hidden="true"><Icon /></span>
      <div><h3>{bench.title}</h3><p>{bench.tagline}</p></div>
      <span className="bench-place"><Lock aria-hidden="true" />{bench.place}</span>
    </header>
    <div className="bench-surface">{stage === 'code' ? code : stage === 'eval' ? qa : prs}</div>
  </section>
}
