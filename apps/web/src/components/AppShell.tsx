import {
  BarChart3, Cable, CreditCard, History, LogOut, Menu, Moon, Network, Orbit, Settings, Sun, Users, Workflow, Gauge, X,
} from 'lucide-react';
import { AtomMark } from './AtomMark';
import { NavLink, Outlet, useLocation, useNavigate } from 'react-router';
import { Suspense, useState } from 'react';
import { useAuth } from '../context/AuthContext';
import { useTheme } from '../context/ThemeContext';
import { RouteErrorBoundary } from './RouteErrorBoundary';

const NAV = [
  { to: '/pipelines',  label: 'Pipelines',  icon: Workflow,  end: false },
  { to: '/lifecycle',  label: 'Lifecycle',  icon: Orbit },
  { to: '/runs',       label: 'Runs',       icon: History },
  { to: '/monitoring', label: 'Monitoring', icon: Gauge },
  { to: '/lineage',    label: 'Lineage',    icon: Network },
  { to: '/connectors', label: 'Connectors', icon: Cable },
  { to: '/analytics',  label: 'Analytics',  icon: BarChart3 },
  { to: '/team',       label: 'Team',       icon: Users },
  { to: '/billing',    label: 'Billing',    icon: CreditCard },
];

const PAGE_META: Record<string, { title: string; eyebrow: string }> = {
  '/pipelines':  { title: 'Pipelines',   eyebrow: 'Build & orchestrate' },
  '/lifecycle':  { title: 'Lifecycle',   eyebrow: 'Promote & gate' },
  '/runs':       { title: 'Runs',        eyebrow: 'History & observability' },
  '/monitoring': { title: 'Monitoring',  eyebrow: 'Reliability & health' },
  '/lineage':    { title: 'Lineage',     eyebrow: 'Workspace architecture' },
  '/connectors': { title: 'Connect accounts', eyebrow: 'Data sources' },
  '/analytics':  { title: 'Analytics',   eyebrow: 'Explore outcomes' },
  '/team':       { title: 'Team',        eyebrow: 'Workspace access' },
  '/billing':    { title: 'Billing',     eyebrow: 'Usage & plan' },
  '/settings':   { title: 'Settings',    eyebrow: 'Preferences' },
  '/profile':    { title: 'Profile',     eyebrow: 'Your account' },
};

