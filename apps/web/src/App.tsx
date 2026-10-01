import { lazy, Suspense } from 'react';
import { createBrowserRouter, createRoutesFromElements, Navigate, Outlet, Route, RouterProvider } from 'react-router';
import { AuthProvider } from './context/AuthContext';
import { CatalogProvider } from './context/CatalogContext';
import { ThemeProvider } from './context/ThemeContext';
import { FeatureProvider } from './context/FeatureContext';
import { ProtectedRoute } from './components/ProtectedRoute';
import { AppShell } from './components/AppShell';
import { RouteErrorBoundary } from './components/RouteErrorBoundary';

const PipelineCanvasPage = lazy(() => import('./pages/PipelineCanvasPage'));
const LifecyclePage = lazy(() => import('./pages/LifecyclePage'));
const RunsPage = lazy(() => import('./pages/RunsPage'));
const RunDetailPage = lazy(() => import('./pages/RunDetailPage'));
const LineagePage = lazy(() => import('./pages/LineagePage'));
const MonitoringPage = lazy(() => import('./pages/MonitoringPage'));
const PipelinesPage = lazy(() => import('./pages/PipelinesPage'));
const ProfilePage = lazy(() => import('./pages/ProfilePage'));
const SettingsPage = lazy(() => import('./pages/SettingsPage'));
const LoginPage = lazy(() => import('./pages/auth').then(module => ({ default: module.LoginPage })));
const AcceptInvitePage = lazy(() => import('./pages/auth').then(module => ({ default: module.AcceptInvitePage })));
const TeamPage = lazy(() => import('./pages/TeamPage').then(module => ({ default: module.TeamPage })));
const ConnectorsPage = lazy(() => import('./pages/ConnectorsPage').then(module => ({ default: module.ConnectorsPage })));
const BillingPage = lazy(() => import('./pages/BillingPage').then(module => ({ default: module.BillingPage })));
const AnalyticsPage = lazy(() => import('./pages/AnalyticsPage').then(module => ({ default: module.AnalyticsPage })));

function RouteFallback() {
  return <div role="status" aria-live="polite" className="flex min-h-screen items-center justify-center bg-white text-sm text-gray-500 dark:bg-[#080a10] dark:text-white/50">Loading page…</div>;
}

// Data router (not <BrowserRouter>) so pages can use useBlocker — the canvas
// guards unsaved drafts against in-app navigation and browser back.
const router = createBrowserRouter(createRoutesFromElements(
  <Route element={<AuthProvider><RouteErrorBoundary><Suspense fallback={<RouteFallback />}><Outlet /></Suspense></RouteErrorBoundary></AuthProvider>}>
    <Route path="/login" element={<LoginPage />} />
    <Route path="/accept-invite" element={<AcceptInvitePage />} />

    {/* Canvas page: full-viewport, no AppShell chrome */}
    <Route element={<ProtectedRoute><FeatureProvider><CatalogProvider><Outlet /></CatalogProvider></FeatureProvider></ProtectedRoute>}>
      <Route index element={<PipelineCanvasPage />} />
    </Route>

    <Route element={<ProtectedRoute><FeatureProvider><CatalogProvider><AppShell /></CatalogProvider></FeatureProvider></ProtectedRoute>}>
      <Route path="ai-builder" element={<Navigate to="/?ai=1" replace />} />
      <Route path="pipelines" element={<PipelinesPage />} />
      <Route path="lifecycle" element={<LifecyclePage />} />
      <Route path="runs" element={<RunsPage />} />
      <Route path="runs/:id" element={<RunDetailPage />} />
      <Route path="lineage" element={<LineagePage />} />
      <Route path="monitoring" element={<MonitoringPage />} />
      <Route path="connectors" element={<ConnectorsPage />} />
      <Route path="billing" element={<BillingPage />} />
      <Route path="analytics" element={<AnalyticsPage />} />
      <Route path="team" element={<TeamPage />} />
      <Route path="settings" element={<SettingsPage />} />
      <Route path="profile" element={<ProfilePage />} />
    </Route>
  </Route>,
));

export default function App() {
  return (
    <ThemeProvider>
      <RouterProvider router={router} />
    </ThemeProvider>
  );
}
