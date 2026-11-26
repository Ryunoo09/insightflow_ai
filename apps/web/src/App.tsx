import { createBrowserRouter, Navigate, RouterProvider } from "react-router-dom";

import { RequireAuth } from "./features/auth/RequireAuth";
import { LoginPage } from "./features/auth/pages/LoginPage";
import { RegisterPage } from "./features/auth/pages/RegisterPage";
import { VerifyOtpPage } from "./features/auth/pages/VerifyOtpPage";
import { DatasetDetailPage } from "./features/datasets/pages/DatasetDetailPage";
import { DatasetExplorerPage } from "./features/datasets/pages/DatasetExplorerPage";
import { DatasetNewPage } from "./features/datasets/pages/DatasetNewPage";
import { DatasetsPage } from "./features/datasets/pages/DatasetsPage";
import { AppShell } from "./features/workspaces/components/AppShell";
import { AppPage } from "./features/workspaces/pages/AppPage";
import { OverviewPage } from "./features/workspaces/pages/OverviewPage";

const router = createBrowserRouter([
  { path: "/", element: <Navigate to="/app" replace /> },
  { path: "/login", element: <LoginPage /> },
  { path: "/register", element: <RegisterPage /> },
  { path: "/verify-otp", element: <VerifyOtpPage /> },
  {
    path: "/app",
    element: (
      <RequireAuth>
        <AppShell />
      </RequireAuth>
    ),
    children: [
      { index: true, element: <AppPage /> },
      { path: ":workspaceId/overview", element: <OverviewPage /> },
      { path: ":workspaceId/datasets", element: <DatasetsPage /> },
      { path: ":workspaceId/datasets/new", element: <DatasetNewPage /> },
      { path: ":workspaceId/datasets/:datasetId", element: <DatasetDetailPage /> },
      { path: ":workspaceId/datasets/:datasetId/explorer", element: <DatasetExplorerPage /> },
    ],
  },
  { path: "*", element: <Navigate to="/app" replace /> },
]);

export default function App() {
  return <RouterProvider router={router} />;
}
