export type AppMode = 'casual' | 'professional'
export type AppModeStorage = Pick<Storage, 'getItem' | 'setItem'>

const storageKey = 'harflex:view-mode'

function defaultStorage(): AppModeStorage | undefined {
  try {
    return typeof window === 'undefined' ? undefined : window.localStorage
  } catch {
    return undefined
  }
}

export function readAppMode(storage: AppModeStorage | undefined = defaultStorage()): AppMode {
  try {
    return storage?.getItem(storageKey) === 'casual' ? 'casual' : 'professional'
  } catch {
    return 'professional'
  }
}

export function writeAppMode(mode: AppMode, storage: AppModeStorage | undefined = defaultStorage()): void {
  try {
    storage?.setItem(storageKey, mode)
  } catch {
    // View choice remains usable for this run when browser storage is unavailable.
  }
}
