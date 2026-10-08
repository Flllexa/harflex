import { useState } from 'react'
import { createRoot } from 'react-dom/client'
import { IonPicker } from '../components/IonPicker'
import '../styles/global.css'

function Fixture() {
  const [value, setValue] = useState('b')
  const [workflow, setWorkflow] = useState('b')
  const [submissions, setSubmissions] = useState(0)
  return <main style={{ width: 'min(440px, calc(100vw - 32px))', margin: 16 }}>
    <output data-testid="selected-value">{value || 'vazio'}</output>
    <IonPicker id="browser-document" label="Documento" value={value} onChange={setValue} searchable
      options={[{ value: '', label: 'Todos os documentos' }, { value: 'a', label: 'Arquitetura' }, { value: 'b', label: 'Código' }, { value: 'long-document', label: 'docs/plataforma/fluxos-e-integracoes/decisoes-de-discovery-e-revisao-de-implementacao.md' }]} />
    <form data-testid="required-form" onSubmit={event => { event.preventDefault(); setSubmissions(count => count + 1) }}>
      <output data-testid="workflow-value">{workflow || 'vazio'}</output>
      <output data-testid="workflow-submissions">{submissions}</output>
      <IonPicker id="browser-workflow" label="Workflow" value={workflow} onChange={setWorkflow} searchable required
        options={[{ value: '', label: 'Escolha workflow' }, { value: 'b', label: 'Workflow B' }]} />
      <button type="button" onClick={() => setWorkflow('')}>Reset workflow</button>
      <button type="submit">Salvar workflow</button>
    </form>
  </main>
}

createRoot(document.getElementById('root')!).render(<Fixture />)
