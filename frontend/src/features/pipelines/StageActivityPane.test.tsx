import { render,screen } from '@testing-library/react'
import { expect,it,vi } from 'vitest'
import { StageActivityPane } from './StageActivityPane'
import { createFakeBackend } from '../../test/fakeBackend'
import { designPipeline } from '../../test/pipelineDesignFixture'

it('shows real stage events while keeping the internal JSON prompt out of the conversation',async () => {
  const {backend} = createFakeBackend(), pipeline = designPipeline(), date = pipeline.updatedAt
  backend.getPipelineStageActivity = async () => ({pipelineId:pipeline.id,workspaceId:pipeline.workspaceId,stage:'spec',status:'completed',phase:'',sessionId:'spec-session',modelId:'default-model',updatedAt:date})
  backend.listEvents = async () => [
    {id:'u',streamId:'spec-session',sequence:1,type:'message.user',data:{role:'user',content:'{"target":"spec","documents":{"secret":"technical context"}}'},createdAt:date},
    {id:'r',streamId:'spec-session',sequence:2,type:'run.started',data:{},createdAt:date},
    {id:'a',streamId:'spec-session',sequence:3,type:'message.assistant',data:{role:'assistant',content:'{"document":"# SPEC pronta","reply":"Pronto."}'},createdAt:date},
    {id:'c',streamId:'spec-session',sequence:4,type:'run.completed',data:{},createdAt:date},
  ]
  const prepare = vi.spyOn(backend,'preparePipelineDesign')
  render(<StageActivityPane backend={backend} pipeline={pipeline} stage="spec" />)
  expect(await screen.findByText('Execução concluída')).toBeInTheDocument()
  expect(screen.getByText('IA iniciou a execução')).toBeInTheDocument()
  expect(screen.queryByText(/technical context/)).not.toBeInTheDocument()
  expect(prepare).not.toHaveBeenCalled()
})

// Following a long run read its whole journal again every second, over the one database connection the run itself
// writes through, until a write missed its deadline and the run died. Each check now asks only for what is new.
it('follows a running stage by reading only the events after the last one it has', async () => {
  vi.useFakeTimers({ shouldAdvanceTime: true })
  try {
    const {backend} = createFakeBackend(), pipeline = designPipeline(), date = pipeline.updatedAt
    backend.getPipelineStageActivity = async () => ({pipelineId:pipeline.id,workspaceId:pipeline.workspaceId,stage:'spec',status:'running',phase:'spec',sessionId:'coder-session',modelId:'opus',updatedAt:date})
    const journal = [
      {id:'u',streamId:'coder-session',sequence:1,type:'message.user',data:{role:'user',content:'pedido'},createdAt:date},
      {id:'r',streamId:'coder-session',sequence:2,type:'run.started',data:{},createdAt:date},
    ]
    const reads: number[] = []
    backend.listEvents = vi.fn(async (_id: string, after: number) => { reads.push(after); return journal.filter(event => event.sequence > after) })
    render(<StageActivityPane backend={backend} pipeline={pipeline} stage="spec" />)
    expect(await screen.findByText('IA iniciou a execução')).toBeInTheDocument()
    await vi.advanceTimersByTimeAsync(2500)
    expect(reads[0]).toBe(0)
    expect(reads.slice(1).length).toBeGreaterThan(0)
    expect(reads.slice(1).every(after => after === 2)).toBe(true)
  } finally { vi.useRealTimers() }
})
