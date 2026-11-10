import { useEffect, useState } from 'react';
export function useLoad<T>(load: () => Promise<T>, key: string) {
  const [data, setData] = useState<T>();
  const [error, setError] = useState<unknown>();
  const [loading, setLoading] = useState(true);
  const [attempt, setAttempt] = useState(0);
  useEffect(() => {
    let live = true;
    setLoading(true);
    setError(undefined);
    setData(undefined);
    load()
      .then((v) => {
        if (live) setData(v);
      })
      .catch((e) => {
        if (live) setError(e);
      })
      .finally(() => {
        if (live) setLoading(false);
      });
    return () => {
      live = false;
    };
  }, [key, attempt]);
  return { data, error, loading, retry: () => setAttempt((n) => n + 1) };
}
