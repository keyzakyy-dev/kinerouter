import '@testing-library/dom'

// jsdom has no matchMedia; the sidebar primitive reads it to pick the desktop rail.
if (!window.matchMedia) {
  window.matchMedia = (query: string) =>
    ({
      // jsdom is a desktop viewport: min-width queries match, media prefs do not.
      matches: query.includes('min-width'),
      media: query,
      onchange: null,
      addEventListener: () => {},
      removeEventListener: () => {},
      addListener: () => {},
      removeListener: () => {},
      dispatchEvent: () => false,
    }) as MediaQueryList
}

// jsdom has no Web Animations API; the sidebar's collapse runs on it.
if (!Element.prototype.animate) {
  Element.prototype.animate = () =>
    ({ finished: Promise.resolve(), cancel() {}, currentTime: 0, effect: null }) as unknown as Animation
}
