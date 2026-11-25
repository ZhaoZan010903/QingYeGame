/** Existing business tests isolate the transport; R24 tests unmock it and exercise real WebCrypto. */
export const plainResponseFixture = {
  fetch: (input: string, init?: RequestInit) => fetch(input, init),
  prepare: async (_url: string, _method?: string, headers?: HeadersInit) => ({
    headers: new Headers(headers),
  }),
  decodeXHR: async (xhr: XMLHttpRequest) =>
    new Response(null, { status: xhr.status }),
  ensure: async () => ({ enabled: false, media_access_plan: false }),
  validateChat: async () => ({ enabled: false }),
  decodeChat: async (value: unknown) => value,
  epoch: () => 0,
  imageCacheIdentity: () => undefined,
  reset: () => {},
  setIdentity: () => {},
  subscribeIdentity: () => () => {},
};
