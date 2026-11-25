import { api } from "../../lib/api";

export type Workspace = {
  id: string;
  name: string;
  slug: string;
  role: string;
};

export const workspacesApi = {
  list: () => api<{ data: Workspace[]; meta: { total: number } }>("/workspaces"),
  get: (id: string) => api<Workspace>(`/workspaces/${id}`),
  create: (name: string) => api<Workspace>("/workspaces", { method: "POST", body: { name } }),
};
