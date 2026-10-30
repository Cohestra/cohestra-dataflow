import { useEffect, useState } from 'react';
import { ShieldCheck } from 'lucide-react';
import { Navigate, useLocation, useSearchParams } from 'react-router';
import { AtomMark } from '../components/AtomMark';
import { useAuth } from '../context/AuthContext';
import { api } from '../api';

function AuthShell({ title, subtitle, children }: { title: string; subtitle?: string; children: React.ReactNode }) {
  return (
    <div className="relative min-h-screen overflow-hidden px-4"
      style={{ background: 'linear-gradient(145deg, #080a10 0%, #0b0e17 48%, #080a10 100%)' }}>
      <div className="absolute left-[12%] top-[-12%] h-[420px] w-[420px] rounded-full bg-brand-500/15 blur-[110px]" />
      <div className="absolute bottom-[-15%] right-[8%] h-[360px] w-[360px] rounded-full bg-cyan/10 blur-[120px]" />
      <div className="relative z-10 flex min-h-screen items-center justify-center">
        {/* The card is always dark, so it sets its own text colour instead of inheriting the light theme's. */}
        <div className="w-full max-w-md rounded-[24px] border border-white/10 bg-[#10131c]/78 p-8 text-white shadow-glass-glow backdrop-blur-2xl">
          <div className="mb-8 flex items-center gap-3">
            <span className="flex h-11 w-11 items-center justify-center rounded-[14px] border border-white/12 bg-white shadow-lg shadow-black/20">
              <AtomMark size={22} />
            </span>
            <div><p className="text-sm font-semibold">Cohestra DataFlow</p><p className="text-[10px] uppercase tracking-[.16em] text-white/30">Durable data pipelines</p></div>
          </div>
          <h1 className="text-2xl font-semibold tracking-[-0.04em]">{title}</h1>
          {subtitle && <p className="mt-1 text-sm text-white/40">{subtitle}</p>}
          {children}
          <div className="mt-7 flex items-center gap-2 border-t border-white/[0.07] pt-4 text-[10px] text-white/30">
            <ShieldCheck size={13} aria-hidden="true" /> Secure OAuth · tenant-isolated workspace
          </div>
        </div>
      </div>
    </div>
  );
}

function ErrorLine({ msg }: { msg: string | null }) {
  return msg ? <div role="alert" className="text-xs text-rose-300 mt-3">{msg}</div> : null;
}

function GoogleButton({ label }: { label: string }) {
  return (
    <a className="glass-btn-primary mt-6 w-full"
       href="/api/auth/google">
      <svg width="16" height="16" viewBox="0 0 18 18" aria-hidden="true">
        <path fill="#4285F4" d="M17.64 9.2c0-.64-.06-1.25-.16-1.84H9v3.48h4.84a4.14 4.14 0 0 1-1.8 2.72v2.26h2.92c1.7-1.57 2.68-3.88 2.68-6.62z"/>
        <path fill="#34A853" d="M9 18c2.43 0 4.47-.8 5.96-2.18l-2.92-2.26c-.8.54-1.84.86-3.04.86-2.34 0-4.32-1.58-5.03-3.7H.96v2.33A9 9 0 0 0 9 18z"/>
        <path fill="#FBBC05" d="M3.97 10.72A5.4 5.4 0 0 1 3.68 9c0-.6.1-1.18.29-1.72V4.95H.96A9 9 0 0 0 0 9c0 1.45.35 2.82.96 4.05l3.01-2.33z"/>
        <path fill="#EA4335" d="M9 3.58c1.32 0 2.5.45 3.44 1.35l2.58-2.59C13.46.89 11.43 0 9 0A9 9 0 0 0 .96 4.95l3.01 2.33C4.68 5.16 6.66 3.58 9 3.58z"/>
      </svg>
      {label}
    </a>
  );
}

const OAUTH_ERRORS: Record<string, string> = {
  oauth_state: 'Sign-in session expired. Please try again.',
  oauth_token: 'Could not complete sign-in. Please try again.',
  oauth_profile: 'Your account did not return a verified email.',
  oauth_failed: 'Sign-in failed. Please try again.',
  oidc_token: 'Could not complete SSO sign-in. Please try again.',
  oidc_userinfo: 'Could not retrieve your SSO profile. Please try again.',
  oidc_profile: 'Your SSO account did not return a verified email.',
  verification_invalid: 'That confirmation link is invalid or has expired. Sign in with your password to get a new one.',
  oidc_failed: 'SSO sign-in failed. Please try again.',
};

// Show SSO button only when the API is configured with an OIDC provider.
// We detect this by checking if VITE_OIDC_ENABLED=true is set at build time.
const OIDC_ENABLED = import.meta.env.VITE_OIDC_ENABLED === 'true';
const PASSWORD_AUTH_ENABLED = import.meta.env.VITE_PASSWORD_LOGIN_ENABLED === 'true';
const GOOGLE_AUTH_ENABLED = import.meta.env.VITE_GOOGLE_LOGIN_ENABLED === 'true';

