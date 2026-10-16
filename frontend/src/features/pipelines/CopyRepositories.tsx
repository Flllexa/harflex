import { GitBranch, RefreshCw } from 'lucide-react'
import type { PipelineCodeCopyPreview } from '../../lib/backend'
import { useT } from '../../i18n'

const skipReasons: Record<string, string> = {
  uncommitted_changes: 'tem alterações não commitadas',
  no_commits: 'ainda não tem commits',
  submodules: 'usa submódulos, que a cópia não leva',
  repository_limit: 'passou do limite de 60 repositórios',
}

/**
 * The repositories inside the project (such as domains/<name>) that join the Code copy, each applied back on its own
 * branch, and the ones that stay out with the reason and the way to bring them in.
 */
export function CopyRepositories({ preview, onRefresh, disabled }: { preview: PipelineCodeCopyPreview; onRefresh: () => void; disabled?: boolean }) {
  const t = useT()
  if (!preview.repositories.length && !preview.skippedRepositories.length) return null
  const fixable = preview.skippedRepositories.some(item => item.reason === 'uncommitted_changes')
  return <div className="copy-repositories">
    {preview.repositories.length > 0 && <details>
      <summary><GitBranch aria-hidden="true" />{t(preview.repositories.length === 1 ? '{count} repositório interno entra na cópia' : '{count} repositórios internos entram na cópia', { count: preview.repositories.length })}</summary>
      <p className="muted">{t('As mudanças em cada um voltam para ele, na branch em que estava, e cada um ganha o seu pull request.')}</p>
      <ul>{preview.repositories.map(item => <li key={item.path}><span className="mono">{item.path}</span> <span className="muted">· {item.branch}</span></li>)}</ul>
    </details>}
    {preview.skippedRepositories.length > 0 && <div className="project-warning" role="status">
      <p>{t('Estes repositórios internos ficam fora da cópia, e o Coder não poderá alterá-los:')}</p>
      <ul>{preview.skippedRepositories.map(item => <li key={item.path}><span className="mono">{item.path}</span> — {t(skipReasons[item.reason] ?? 'não pôde ser lido')}</li>)}</ul>
      {fixable && <p>{t('Faça commit ou stash das alterações nesses repositórios e atualize a prévia para incluí-los.')}</p>}
      <button type="button" className="touch-target secondary-button" onClick={onRefresh} disabled={disabled}><RefreshCw aria-hidden="true" />{t('Atualizar prévia')}</button>
    </div>}
  </div>
}
