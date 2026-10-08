/**
 * Tags the document with the host OS. On macOS the window hides its native title bar, so the traffic
 * lights float over the sidebar's top-left corner and the layout reserves a safe area for them.
 */
export function markPlatform(root: HTMLElement = document.documentElement, platform: string = navigator.platform || navigator.userAgent) {
  root.dataset.platform = /mac/i.test(platform) ? 'mac' : /win/i.test(platform) ? 'windows' : 'other'
}