// API errors arrive as "<status> <json body>"; show the server's message when there is one.
function apiErrorMessage(ex: any): string {
  const raw: string = ex?.message ?? 'Failed';
  const spaceIdx = raw.indexOf(' ');
  try { const p = JSON.parse(spaceIdx >= 0 ? raw.slice(spaceIdx + 1) : ''); if (p?.error) return p.error; } catch { /* not JSON */ }
  return raw;
}

function ForgotPasswordForm({ initialEmail, onBack }: { initialEmail: string; onBack: () => void }) {
  const [email, setEmail] = useState(initialEmail);
  const [busy, setBusy] = useState(false);
  const [sent, setSent] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault(); setBusy(true); setErr(null);
    try { await api.forgotPassword(email); setSent(true); } catch (ex) { setErr(apiErrorMessage(ex)); }
    setBusy(false);
  };

  if (sent) {
    return (
      <div role="status" className="mt-5 space-y-2 border-t border-white/[0.07] pt-5 text-sm">
        <p className="font-medium">Check your inbox</p>
        <p className="text-xs text-white/60">If {email} has an account, we sent it a link to choose a new password. The link expires in 1 hour.</p>
        <button type="button" className="text-xs text-white/40 hover:text-white/70" onClick={onBack}>Back to sign in</button>
      </div>
    );
  }
  return (
    <form onSubmit={submit} className="mt-5 space-y-3 border-t border-white/[0.07] pt-5">
      <p className="text-xs text-white/40">Enter your email and we'll send you a reset link.</p>
      <input className="glass-input w-full text-sm" type="email" placeholder="you@example.com" aria-label="Email" value={email} onChange={e => setEmail(e.target.value)} required />
      {err && <div role="alert" className="text-xs text-rose-300">{err}</div>}
      <button type="submit" className="glass-btn-primary w-full" disabled={busy}>{busy ? '…' : 'Send reset link'}</button>
      <button type="button" className="text-xs text-white/40 hover:text-white/70 w-full text-center" onClick={onBack}>Back to sign in</button>
    </form>
  );
}

function PasswordForm({ initialEmail = '', inviteToken }: { initialEmail?: string; inviteToken?: string }) {
  const [mode, setMode] = useState<'login' | 'register'>(inviteToken ? 'register' : 'login');
  const [email, setEmail] = useState(initialEmail);
  const [password, setPassword] = useState('');
  const [tenantName, setTenantName] = useState('');
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const [checkInbox, setCheckInbox] = useState(false);
  const [forgot, setForgot] = useState(false);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault(); setBusy(true); setErr(null);
    try {
      if (mode === 'register') {
        const result = await api.registerWithPassword({ email, password, tenantName: tenantName || undefined, inviteToken });
        if (result?.verificationRequired) { setCheckInbox(true); setBusy(false); return; }
      } else {
        await api.loginWithPassword({ email, password });
      }
      window.location.href = '/';
    } catch (ex) {
      setErr(apiErrorMessage(ex));
      setBusy(false);
    }
  };

  if (checkInbox) {
    return (
      <div role="status" className="mt-5 space-y-2 border-t border-white/[0.07] pt-5 text-sm">
        <p className="font-medium">Check your inbox</p>
        <p className="text-xs text-white/60">We sent a confirmation link to {email}. Open it, then sign in.</p>
      </div>
    );
  }

  if (forgot) return <ForgotPasswordForm initialEmail={email} onBack={() => setForgot(false)} />;

  return (
    <form onSubmit={submit} className="mt-5 space-y-3 border-t border-white/[0.07] pt-5">
      <p className="text-xs text-white/40">{inviteToken ? 'Create your account' : 'Or sign in with email'}</p>
      <input className="glass-input w-full text-sm" type="email" placeholder="you@example.com" aria-label="Email" value={email} onChange={e => setEmail(e.target.value)} readOnly={!!inviteToken} required />
      <input className="glass-input w-full text-sm" type="password" placeholder="Password (8+ chars)" aria-label="Password" value={password} onChange={e => setPassword(e.target.value)} required minLength={8} />
      {mode === 'register' && !inviteToken && (
        <input className="glass-input w-full text-sm" placeholder="Workspace name (optional)" aria-label="Workspace name (optional)" value={tenantName} onChange={e => setTenantName(e.target.value)} />
      )}
      {err && <div role="alert" className="text-xs text-rose-300">{err}</div>}
      <button type="submit" className="glass-btn-primary w-full" disabled={busy}>
        {busy ? '…' : mode === 'login' ? 'Sign in' : 'Create account'}
      </button>
      {mode === 'login' && (
        <button type="button" className="text-xs text-white/40 hover:text-white/70 w-full text-center" onClick={() => { setForgot(true); setErr(null); }}>
          Forgot password?
        </button>
      )}
      {!inviteToken && (
        <button type="button" className="text-xs text-white/40 hover:text-white/70 w-full text-center" onClick={() => { setMode(m => m === 'login' ? 'register' : 'login'); setErr(null); }}>
          {mode === 'login' ? 'Need an account? Register' : 'Already have an account? Sign in'}
        </button>
      )}
    </form>
  );
}

