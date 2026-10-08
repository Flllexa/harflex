import { describe, expect, it } from 'vitest'
import { blocker, detachedWorktree, ignoredWorktree, lockedWorktree, mainWorktree, missingWorktree, pendingWorktree, worktreeItem } from '../../test/worktreeFixture'
import { blockerText, ignoredSummary, plural, shortWorktreePath, worktreeFacts, worktreeTitle } from './worktreeText'

describe('worktree wording', () => {
  it('pluralises counts in Portuguese', () => {
    expect(plural(1, 'commit', 'commits')).toBe('1 commit')
    expect(plural(0, 'commit', 'commits')).toBe('0 commits')
    expect(plural(3, 'commit', 'commits')).toBe('3 commits')
  })

  it('says why a worktree is held in one plain sentence per reason', () => {
    expect(blockerText(blocker('uncommitted_changes', 1), 'main')).toBe('1 alteração não salva (arquivos editados ou novos).')
    expect(blockerText(blocker('uncommitted_changes', 3), 'main')).toBe('3 alterações não salvas (arquivos editados ou novos).')
    expect(blockerText(blocker('unmerged_commits', 2, 'develop'), 'main')).toBe('2 commits só desta branch, ainda não mesclados em develop.')
    expect(blockerText(blocker('unmerged_commits', 1), 'main')).toBe('1 commit só desta branch, ainda não mesclado em main.')
    expect(blockerText(blocker('detached_commits', 1), 'main')).toBe('1 commit solto (HEAD destacado) que nenhuma branch guarda.')
    expect(blockerText(blocker('locked', 0, 'em uso no CI'), 'main')).toContain('Está travado (em uso no CI)')
    expect(blockerText(blocker('locked'), 'main')).toContain('Está travado.')
    expect(blockerText(blocker('operation_in_progress', 0, 'rebase'), 'main')).toBe('Há um rebase em andamento. Conclua ou aborte antes.')
    expect(blockerText(blocker('operation_in_progress', 0, 'algo-novo'), 'main')).toBe('Há uma operação do Git em andamento. Conclua ou aborte antes.')
    expect(blockerText(blocker('in_use', 1, 'zsh (pid 4242)'), 'main')).toBe('1 programa está usando esta pasta (zsh (pid 4242)). Feche-o e confira de novo.')
    expect(blockerText(blocker('in_use', 2, 'zsh (pid 4242), code (pid 4300)'), 'main')).toBe('2 programas estão usando esta pasta (zsh (pid 4242), code (pid 4300)). Feche-os e confira de novo.')
    expect(blockerText(blocker('in_use', 5, 'zsh (pid 1), node (pid 2), claude (pid 3)'), 'main')).toContain('(zsh (pid 1), node (pid 2), claude (pid 3) e mais 2)')
    expect(blockerText(blocker('in_use', 1), 'main')).toBe('1 programa está usando esta pasta. Feche-o e confira de novo.')
    expect(blockerText(blocker('main_worktree'), 'main')).toBe('É o worktree principal do repositório.')
    expect(blockerText(blocker('current_project'), 'main')).toContain('projeto aberto agora')
    expect(blockerText(blocker('missing_directory'), 'main')).toContain('A pasta não existe mais')
    expect(blockerText(blocker('status_unavailable'), 'main')).toContain('Não foi possível ler')
    expect(blockerText(blocker('base_unknown'), 'main')).toContain('branch base')
    expect(blockerText(blocker('base_dirty', 4, 'main'), 'main')).toBe('O worktree principal tem alterações (4); deixe-o limpo antes de mesclar nele.')
    expect(blockerText(blocker('base_dirty'), 'main')).toBe('O worktree principal tem alterações; deixe-o limpo antes de mesclar nele.')
    expect(blockerText(blocker('nothing_to_save'), 'main')).toBe('Não há nada pendente para salvar.')
    expect(blockerText(blocker('ignored_files', 2), 'main')).toBe('2 itens ignorados pelo Git serão apagados junto.')
  })

  it('never invents a verdict for a reason it does not know', () => {
    expect(blockerText(blocker('algo_do_futuro'), 'main')).toContain('por segurança, nada será excluído')
  })

  it('lists what is pending and where the commits stand', () => {
    expect(worktreeFacts(worktreeItem(), 'main')).toEqual(['Sem alterações pendentes', 'Mesclado em main'])
    expect(worktreeFacts(pendingWorktree(), 'main')).toEqual(['3 alterações não salvas', '2 commits à frente de main'])
    expect(worktreeFacts(pendingWorktree({ changed: 1, ahead: 1 }), 'main')).toEqual(['1 alteração não salva', '1 commit à frente de main'])
    expect(worktreeFacts(worktreeItem({ behind: 4 }), 'main')).toEqual(['Sem alterações pendentes', 'Mesclado em main', '4 atrás de main'])
    expect(worktreeFacts(worktreeItem({ operation: 'merge' }), 'main')).toContain('um merge em andamento')
    expect(worktreeFacts(mainWorktree(), 'main')).toEqual(['Sem alterações pendentes'])
    expect(worktreeFacts(missingWorktree(), 'main')).toEqual(['Pasta ausente'])
    expect(worktreeFacts(worktreeItem({ statusKnown: false }), 'main')).toEqual(['Estado ilegível'])
    expect(worktreeFacts(lockedWorktree(), 'main')).toEqual(['Sem alterações pendentes', 'Mesclado em main'])
  })

  it('names a worktree by its branch, or by the commit a detached one stands on', () => {
    expect(worktreeTitle(worktreeItem())).toBe('feature/exportacao')
    expect(worktreeTitle(detachedWorktree())).toBe('HEAD destacado em 4d5e6f7a')
    expect(worktreeTitle(detachedWorktree({ head: '' }))).toBe('HEAD destacado')
  })

  it('keeps the end of a long path, where the worktree folder is', () => {
    expect(shortWorktreePath('/Users/dev/Projects/harflex/.claude/worktrees/tarefa')).toBe('…/.claude/worktrees/tarefa')
    expect(shortWorktreePath('C:\\Users\\dev\\Projects\\harflex\\worktrees\\tarefa')).toBe('…/harflex/worktrees/tarefa')
    expect(shortWorktreePath('/a/b/c')).toBe('/a/b/c')
    expect(shortWorktreePath('relativo')).toBe('relativo')
  })

  it('summarises ignored files without listing a whole node_modules', () => {
    expect(ignoredSummary(worktreeItem())).toBe('')
    expect(ignoredSummary(ignoredWorktree())).toBe('5 itens ignorados pelo Git: node_modules/, .env.local, dist/ e mais 2')
    expect(ignoredSummary(worktreeItem({ ignored: ['.env'], ignoredMore: 0 }))).toBe('1 item ignorado pelo Git: .env')
    expect(ignoredSummary(worktreeItem({ ignored: ['a', 'b', 'c', 'd', 'e'], ignoredMore: 0 }))).toBe('5 itens ignorados pelo Git: a, b, c, d e mais 1')
  })
})
