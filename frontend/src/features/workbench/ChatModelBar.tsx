import { useState } from 'react'
import { Button, Dialog, DialogTrigger, Popover, Radio, RadioGroup } from 'react-aria-components'
import { Bot, Check, ChevronDown, Gauge, Server, Sparkles } from 'lucide-react'
import type { IonOption } from '../../components/IonPicker'
import type { BackendOption } from '../../lib/backend'
import './casualStart.css'

type Props = {
  backends: BackendOption[]
  backendId: string
  onBackend: (value: string) => void
  /** The models the chosen CLI lists; empty while they are being read. */
  modelOptions: IonOption[]
  modelId: string
  onModel: (value: string) => void
  /** An API profile has its model in the profile itself: it is shown, not chosen here. */
  fixedModel?: string
  efforts: string[]
  effort: string
  onEffort: (value: string) => void
  loading?: boolean
  disabled?: boolean
}

const effortLabel = (value: string) => value === '' ? 'Automático' : value

/** Provider, model and effort of a chat behind one compact button: the same control before and during a conversation. */
export function ChatModelBar({ backends, backendId, onBackend, modelOptions, modelId, onModel, fixedModel, efforts, effort, onEffort, loading = false, disabled = false }: Props) {
  const [open, setOpen] = useState(false)
  const option = backends.find(item => item.id === backendId)
  const model = modelOptions.find(item => item.value === modelId)
  const modelText = option?.kind === 'api' ? fixedModel || 'Modelo do perfil' : loading ? 'Lendo modelos…' : model?.label ?? 'Escolha um modelo'
  const summary = option ? [option.name, modelText, ...(option.kind === 'cli' && efforts.length > 0 ? [effortLabel(effort)] : [])] : ['Escolha um provedor']
  const incomplete = !option || option.kind === 'cli' && !loading && !model

  return <DialogTrigger isOpen={open} onOpenChange={setOpen}>
    <Button className={`chat-model-trigger${incomplete ? ' is-incomplete' : ''}`} isDisabled={disabled} aria-label={`Provedor, modelo e esforço: ${summary.join(', ')}`}>
      <Sparkles aria-hidden="true" className="chat-model-trigger-icon" />
      <span className="chat-model-trigger-text">{summary.map((part, index) => <span key={index} className={index === 0 ? 'is-provider' : undefined}>{part}</span>)}</span>
      <ChevronDown aria-hidden="true" className="chat-model-trigger-chevron" />
    </Button>
    <Popover className="chat-model-popover" placement="top start" offset={8}>
      <Dialog className="chat-model-dialog" aria-label="Provedor, modelo e esforço">
        <RadioGroup className="chat-model-section" aria-label="Provedor do chat" value={backendId} onChange={onBackend}>
          <span className="chat-model-section-title"><Server aria-hidden="true" />Provedor</span>
          <div className="chat-model-options">
            {backends.map(item => <Radio key={item.id} value={item.id} isDisabled={!item.available} className="chat-model-option">
              {({ isSelected }) => <>
                <span className="chat-model-option-name">{item.name}</span>
                <span className="chat-model-badge">{item.available ? item.kind === 'api' ? 'API' : 'CLI' : 'indisponível'}</span>
                <Check aria-hidden="true" className={`chat-model-check${isSelected ? ' is-on' : ''}`} />
              </>}
            </Radio>)}
          </div>
        </RadioGroup>

        {option && <div className="chat-model-section">
          <span className="chat-model-section-title"><Bot aria-hidden="true" />Modelo</span>
          {option.kind === 'api'
            ? <p className="chat-model-fixed-note"><strong>{fixedModel || 'Modelo do perfil'}</strong><span className="muted">Definido no perfil do provedor, em Configurações.</span></p>
            : loading ? <p className="chat-model-fixed-note muted" role="status">Lendo os modelos de {option.name}…</p>
              : modelOptions.length === 0 ? <p className="chat-model-fixed-note muted">{option.name} não listou modelos.</p>
                : <RadioGroup aria-label="Modelo da sessão" value={modelId} onChange={value => { onModel(value) }} className="chat-model-options">
                  {modelOptions.map(item => <Radio key={item.value} value={item.value} isDisabled={item.disabled} className="chat-model-option">
                    {({ isSelected }) => <><span className="chat-model-option-name">{item.label}</span><Check aria-hidden="true" className={`chat-model-check${isSelected ? ' is-on' : ''}`} /></>}
                  </Radio>)}
                </RadioGroup>}
        </div>}

        {option?.kind === 'cli' && efforts.length > 0 && <RadioGroup className="chat-model-section" aria-label="Esforço do modelo" value={effort} onChange={onEffort}>
          <span className="chat-model-section-title"><Gauge aria-hidden="true" />Esforço</span>
          <div className="chat-model-segments">
            {['', ...efforts].map(value => <Radio key={value || 'auto'} value={value} className="chat-model-segment">{effortLabel(value)}</Radio>)}
          </div>
        </RadioGroup>}
      </Dialog>
    </Popover>
  </DialogTrigger>
}
