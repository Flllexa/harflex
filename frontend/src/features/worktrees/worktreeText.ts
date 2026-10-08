import type { Worktree, WorktreeBlocker } from '../../lib/backend'

export const plural = (count: number, one: string, many: string) => `${count} ${count === 1 ? one : many}`

const operations: Record<string, string> = {
  merge: 'um merge', rebase: 'um rebase', 'cherry-pick': 'um cherry-pick', revert: 'um revert', bisect: 'um bisect', sequencer: 'uma sequência de commits',
}

/** One plain sentence per reason a worktree cannot be deleted (or handed to the assistant). */
export function blockerText(blocker: WorktreeBlocker, base: string): string {
  switch (blocker.code) {
    case 'main_worktree': return 'É o worktree principal do repositório.'
    case 'current_project': return 'É o projeto aberto agora. Abra outro projeto para poder excluí-lo.'
    case 'locked': return `Está travado${blocker.detail ? ` (${blocker.detail})` : ''}. Só o destrave manual (git worktree unlock) o libera.`
    case 'missing_directory': return 'A pasta não existe mais; falta limpar o registro dela.'
    case 'status_unavailable': return 'Não foi possível ler o estado deste worktree.'
    case 'in_use': {
      const listed = blocker.detail ? blocker.detail.split(', ').length : 0
      const rest = blocker.count - listed
      return `${plural(blocker.count, 'programa está usando', 'programas estão usando')} esta pasta${blocker.detail ? ` (${blocker.detail}${rest > 0 ? ` e mais ${rest}` : ''})` : ''}. Feche-${blocker.count === 1 ? 'o' : 'os'} e confira de novo.`
    }
    case 'operation_in_progress': return `Há ${operations[blocker.detail] ?? 'uma operação do Git'} em andamento. Conclua ou aborte antes.`
    case 'uncommitted_changes': return `${plural(blocker.count, 'alteração não salva', 'alterações não salvas')} (arquivos editados ou novos).`
    case 'unmerged_commits': return `${plural(blocker.count, 'commit só desta branch', 'commits só desta branch')}, ainda não ${blocker.count === 1 ? 'mesclado' : 'mesclados'} em ${blocker.detail || base}.`
    case 'detached_commits': return `${plural(blocker.count, 'commit solto', 'commits soltos')} (HEAD destacado) que nenhuma branch guarda.`
    case 'base_unknown': return 'Não foi possível identificar a branch base do repositório.'
    case 'base_dirty': return `O worktree principal tem alterações${blocker.count ? ` (${blocker.count})` : ''}; deixe-o limpo antes de mesclar nele.`
    case 'nothing_to_save': return 'Não há nada pendente para salvar.'
    case 'ignored_files': return `${plural(blocker.count, 'item ignorado pelo Git', 'itens ignorados pelo Git')} serão apagados junto.`
    default: return 'Há um impedimento que o Harflex não reconhece; por segurança, nada será excluído.'
  }
}

/** The facts under a worktree's name: what is pending and where its commits stand. */
export function worktreeFacts(item: Worktree, base: string): string[] {
  const facts: string[] = []
  if (item.missing) return ['Pasta ausente']
  if (!item.statusKnown) return ['Estado ilegível']
  if (item.changed > 0) facts.push(plural(item.changed, 'alteração não salva', 'alterações não salvas'))
  else facts.push('Sem alterações pendentes')
  if (item.isMain) return facts
  if (item.ahead > 0) facts.push(`${plural(item.ahead, 'commit', 'commits')} à frente de ${base}`)
  else if (base) facts.push(`Mesclado em ${base}`)
  if (item.behind > 0) facts.push(`${item.behind} atrás de ${base}`)
  if (item.operation) facts.push(`${operations[item.operation] ?? 'Operação'} em andamento`)
  return facts
}

export function worktreeTitle(item: Worktree): string {
  if (item.branch) return item.branch
  return item.head ? `HEAD destacado em ${item.head.slice(0, 8)}` : 'HEAD destacado'
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
  return `${plural(total, 'item ignorado pelo Git', 'itens ignorados pelo Git')}: ${names}${rest > 0 ? ` e mais ${rest}` : ''}`
}
