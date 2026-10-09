import type { WorkspaceProfile } from '../../lib/backend'
import { t } from '../../i18n'

// The labels and hints are Portuguese keys: t() is applied where they are shown.
export const profileChoices: ReadonlyArray<{ value: WorkspaceProfile; label: string; hint: string }> = [
  { value: 'ask', label: 'Perguntar', hint: 'Solicitar aprovação para escrita, shell e rede.' },
  { value: 'trusted_workspace', label: 'Workspace confiável', hint: 'Permitir escrita nesta pasta; shell e rede ainda pedem aprovação.' },
  { value: 'full_access', label: 'Acesso total', hint: 'Escrever, executar comandos e usar ferramentas de rede sem pedir aprovação.' },
]

export function profileLabel(profile: string): string {
  const label = profileChoices.find(item => item.value === profile)?.label
  if (label !== undefined) return t(label)
  return profile === 'sandbox' ? t('Sandbox (indisponível)') : profile
}
