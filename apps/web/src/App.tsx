import { createBrowserRouter, Navigate, RouterProvider } from "react-router-dom";

import { RequireAuth } from "./features/auth/RequireAuth";
import { LoginPage } from "./features/auth/pages/LoginPage";
import { RegisterPage } from "./features/auth/pages/RegisterPage";
import { VerifyOtpPage } from "./features/auth/pages/VerifyOtpPage";
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
    ],
  },
  { path: "*", element: <Navigate to="/app" replace /> },
]);

export default function App() {
  return <RouterProvider router={router} />;
}
