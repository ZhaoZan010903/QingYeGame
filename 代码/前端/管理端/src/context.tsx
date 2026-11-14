import {
  Fragment,
  createContext,
  useCallback,
  useContext,
  useEffect,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { api } from "./lib/api";
import type { User } from "./types";
import { responseCrypto } from "../../共享/response-crypto";
import { resetMediaIdentity } from "../../共享/media-access-client";
import { chatTransport } from "./lib/chat-api";
type LoginNotice =
  "password-changed" | "password-uncertain" | "session-expired" | null;
const Session = createContext<{
  user: User | null;
  loading: boolean;
  error: unknown;
  refresh: () => Promise<void>;
  invalidate: (notice?: LoginNotice) => void;
  loginNotice: LoginNotice;
  profileRevision: number;
}>(null!);
export const useSession = () => useContext(Session);
export function Providers({ children }: { children: ReactNode }) {
  const generation = useRef(0);
  const [identityEpoch, setIdentityEpoch] = useState(0);
  const [profileRevision, setProfileRevision] = useState(0);
  const [loginNotice, setLoginNotice] = useState<LoginNotice>(null);
  const [user, setUser] = useState<User | null>(null),
    [loading, setLoading] = useState(true),
    [error, setError] = useState<unknown>();
  const refresh = useCallback(async () => {
    const current = ++generation.current;
    setError(undefined);
    try {
      const latest = await api.me();
      if (generation.current !== current) return;
      setUser(latest);
      if (latest) setLoginNotice(null);
      setProfileRevision((value) => value + 1);
    } catch (e) {
      if (generation.current !== current) return;
      setUser(null);
      setError(e);
    } finally {
      if (generation.current === current) setLoading(false);
    }
  }, []);
  const invalidate = useCallback((notice: LoginNotice = null) => {
    generation.current++;
    // A password-result warning deliberately stays signed out until an explicit login.
    responseCrypto.reset(true, false);
    chatTransport.disconnect();
    resetMediaIdentity();
    setLoginNotice(notice);
    setUser(null);
    setError(undefined);
    setLoading(false);
    setProfileRevision((value) => value + 1);
    setIdentityEpoch((value) => value + 1);
  }, []);
  useEffect(
    () =>
      responseCrypto.subscribeIdentity(() => {
        generation.current++;
        chatTransport.disconnect();
        resetMediaIdentity();
        setUser(null);
        setError(undefined);
        setLoading(true);
        setIdentityEpoch((value) => value + 1);
        void refresh();
      }),
    [refresh],
  );
  useEffect(() => {
    void refresh();
    return () => {
      generation.current++;
    };
  }, [refresh]);
  return (
    <Session.Provider
      value={{
        user,
        loading,
        error,
        refresh,
        invalidate,
        profileRevision,
        loginNotice,
      }}
    >
      <Fragment key={identityEpoch}>{children}</Fragment>
    </Session.Provider>
  );
}
