// Only freshly authorized public product representations receive a cache key.
// Signed URLs and authorization results are never saved in this database.
const databaseName = "qingye-public-product-images-v1";
const maxBytes = 64 * 1024 * 1024;
const maxItemBytes = 8 * 1024 * 1024;
const maxEntries = 256;
const lifetime = 24 * 60 * 60 * 1000;
type Entry = { key: string; bytes: number; touched: number; expires: number };
let opening: Promise<IDBDatabase | undefined> | undefined;

function openCache() {
  if (!opening) {
    opening = new Promise<IDBDatabase | undefined>((resolve) => {
      let timedOut = false;
      // Contended or disabled storage must not delay the first image.
      const timer = setTimeout(() => {
        timedOut = true;
        resolve(undefined);
      }, 300);
      try {
        const req = indexedDB.open(databaseName, 1);
        req.onupgradeneeded = () => {
          req.result.createObjectStore("index", { keyPath: "key" });
          req.result.createObjectStore("images");
        };
        req.onsuccess = () => {
          clearTimeout(timer);
          const db = req.result;
          db.onversionchange = () => {
            db.close();
            opening = undefined;
          };
          if (timedOut) opening = Promise.resolve(db);
          else resolve(db);
        };
        req.onerror = () => {
          clearTimeout(timer);
          resolve(undefined);
        };
      } catch {
        clearTimeout(timer);
        resolve(undefined);
      }
    });
  }
  return opening;
}

function removeEntry(tx: IDBTransaction, key: string) {
  tx.objectStore("index").delete(key);
  tx.objectStore("images").delete(key);
}

export async function readProductImage(key: string): Promise<Blob | undefined> {
  const db = await openCache();
  if (!db) return;
  return new Promise((resolve) => {
    const tx = db.transaction(["index", "images"], "readwrite");
    const index = tx.objectStore("index");
    const req = index.get(key);
    let image: Blob | undefined;
    req.onsuccess = () => {
      const entry: Entry | undefined = req.result;
      if (!entry) return;
      if (entry.expires <= Date.now()) {
        removeEntry(tx, key);
        return;
      }
      const value = tx.objectStore("images").get(key);
      value.onsuccess = () => {
        const blob = value.result;
        if (blob instanceof Blob && blob.size === entry.bytes) {
          image = blob;
          index.put({ ...entry, touched: Date.now() });
        } else removeEntry(tx, key);
      };
    };
    tx.oncomplete = () => resolve(image);
    tx.onabort = () => resolve(undefined);
  });
}

export async function storeProductImage(
  key: string,
  image: Blob,
): Promise<void> {
  if (!image.size || image.size > maxItemBytes) return;
  const db = await openCache();
  if (!db) return;
  await new Promise<void>((resolve) => {
    const tx = db.transaction(["index", "images"], "readwrite");
    const index = tx.objectStore("index");
    const req = index.getAll(); // Metadata only; image bytes live in a separate store.
    req.onsuccess = () => {
      const now = Date.now();
      const entries: Entry[] = req.result;
      let bytes = image.size;
      let count = 1;
      for (const entry of entries.sort((a, b) => b.touched - a.touched)) {
        if (entry.key === key) continue;
        if (
          entry.expires <= now ||
          bytes + entry.bytes > maxBytes ||
          count >= maxEntries
        ) {
          removeEntry(tx, entry.key);
        } else {
          bytes += entry.bytes;
          count++;
        }
      }
      index.put({
        key,
        bytes: image.size,
        touched: now,
        expires: now + lifetime,
      });
      tx.objectStore("images").put(image, key);
    };
    tx.oncomplete = tx.onabort = () => resolve();
  });
}

export async function discardProductImage(key: string) {
  const db = await openCache();
  if (!db) return;
  const tx = db.transaction(["index", "images"], "readwrite");
  removeEntry(tx, key);
}

export async function clearProductImageIdentity(identity: string) {
  const db = await openCache();
  if (!db) return;
  const prefix = JSON.stringify([identity]).slice(0, -1) + ",";
  const tx = db.transaction(["index", "images"], "readwrite");
  const keys = tx
    .objectStore("index")
    .getAllKeys(IDBKeyRange.bound(prefix, prefix + "\uffff"));
  keys.onsuccess = () => {
    for (const key of keys.result) removeEntry(tx, String(key));
  };
}
