import { t } from '../../i18n'

// The background QA loop reports its state in Portuguese (set by the server). These are the messages it sends,
// translated when they reach the screen; a message that ends with an error detail keeps that detail as it is.
const templates = [
  'O QA não terminou: {detail}',
  'O Coder não terminou: {detail}',
  'O Coder não deixou mudanças verificáveis: {detail}',
  'Não foi possível levar a correção ao QA: {detail}',
  'O QA não entregou um relatório válido: {detail}',
  'Não foi possível voltar ao Code: {detail}',
]

export function qaLoopText(message: string): string {
  for (const template of templates) {
    const prefix = template.slice(0, template.indexOf('{detail}'))
    if (message.startsWith(prefix)) return t(template, { detail: message.slice(prefix.length) })
  }
  return t(message)
}
