import '@testing-library/jest-dom'

// jsdom doesn't implement matchMedia — polyfill it so components using responsive
// hooks (e.g. DashboardPage's useIsMobile) don't crash on mount in tests.
if (typeof window !== 'undefined' && !window.matchMedia) {
  window.matchMedia = (query: string) => ({
    matches: false,
    media: query,
    onchange: null,
    addListener: () => {},
    removeListener: () => {},
    addEventListener: () => {},
    removeEventListener: () => {},
    dispatchEvent: () => false,
  }) as unknown as MediaQueryList
}
