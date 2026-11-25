import { create } from "zustand";

export type AuthUser = {
  id: string;
  email: string;
  full_name: string;
  email_verified: boolean;
  auth_methods?: string[];
};

type AuthState = {
  /** Access token lives only in memory: never localStorage or sessionStorage. */
  accessToken: string | null;
  user: AuthUser | null;
  setSession: (accessToken: string, user: AuthUser) => void;
  setAccessToken: (accessToken: string) => void;
  setUser: (user: AuthUser) => void;
  clear: () => void;
};

export const useAuthStore = create<AuthState>((set) => ({
  accessToken: null,
  user: null,
  setSession: (accessToken, user) => set({ accessToken, user }),
  setAccessToken: (accessToken) => set({ accessToken }),
  setUser: (user) => set({ user }),
  clear: () => set({ accessToken: null, user: null }),
}));