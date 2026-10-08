import { useEffect, useRef, useState } from 'react'
import type { Backend, BackendOption, ModelCatalogResult, ProviderProfile, Session } from '../../lib/backend'

/** Who answers a chat: the provider, and for a CLI the model and effort. */
export type ChatChoice = { backendId: string; modelId: string; effort: string }

const sameChoice = (a?: ChatChoice, b?: ChatChoice) => !!a && !!b && a.backendId === b.backendId && a.modelId === b.modelId && a.effort === b.effort

/**
 * The provider, model and effort shown during a conversation. It starts as what the conversation runs on; a different
 * choice is what the next message continues with, in a new chat that carries this one's context.
 */
export function useChatModelChoice(backend: Backend, workspaceId: string | undefined, session: Session | undefined, backends: BackendOption[], enabled: boolean) {
  const [current, setCurrent] = useState<ChatChoice>()
  const [choice, setChoice] = useState<ChatChoice>()
  const [catalog, setCatalog] = useState<ModelCatalogResult>()
  const [loading, setLoading] = useState(false)
  const [profiles, setProfiles] = useState<ProviderProfile[]>([])
  const request = useRef(0)

  useEffect(() => {
    if (!enabled || !session) { setCurrent(undefined); setChoice(undefined); return }
    let live = true
    const fallback = { backendId: session.backendId, modelId: '', effort: '' }
    void backend.getSessionModelSelection(session.id).then(
      selection => { if (live) { const value = { backendId: session.backendId, modelId: selection.modelId, effort: selection.reasoningEffort }; setCurrent(value); setChoice(value) } },
      () => { if (live) { setCurrent(fallback); setChoice(fallback) } })
    return () => { live = false }
  }, [backend, session?.id, session?.backendId, enabled])

  useEffect(() => {
    if (!enabled) return
    let live = true
    void backend.listProviderProfiles().then(value => { if (live) setProfiles(value) }, () => undefined)
    return () => { live = false }
  }, [backend, enabled])

  // A CLI's models are read when it is chosen, so the bar can offer them.
  const option = backends.find(item => item.id === choice?.backendId)
  useEffect(() => {
    if (!enabled || !workspaceId || option?.kind !== 'cli') { setCatalog(undefined); return }
    const mine = ++request.current
    setLoading(true)
    void backend.queryCLIModelCatalog({ workspaceId, backendId: option.id }).then(
      value => { if (mine === request.current) setCatalog(value) },
      () => { if (mine === request.current) setCatalog(undefined) })
      .finally(() => { if (mine === request.current) setLoading(false) })
  }, [backend, workspaceId, option?.id, option?.kind, enabled])

  const listed = catalog?.complete && catalog.status === 'complete' && catalog.backendId === choice?.backendId
    ? catalog.models.filter(item => item.backendId === choice?.backendId && item.source === catalog.source) : []
  const model = listed.find(item => item.id === choice?.modelId)
  const changed = !!choice && !!current && !sameChoice(choice, current)
  // A changed choice can carry the conversation only when it names a model the catalog confirms (or an API profile).
  const usable = !!choice && !!option?.available && (option.kind === 'api' || (option.id !== 'opencode' && !!model && model.availability !== 'unavailable'))
  return {
    choice, current, changed, usable, catalog, listed, model, loading, option,
    fixedModel: profiles.find(profile => profile.id === choice?.backendId)?.model ?? '',
    setBackend: (backendId: string) => setChoice({ backendId, modelId: backendId === current?.backendId ? current.modelId : '', effort: backendId === current?.backendId ? current.effort : '' }),
    setModel: (modelId: string) => setChoice(previous => previous && { ...previous, modelId, effort: '' }),
    setEffort: (effort: string) => setChoice(previous => previous && { ...previous, effort }),
    reset: () => setChoice(current),
  }
}
