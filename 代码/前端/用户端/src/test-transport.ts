import { vi } from 'vitest';

vi.mock('../../共享/response-crypto', async (original) => {
  const actual = await original<typeof import('../../共享/response-crypto')>();
  const { plainResponseFixture } = await import('../../共享/plain-response-fixture');
  return { ...actual, responseCrypto: plainResponseFixture };
});
