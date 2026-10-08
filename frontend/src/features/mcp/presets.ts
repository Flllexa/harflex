import type { MCPServer } from '../../lib/backend'

/** A server the person only has to give a credential to: the address, the way to authenticate and the steps are known. */
export type McpPreset = {
  id: 'github' | 'bitbucket'
  /** Name the server is saved with. */
  name: string
  url: string
  /** How the stored credential is presented: the token itself, or "user:secret". */
  authScheme: 'bearer' | 'basic'
  summary: string
  /** Where the credential comes from, in the order the person does it. */
  steps: readonly string[]
  /** What to check when the server refuses the connection. */
  trouble: string
}

export const mcpPresets: readonly McpPreset[] = [
  {
    id: 'github',
    name: 'GitHub',
    url: 'https://api.githubcopilot.com/mcp/',
    authScheme: 'bearer',
    summary: 'Cria branches, abre e revisa pull requests, lê código e issues. Servidor remoto oficial do GitHub.',
    steps: [
      'Em github.com/settings/tokens, crie um token de acesso pessoal.',
      'Token clássico: marque o escopo repo. Token granular: dê leitura e escrita em Contents e em Pull requests nos repositórios que o agente vai usar.',
      'Cole o token abaixo. Ele fica no cofre do sistema e nunca volta para esta tela.',
    ],
    trouble: 'Confira se o token não expirou e se ele alcança o repositório (escopo repo, ou Contents e Pull requests no token granular).',
  },
  {
    id: 'bitbucket',
    name: 'Bitbucket',
    url: 'https://mcp.atlassian.com/v2/mcp',
    authScheme: 'basic',
    summary: 'Abre e revisa pull requests e lê repositórios do Bitbucket Cloud pelo servidor Rovo MCP da Atlassian.',
    steps: [
      'Em id.atlassian.com, abra Segurança > Tokens de API e crie um token com escopos.',
      'Marque read:bitbucket:agent-interface e write:bitbucket:agent-interface.',
      'O administrador da organização precisa liberar a autenticação por token de API em Atlassian Administration > Rovo > Servidor MCP > Autenticação (vem desligada).',
      'O workspace do Bitbucket precisa estar vinculado a uma organização Atlassian.',
    ],
    trouble: 'Confira o e-mail e o token de API, os dois escopos agent-interface e se o administrador liberou a autenticação por token de API. As ferramentas do Bitbucket só aparecem para workspaces vinculados a uma organização.',
  },
]

const sameEndpoint = (a: string, b: string) => a.replace(/\/+$/, '') === b.replace(/\/+$/, '')

/** The preset a saved server was made from, recognised by its address. */
export function presetOf(server: Pick<MCPServer, 'transport' | 'url'>): McpPreset | undefined {
  return server.transport === 'http' ? mcpPresets.find(preset => sameEndpoint(preset.url, server.url)) : undefined
}

/** The credential as the backend stores it: the token, or "user:secret" for the servers that ask for Basic. */
export function credentialFor(preset: McpPreset, user: string, secret: string): string {
  return preset.authScheme === 'basic' ? `${user.trim()}:${secret.trim()}` : secret.trim()
}
