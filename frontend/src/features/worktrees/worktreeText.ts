import type { Worktree, WorktreeBlocker } from '../../lib/backend'
import { t } from '../../i18n'

/** `one` and `many` are already translated by the caller, so each form stays a plain text the dictionary can hold. */
export const plural = (count: number, one: string, many: string) => `${count} ${count === 1 ? one : many}`

// Git's operation names, read at render time so the language applies when they are shown.
const operations: Record<string, string> = {
  merge: 'um merge', rebase: 'um rebase', 'cherry-pick': 'um cherry-pick', revert: 'um revert', bisect: 'um bisect', sequencer: 'uma sequência de commits',
}

/** One plain sentence per reason a worktree cannot be deleted (or handed to the assistant). */
export function blockerText(blocker: WorktreeBlocker, base: string): string {
  switch (blocker.code) {
    case 'main_worktree': return t('É o worktree principal do repositório.')
    case 'current_project': return t('É o projeto aberto agora. Abra outro projeto para poder excluí-lo.')
    case 'locked': return blocker.detail
      ? t('Está travado ({detail}). Só o destrave manual (git worktree unlock) o libera.', { detail: blocker.detail })
      : t('Está travado. Só o destrave manual (git worktree unlock) o libera.')
    case 'missing_directory': return t('A pasta não existe mais; falta limpar o registro dela.')
    case 'status_unavailable': return t('Não foi possível ler o estado deste worktree.')
    case 'in_use': {
      const listed = blocker.detail ? blocker.detail.split(', ').length : 0
      const rest = blocker.count - listed
      const programs = plural(blocker.count, t('programa está usando'), t('programas estão usando'))
      const close = blocker.count === 1 ? t('Feche-o e confira de novo.') : t('Feche-os e confira de novo.')
      if (!blocker.detail) return t('{programs} esta pasta. {close}', { programs, close })
      const names = rest > 0 ? t('{names} e mais {rest}', { names: blocker.detail, rest }) : blocker.detail
      return t('{programs} esta pasta ({names}). {close}', { programs, names, close })
    }
    case 'operation_in_progress': return t('Há {operation} em andamento. Conclua ou aborte antes.', { operation: t(operations[blocker.detail] ?? 'uma operação do Git') })
    case 'uncommitted_changes': return t('{changes} (arquivos editados ou novos).', { changes: plural(blocker.count, t('alteração não salva'), t('alterações não salvas')) })
    case 'unmerged_commits': {
      const commits = plural(blocker.count, t('commit só desta branch'), t('commits só desta branch'))
      const target = blocker.detail || base
      return blocker.count === 1
        ? t('{commits}, ainda não mesclado em {target}.', { commits, target })
        : t('{commits}, ainda não mesclados em {target}.', { commits, target })
    }
    case 'detached_commits': return t('{commits} (HEAD destacado) que nenhuma branch guarda.', { commits: plural(blocker.count, t('commit solto'), t('commits soltos')) })
    case 'base_unknown': return t('Não foi possível identificar a branch base do repositório.')
    case 'base_dirty': return blocker.count
      ? t('O worktree principal tem alterações ({count}); deixe-o limpo antes de mesclar nele.', { count: blocker.count })
      : t('O worktree principal tem alterações; deixe-o limpo antes de mesclar nele.')
    case 'nothing_to_save': return t('Não há nada pendente para salvar.')
    case 'ignored_files': return t('{items} serão apagados junto.', { items: plural(blocker.count, t('item ignorado pelo Git'), t('itens ignorados pelo Git')) })
    default: return t('Há um impedimento que o Harflex não reconhece; por segurança, nada será excluído.')
  }
}

/** The facts under a worktree's name: what is pending and where its commits stand. */
export function worktreeFacts(item: Worktree, base: string): string[] {
  const facts: string[] = []
  if (item.missing) return [t('Pasta ausente')]
  if (!item.statusKnown) return [t('Estado ilegível')]
  if (item.changed > 0) facts.push(plural(item.changed, t('alteração não salva'), t('alterações não salvas')))
  else facts.push(t('Sem alterações pendentes'))
  if (item.isMain) return facts
  if (item.ahead > 0) facts.push(t('{commits} à frente de {base}', { commits: plural(item.ahead, t('commit'), t('commits')), base }))
  else if (base) facts.push(t('Mesclado em {base}', { base }))
  if (item.behind > 0) facts.push(t('{behind} atrás de {base}', { behind: item.behind, base }))
  if (item.operation) facts.push(t('{operation} em andamento', { operation: t(operations[item.operation] ?? 'Operação') }))
  return facts
}

export function worktreeTitle(item: Worktree): string {
  if (item.branch) return item.branch
  return item.head ? t('HEAD destacado em {sha}', { sha: item.head.slice(0, 8) }) : t('HEAD destacado')
}

/** Keep the end of a long absolute path, where the worktree's own folder is. */
export function shortWorktreePath(path: string): string {
  const parts = path.split(/[\\/]/).filter(Boolean)
  if (!/[\\/]/.test(path) || parts.length <= 4) return path
  return `…/${parts.slice(-3).join('/')}`
}

export function ignoredSummary(item: Worktree): string {
  const total = item.ignored.length + item.ignoredMore
  if (total === 0) return ''
  const names = item.ignored.slice(0, 4).join(', ')
  const rest = total - Math.min(item.ignored.length, 4)
  const items = plural(total, t('item ignorado pelo Git'), t('itens ignorados pelo Git'))
  const list = rest > 0 ? t('{names} e mais {rest}', { names, rest }) : names
  return t('{items}: {list}', { items, list })
}

/** "2 podem ser excluídos" or "1 pode ser excluído": how many worktrees can be deleted. */
export const deletableText = (count: number) => count === 1 ? t('{count} pode ser excluído', { count }) : t('{count} podem ser excluídos', { count })
