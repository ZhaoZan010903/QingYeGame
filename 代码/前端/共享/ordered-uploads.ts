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
          results[index].resolve({
            ok: true,
            value: await upload(items[index]),
          });
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
  reorderPending(keys: string[]) {
    const movable = new Set(keys);
    const ordered = keys.filter((key) => this.keys.includes(key));
    let index = 0;
    this.keys = this.keys.map((key) =>
      movable.has(key) ? ordered[index++] : key,
    );
  }
  attached(key: string) {
    return this.images.get(key);
  }
  markAttached(key: string, imageId: string) {
    this.images.set(key, imageId);
  }
  unmarkAttached(key: string) {
    this.images.delete(key);
  }
  before(key: string): string | null {
    const index = this.keys.indexOf(key);
    if (index < 0) return null;
    for (const next of this.keys.slice(index + 1)) {
      const id = this.images.get(next);
      if (id) return id;
    }
    return null;
  }
  reorderImages(ids: string[]) {
    const keysByImage = new Map(
      [...this.images].map(([key, image]) => [image, key]),
    );
    const orderedKeys = ids.flatMap((id) =>
      keysByImage.has(id) ? [keysByImage.get(id)!] : [],
    );
    this.reorderPending(orderedKeys);
  }
  forget(key: string) {
    this.keys = this.keys.filter((item) => item !== key);
    this.images.delete(key);
  }
  removeImage(imageId: string) {
    for (const [key, id] of this.images) if (id === imageId) this.forget(key);
  }
}

/** Invalidate late responses even if navigation changes A -> B -> A. */
export class UploadScope {
  private version = 0;
  constructor(private id?: string) {}
  select(id?: string, adoptCreated = false) {
    if (id === this.id) return;
    if (!(adoptCreated && this.id === undefined)) this.version++;
    this.id = id;
  }
  capture() {
    const version = this.version;
    return () => {
      if (version !== this.version)
        throw new DOMException("当前商品已变更", "AbortError");
    };
  }
}
