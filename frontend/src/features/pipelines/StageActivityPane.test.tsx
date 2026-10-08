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
