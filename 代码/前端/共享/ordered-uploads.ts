/** Upload bytes concurrently; commit each successful result in the user's selection order. */
export async function runOrderedUploads<T, R>(
  items: T[],
  concurrency: number,
  upload: (item: T) => Promise<R>,
  commit: (item: T, result: R) => Promise<void>,
  fail: (item: T, error: unknown) => void,
) {
  type Result = { ok: true; value: R } | { ok: false; error: unknown };
  const results = items.map(() => {
    let resolve!: (result: Result) => void;
    const promise = new Promise<Result>((done) => {
      resolve = done;
    });
    return { promise, resolve };
  });
  let next = 0;
  const workers = Array.from(
    { length: Math.min(items.length, Math.max(1, concurrency)) },
    async () => {
      while (next < items.length) {
        const index = next++;
        try {
          results[index].resolve({ ok: true, value: await upload(items[index]) });
        } catch (error) {
          results[index].resolve({ ok: false, error });
        }
      }
    },
  );
  for (let index = 0; index < items.length; index++) {
    const result = await results[index].promise;
    if (!result.ok) {
      fail(items[index], result.error);
      continue;
    }
    try {
      await commit(items[index], result.value);
    } catch (error) {
      fail(items[index], error);
    }
  }
  await Promise.all(workers);
}

/** Retain retry anchors across batches without retaining files or object URLs. */
export class UploadSelectionOrder {
  private keys: string[] = [];
  private images = new Map<string, string>();
  register(keys: string[]) {
    this.keys.push(...keys.filter((key) => !this.keys.includes(key)));
  }
  attached(key: string) {
    return this.images.get(key);
  }
  markAttached(key: string, imageId: string) {
    this.images.set(key, imageId);
  }
  before(key: string): string | null {
    for (const next of this.keys.slice(this.keys.indexOf(key) + 1)) {
      const id = this.images.get(next);
      if (id) return id;
    }
    return null;
  }
  forget(key: string) {
    this.keys = this.keys.filter((item) => item !== key);
    this.images.delete(key);
  }
  removeImage(imageId: string) {
    for (const [key, id] of this.images) if (id === imageId) this.forget(key);
  }
}
