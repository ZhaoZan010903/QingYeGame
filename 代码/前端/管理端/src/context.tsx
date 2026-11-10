import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useState,
  type ReactNode,
} from "react";
import { api } from "./lib/api";
import type { User } from "./types";
const Session = createContext<{
  user: User | null;
  loading: boolean;
  error: unknown;
  refresh: () => Promise<void>;
}>(null!);
export const useSession = () => useContext(Session);
export function Providers({ children }: { children: ReactNode }) {
  const [user, setUser] = useState<User | null>(null),
    [loading, setLoading] = useState(true),
    [error, setError] = useState<unknown>();
  const refresh = useCallback(async () => {
    setError(undefined);
    try {
      setUser(await api.me());
    } catch (e) {
      setUser(null);
      setError(e);
    } finally {
      setLoading(false);
    }
  }, []);
  useEffect(() => {
    void refresh();
  }, [refresh]);
  return (
    <Session.Provider value={{ user, loading, error, refresh }}>
      {children}
    </Session.Provider>
  );
}