export function AppShell() {
  const { user, logout } = useAuth();
  const { dark, toggle } = useTheme();
  const navigate = useNavigate();
  const doLogout = async () => { await logout(); navigate('/login', { replace: true }); };
  const { pathname } = useLocation();
  const [sidebarOpen, setSidebarOpen] = useState(false);
  const [bannerDismissed, setBannerDismissed] = useState(false); // ponytail: session-only dismiss, reappears on reload by design
  const meta = PAGE_META[pathname] ?? PAGE_META[Object.keys(PAGE_META).find(k => pathname.startsWith(k + '/')) ?? ''] ?? PAGE_META['/pipelines'];
  const initials = user?.email?.slice(0, 2).toUpperCase() ?? 'DF';

  return (
    <div className="canvas-root flex h-screen overflow-hidden">
      {/* ── mobile nav overlay ── */}
      {sidebarOpen && (
        <div className="fixed inset-0 z-50 sm:hidden">
          <div className="absolute inset-0 bg-black/40" onClick={() => setSidebarOpen(false)} />
          <aside className="absolute left-0 top-0 bottom-0 flex w-[200px] flex-col items-center gap-1 py-3
            border-r border-gray-200 dark:border-white/8
            bg-white/98 dark:bg-[#0d0f17]/98 backdrop-blur-lg">
            <button aria-label="Close menu" onClick={() => setSidebarOpen(false)}
              className="relative self-end mr-2 mb-2 flex h-8 w-8 items-center justify-center rounded-[10px] text-gray-400 hover:bg-gray-100 dark:hover:bg-white/8 before:absolute before:inset-[-6px] before:content-['']">
              <X size={15} />
            </button>
            {NAV.map(({ to, label, icon: Icon, end }) => (
              <NavLink key={to} to={to} end={end} onClick={() => setSidebarOpen(false)}
                className={({ isActive }) => `flex w-[180px] items-center gap-3 px-3 h-11 rounded-[10px] border transition-all ${
                  isActive ? 'border-brand-300/20 bg-brand-500/15 text-brand-500 dark:text-brand-300' : 'border-transparent text-gray-600 dark:text-white/50 hover:bg-gray-100 dark:hover:bg-white/5.5'
                }`}>
                <Icon size={17} strokeWidth={1.75} />
                <span className="text-sm font-medium">{label}</span>
              </NavLink>
            ))}
            <div className="my-1 h-px w-[180px] bg-gray-200 dark:bg-white/8" />
            <button onClick={doLogout}
              className="flex w-[180px] items-center gap-3 px-3 h-11 rounded-[10px] border border-transparent text-gray-600 dark:text-white/50 hover:bg-gray-100 dark:hover:bg-white/5.5 hover:text-red-500 dark:hover:text-red-400 transition-all">
              <LogOut size={17} strokeWidth={1.75} />
              <span className="text-sm font-medium">Sign out</span>
            </button>
          </aside>
        </div>
      )}

      {/* ── sidebar: 52px compact glass, matches canvas ── */}
      <aside className="relative z-40 hidden sm:flex w-[52px] flex-none flex-col items-center gap-1 overflow-visible py-3
        border-r border-gray-200 dark:border-white/8
        bg-white/95 dark:bg-[#0d0f17]/95 backdrop-blur-lg shrink-0">

        {/* brand mark */}
        <NavLink to="/pipelines" aria-label="DataFlow home" className="mb-2 flex h-9 w-9 items-center justify-center rounded-[10px]
          border border-gray-200 bg-white shadow-md dark:border-white/12">
          <AtomMark size={22} />
        </NavLink>
        <div className="my-1 h-px w-8 bg-gray-200 dark:bg-white/8" />

        {/* nav */}
        <nav className="flex flex-col gap-1">
          {NAV.map(({ to, label, icon: Icon, end }) => (
            <NavLink key={to} to={to} end={end} aria-label={label} title={label}
              className={({ isActive }) =>
                `group relative flex h-9 w-9 items-center justify-center rounded-[10px] border transition-all ${
                  isActive
                    ? 'border-brand-300/20 bg-brand-500/15 text-brand-500 dark:text-brand-300 shadow-[0_0_24px_rgba(124,108,242,.12)]'
                    : 'border-transparent text-gray-400 dark:text-white/40 hover:border-gray-200 dark:hover:border-white/8 hover:bg-gray-100 dark:hover:bg-white/5.5 hover:text-gray-700 dark:hover:text-white/85'
                }`
              }>
              <Icon size={17} strokeWidth={1.75} />
              <span className="pointer-events-none absolute left-[46px] z-50 whitespace-nowrap rounded-lg border border-gray-200 dark:border-white/10 bg-gray-900/95 px-2 py-1 text-[11px] text-white/90 opacity-0 shadow-xl backdrop-blur-xl transition group-hover:opacity-100">
                {label}
              </span>
            </NavLink>
          ))}
        </nav>

        <div className="flex-1" />
        <div className="my-1 h-px w-8 bg-gray-200 dark:bg-white/8" />

        {/* bottom */}
        <button onClick={toggle} title={dark ? 'Light mode' : 'Dark mode'}
          className="flex h-9 w-9 items-center justify-center rounded-[10px] border border-gray-200 dark:border-white/8
            bg-gray-50 dark:bg-white/4 text-gray-400 dark:text-white/40
            hover:bg-gray-100 dark:hover:bg-white/8 hover:text-gray-700 dark:hover:text-white transition-all">
          {dark ? <Sun size={15} /> : <Moon size={15} />}
        </button>
        <NavLink to="/settings" title="Settings"
          className={({ isActive }) =>
            `group relative flex h-9 w-9 items-center justify-center rounded-[10px] border transition-all ${
              isActive
                ? 'border-brand-300/20 bg-brand-500/15 text-brand-500 dark:text-brand-300'
                : 'border-transparent text-gray-400 dark:text-white/30 hover:bg-gray-100 dark:hover:bg-white/8 hover:text-gray-700 dark:hover:text-white'
            }`
          }>
          <Settings size={15} />
          <span className="pointer-events-none absolute left-[46px] z-50 whitespace-nowrap rounded-lg border border-gray-200 dark:border-white/10 bg-gray-900/95 px-2 py-1 text-[11px] text-white/90 opacity-0 shadow-xl backdrop-blur-xl transition group-hover:opacity-100">
            Settings
          </span>
        </NavLink>
        <NavLink to="/profile" title={user?.email ?? 'Profile'}
          className={({ isActive }) =>
            `group relative flex h-9 w-9 items-center justify-center rounded-full border text-[11px] font-semibold transition-all ${
              isActive
                ? 'border-brand-400/40 bg-brand-500/15 text-brand-500 dark:text-brand-300'
                : 'border-gray-200 dark:border-white/10 bg-gray-50 dark:bg-white/6 text-gray-600 dark:text-white/70 hover:border-gray-300 dark:hover:border-white/20 hover:text-gray-900 dark:hover:text-white'
            }`
          }>
          {initials}
          <span className="absolute bottom-0 right-0 h-2.5 w-2.5 rounded-full border-2 border-white dark:border-[#0a0c12] bg-emerald-400" />
          <span className="pointer-events-none absolute left-[46px] z-50 whitespace-nowrap rounded-lg border border-gray-200 dark:border-white/10 bg-gray-900/95 px-2 py-1 text-[11px] text-white/90 opacity-0 shadow-xl backdrop-blur-xl transition group-hover:opacity-100">
            Profile
          </span>
        </NavLink>
        <button onClick={async () => { await logout(); navigate('/login', { replace: true }); }}
          title="Sign out"
          className="flex h-9 w-9 items-center justify-center rounded-[10px] border-transparent
            text-gray-400 dark:text-white/30 hover:text-red-500 dark:hover:text-red-400 transition-all">
          <LogOut size={15} />
        </button>
      </aside>

      {/* ── content ── */}
      <div className="flex flex-1 flex-col min-w-0 min-h-0">
        {!bannerDismissed && (
          <div role="status" className="flex items-center justify-center gap-2 px-4 py-1.5 shrink-0
            border-b border-amber-300 dark:border-amber-400/25
            bg-amber-100 dark:bg-amber-500/15 text-[11px] font-medium text-amber-900 dark:text-amber-200">
            <span>Pre-release demo — single-zone deployment, not highly available. Data may be reset.</span>
            <button onClick={() => setBannerDismissed(true)} aria-label="Dismiss demo notice"
              className="flex h-5 w-5 items-center justify-center rounded-sm hover:bg-amber-200 dark:hover:bg-amber-400/20">
              <X size={12} />
            </button>
          </div>
        )}
        <header className="flex items-center px-4 sm:px-6 h-14 border-b border-gray-200 dark:border-white/6
          bg-white/90 dark:bg-black/20 backdrop-blur-lg shrink-0">
          <button className="flex sm:hidden h-8 w-8 items-center justify-center rounded-[10px] text-gray-400 hover:bg-gray-100 dark:hover:bg-white/8 mr-2" onClick={() => setSidebarOpen(true)} aria-label="Menu">
            <Menu size={18} />
          </button>
          <div>
            <p className="text-[9px] font-semibold uppercase tracking-[.16em] text-gray-400 dark:text-white/30">{meta.eyebrow}</p>
            <h1 className="mt-0.5 text-[15px] font-bold tracking-[-0.02em] text-gray-900 dark:text-white/90">{meta.title}</h1>
          </div>
          <div className="flex-1" />
          <div className="hidden items-center gap-2.5 text-[11px] text-gray-400 dark:text-white/40 sm:flex">
            <span>{user?.email}</span>
            <span className="glass-badge">{user?.role}</span>
          </div>
          <button onClick={toggle} className="ml-2 icon-button sm:hidden" title={dark ? 'Light mode' : 'Dark mode'}>
            {dark ? <Sun size={15} /> : <Moon size={15} />}
          </button>
        </header>
        <main className="flex-1 overflow-y-auto">
          <RouteErrorBoundary key={pathname}>
            <Suspense fallback={<div role="status" aria-live="polite" className="flex h-32 items-center justify-center text-xs text-gray-400 dark:text-white/40">Loading page…</div>}>
              <Outlet />
            </Suspense>
          </RouteErrorBoundary>
        </main>
      </div>
    </div>
  );
}
