import { t } from '../i18n'
import type { BackendOption } from './backend'

/** The name a provider picker shows: an unavailable CLI says why, so the person knows what to fix. */
export function backendLabel(item: BackendOption): string {
  if (item.available) return item.name
  switch (item.unavailableReason) {
    case 'not_found':
      return t('{name} · não encontrado', { name: item.name })
    case 'timeout':
      return t('{name} · demorou para responder', { name: item.name })
    case 'failed':
      return item.unavailableDetail ? t('{name} · não iniciou: {detail}', { name: item.name, detail: item.unavailableDetail }) : t('{name} · não iniciou', { name: item.name })
    default:
      return t('{name} · indisponível', { name: item.name })
  }
}
