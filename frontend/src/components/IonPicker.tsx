import { ChevronDown } from 'lucide-react'
import { useContext, useLayoutEffect, useRef, useState } from 'react'
import { Button, ComboBox, ComboBoxStateContext, Group, Input, Label, ListBox, ListBoxItem, Popover, Select, SelectValue } from 'react-aria-components'
import { useFilter } from 'react-aria-components/Autocomplete'
import { I18nProvider } from 'react-aria-components/I18nProvider'
import { useT } from '../i18n'
import './IonPicker.css'

export type IonOption = { value: string; label: string; disabled?: boolean }

export type IonPickerProps = {
  id: string
  label: string
  value: string
  onChange: (value: string) => void
  options: readonly IonOption[]
  required?: boolean
  disabled?: boolean
  searchable?: boolean
  /** A single-line trigger for toolbars: the label stays for assistive technology but is not drawn. */
  compact?: boolean
}

const keyOf = (value: string) => `v:${value}`

function OpenComboBoxAfterInput({ revision }: { revision: number }) {
  const state = useContext(ComboBoxStateContext)
  useLayoutEffect(() => {
    if (revision > 0) state?.open()
  }, [revision])
  return null
}

export function IonPicker({ id, label, value, onChange, options, required = false, disabled = false, searchable = false, compact = false }: IonPickerProps) {
  const t = useT()
  const { contains } = useFilter({ sensitivity: 'base' })
  const items = options.map(option => ({ ...option, id: keyOf(option.value) }))
  const current = options.find(option => option.value === value)
  const selected = current && (!required || value !== '') ? keyOf(value) : null
  const placeholder = options.find(option => option.value === '')?.label ?? t('Escolha uma opção')
  const [inputText, setInputText] = useState(value === '' ? '' : current?.label ?? '')
  const [openRequestRevision, setOpenRequestRevision] = useState(0)
  const lastLocalValue = useRef(value)
  const inputRef = useRef<HTMLInputElement>(null)
  const comboState = useRef<{ isOpen: boolean; open: () => void; close: () => void }>()
  const requiredSelectionInvalid = required && (selected === null || inputText !== current?.label)
  useLayoutEffect(() => {
    // Empty required values are placeholders, not selections; a cleared search also stays blank.
    if (lastLocalValue.current !== value) {
      comboState.current?.close()
      inputRef.current?.blur()
    }
    lastLocalValue.current = value
    setInputText(previous => value === '' && (required || previous === '') ? '' : current?.label ?? '')
  }, [value, current?.label, required])
  const commit = (key: string | number | null) => {
    if (key === null) return
    const next = String(key).slice(2)
    if (searchable) setInputText(required && next === '' ? '' : options.find(option => option.value === next)?.label ?? '')
    if (next !== value) { lastLocalValue.current = next; onChange(next) }
  }
  const changeInput = (text: string) => {
    setInputText(text)
    if (text === '' && value !== '' && options.some(option => option.value === '')) { lastLocalValue.current = ''; onChange('') }
  }
  const list = <ListBox className="ion-picker-list" items={items} renderEmptyState={() => <span className="ion-picker-empty">{t('Nenhuma opção encontrada.')}</span>}>
    {item => <ListBoxItem id={item.id} textValue={item.label} isDisabled={item.disabled} className="ion-picker-option"><span title={item.label}>{item.label}</span></ListBoxItem>}
  </ListBox>
  const popover = <Popover className="ion-picker-popover" data-ion-picker-popover placement="bottom start" offset={4}>{list}</Popover>

  return <I18nProvider locale="pt-BR"><div data-testid={`picker-${id}`} className={`ion-picker${compact ? ' ion-picker-compact' : ''}`}>
    {searchable ? <ComboBox value={selected} onChange={commit} inputValue={inputText} onInputChange={text => {
      changeInput(text)
      const isCompletedSelection = !!text && options.some(option => option.label === text) && comboState.current?.isOpen
      if (!isCompletedSelection) setOpenRequestRevision(revision => revision + 1)
    }} isRequired={required} isInvalid={requiredSelectionInvalid} isDisabled={disabled} defaultFilter={contains} allowsEmptyCollection menuTrigger="manual">
      <ComboBoxStateContext.Consumer>{state => {
        comboState.current = state ? { isOpen: state.isOpen, open: () => state.open(), close: () => state.close() } : undefined
        return <>
          <OpenComboBoxAfterInput revision={openRequestRevision} />
          <Label className={`ion-picker-label${compact ? ' visually-hidden' : ''}`}>{label}</Label>
          {/* A click or the arrow opens every option; typing filters, and replaces the chosen name instead of adding to it. */}
          <Group className="ion-picker-group"><Input ref={inputRef} className="ion-picker-input" placeholder={placeholder} title={current?.label} onPointerDown={() => state?.open(null, 'manual')}
            onPointerUp={event => { if (current && event.currentTarget.value === current.label) event.currentTarget.select() }}
            onFocus={event => { if (current && event.currentTarget.value === current.label) event.currentTarget.select() }} onKeyDown={event => {
            if (event.key === 'ArrowDown') state?.open(null, 'manual')
          }} /><Button type="button" className="ion-picker-chevron" aria-label={t('Abrir opções de {label}', { label })}><ChevronDown aria-hidden="true" /></Button></Group>
        </>
      }}</ComboBoxStateContext.Consumer>
      {popover}
    </ComboBox> : <Select value={selected} onChange={commit} isRequired={required} isDisabled={disabled} allowsEmptyCollection placeholder={placeholder}>
      <Label className={`ion-picker-label${compact ? ' visually-hidden' : ''}`}>{label}</Label>
      <Button type="button" className="ion-picker-trigger" ref={element => {
        if (!element) return
        if (current) element.title = current.label
        else element.removeAttribute('title')
      }}><SelectValue /><ChevronDown aria-hidden="true" /></Button>
      {popover}
    </Select>}
  </div></I18nProvider>
}