export function LoginPage() {
  const { user } = useAuth();
  const location = useLocation() as any;
  const [params] = useSearchParams();
  const errorCode = params.get('error');

  if (user) return <Navigate to={location.state?.from ?? '/'} replace />;

  return (
    <AuthShell title="Sign in" subtitle="Welcome to DataFlow.">
      {GOOGLE_AUTH_ENABLED && <GoogleButton label="Continue with Google" />}
      {OIDC_ENABLED && (
        <a className="glass-btn-ghost mt-2 w-full" href="/api/auth/oidc">
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" aria-hidden="true">
            <rect x="3" y="11" width="18" height="11" rx="2" /><path d="M7 11V7a5 5 0 0 1 10 0v4"/>
          </svg>
          Continue with SSO
        </a>
      )}
      {params.get('verified') === '1' && (
        <p role="status" className="mt-4 text-xs text-emerald-300">Email confirmed. Sign in to continue.</p>
      )}
      {params.get('reset') === '1' && (
        <p role="status" className="mt-4 text-xs text-emerald-300">Password changed. Sign in with your new password.</p>
      )}
      {PASSWORD_AUTH_ENABLED && <PasswordForm />}
      <ErrorLine msg={errorCode ? (OAUTH_ERRORS[errorCode] ?? 'Sign-in failed.') : null} />
      {!PASSWORD_AUTH_ENABLED && (
        <p className="text-xs mt-5 opacity-60">
          Need access? Contact your workspace administrator.
        </p>
      )}
    </AuthShell>
  );
}

// ─── ResetPasswordPage ────────────────────────────────────────────────────────

export function ResetPasswordPage() {
  const [params] = useSearchParams();
  const token = params.get('token') ?? '';
  const [password, setPassword] = useState('');
  const [confirm, setConfirm] = useState('');
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault(); setErr(null);
    if (password !== confirm) { setErr('The passwords do not match.'); return; }
    setBusy(true);
    try {
      await api.resetPassword({ token, password });
      window.location.href = '/login?reset=1';
    } catch (ex) {
      setErr(apiErrorMessage(ex));
      setBusy(false);
    }
  };

  if (!token) return <AuthShell title="Reset link problem"><p className="mt-4 text-sm text-rose-300">This reset link is incomplete. Ask for a new one from the sign-in page.</p></AuthShell>;
  return (
    <AuthShell title="Choose a new password" subtitle="This signs you out on every device.">
      <form onSubmit={submit} className="mt-5 space-y-3">
        <input className="glass-input w-full text-sm" type="password" placeholder="New password (8+ chars)" aria-label="New password" autoComplete="new-password" value={password} onChange={e => setPassword(e.target.value)} required minLength={8} />
        <input className="glass-input w-full text-sm" type="password" placeholder="Repeat new password" aria-label="Repeat new password" autoComplete="new-password" value={confirm} onChange={e => setConfirm(e.target.value)} required minLength={8} />
        {err && <div role="alert" className="text-xs text-rose-300">{err}</div>}
        <button type="submit" className="glass-btn-primary w-full" disabled={busy}>{busy ? '…' : 'Set new password'}</button>
        <a href="/login" className="block text-center text-xs text-white/40 hover:text-white/70">Back to sign in</a>
      </form>
    </AuthShell>
  );
}

// ─── AcceptInvitePage ─────────────────────────────────────────────────────────

export function AcceptInvitePage() {
  const [params] = useSearchParams();
  const token = params.get('token');
  const [info, setInfo] = useState<{ email: string; role: string; tenantName: string } | null>(null);
  const [err, setErr] = useState<string | null>(null);

  useEffect(() => {
    if (!token) { setErr('Missing token.'); return; }
    api.inviteInfo(token).then(setInfo).catch(e => setErr(e.message || 'Invalid invite.'));
  }, [token]);

  if (err && !info) return (
    <AuthShell title="Invite problem"><p className="text-sm text-rose-300">{err}</p></AuthShell>
  );
  if (!info) return <AuthShell title="Loading invite…"><p className="text-sm opacity-70">One moment…</p></AuthShell>;

  return (
    <AuthShell title={`Join ${info.tenantName}`}
      subtitle={`You've been invited as ${info.role}.`}>
      {GOOGLE_AUTH_ENABLED && <GoogleButton label="Continue with Google" />}
      {PASSWORD_AUTH_ENABLED && <PasswordForm initialEmail={info.email} inviteToken={token!} />}
      {!GOOGLE_AUTH_ENABLED && !PASSWORD_AUTH_ENABLED && <p className="mt-5 text-sm text-white/50">No sign-in method is configured. Contact your workspace administrator.</p>}
    </AuthShell>
  );
}
