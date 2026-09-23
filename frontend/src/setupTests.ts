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

// jsdom doesn't implement IntersectionObserver — polyfill it as a no-op so components using it
// for infinite scroll (e.g. RecentTradesPage) don't crash on mount in tests. Tests that need to
// exercise the "load more" trigger invoke it manually rather than relying on this stub to fire.
if (typeof window !== 'undefined' && !window.IntersectionObserver) {
  class NoopIntersectionObserver implements IntersectionObserver {
    readonly root: Element | Document | null = null
    readonly rootMargin: string = ''
    readonly thresholds: ReadonlyArray<number> = []
    observe() {}
    unobserve() {}
    disconnect() {}
    takeRecords(): IntersectionObserverEntry[] { return [] }
  }
  window.IntersectionObserver = NoopIntersectionObserver as unknown as typeof IntersectionObserver
}
