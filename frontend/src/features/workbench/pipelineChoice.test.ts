import { describe, expect, it } from 'vitest'
import { pipelineChoicePrompt, splitPipelineChoice } from './pipelineChoice'

const block = '```harflex-choice\n{"kind":"pipeline"}\n```'

describe('the choice block of a large request', () => {
  it('takes the block out of the message and says the agent asked', () => {
    expect(splitPipelineChoice(`Entendi: integrar o arquivo.\n\n${block}`)).toEqual({ text: 'Entendi: integrar o arquivo.', ask: true })
    expect(splitPipelineChoice(`Antes.\n${block}\nDepois.`)).toEqual({ text: 'Antes.\n\nDepois.', ask: true })
  })
  it('never shows a block that is still being written, and does not ask yet', () => {
    expect(splitPipelineChoice('Entendi.\n\n```harflex-choice\n{"kind":"pipe')).toEqual({ text: 'Entendi.', ask: false })
    expect(splitPipelineChoice('Entendi.\n\n```harflex-')).toEqual({ text: 'Entendi.\n\n```harflex-', ask: false })
  })
  it('asks only for the pipeline choice it knows', () => {
    expect(splitPipelineChoice('Ok.\n```harflex-choice\n{"kind":"other"}\n```').ask).toBe(false)
    expect(splitPipelineChoice('Ok.\n```harflex-choice\nnot json\n```').ask).toBe(false)
    expect(splitPipelineChoice('Texto comum com ```js\ncode\n``` dentro.')).toEqual({ text: 'Texto comum com ```js\ncode\n``` dentro.', ask: false })
  })
  it('tells the agent what was chosen', () => {
    expect(pipelineChoicePrompt('casual')).toContain('no modo Casual')
    expect(pipelineChoicePrompt('professional')).toContain('modo Profissional')
    expect(pipelineChoicePrompt('chat')).toContain('sem abrir uma pipeline')
  })
})
