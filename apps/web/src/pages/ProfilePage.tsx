import { LogOut, User } from 'lucide-react';
import { useNavigate } from 'react-router';
import { useAuth } from '../context/AuthContext';

export default function ProfilePage() {
  const { user, logout } = useAuth();
  const navigate = useNavigate();

  const initials = user?.email?.slice(0, 2).toUpperCase() ?? 'DF';

  const handleLogout = async () => {
    await logout();
    navigate('/login', { replace: true });
  };

  const rows: { label: string; value: string }[] = [
    { label: 'Email',     value: user?.email ?? '—' },
    { label: 'Role',      value: user?.role ? user.role.charAt(0).toUpperCase() + user.role.slice(1) : '—' },
    { label: 'Workspace', value: user?.tenant_name ?? user?.tenant_id ?? '—' },
    { label: 'Verified',  value: (user?.email_verified || user?.emailVerified) ? 'Yes' : 'Pending' },
  ];

  return (
    <div className="mx-auto max-w-lg space-y-6 px-6 py-10">
      <div>
        <h2 className="page-heading flex items-center gap-2"><User size={20} /> Profile</h2>
        <p className="page-subtitle mt-1">Your account and workspace details.</p>
      </div>

      <div className="glass-card divide-y divide-gray-100 dark:divide-white/6 overflow-hidden">
        {/* Avatar row */}
        <div className="flex items-center gap-4 p-5">
          <div className="relative flex h-14 w-14 shrink-0 items-center justify-center rounded-full
            bg-linear-to-br from-brand-400 to-brand-600 text-[18px] font-bold text-white
            shadow-lg shadow-brand-500/20">
            {initials}
            <span className="absolute bottom-0 right-0 h-3 w-3 rounded-full border-2 border-white dark:border-[#0a0c12] bg-emerald-400" />
          </div>
          <div className="min-w-0">
            <p className="truncate font-semibold text-gray-900 dark:text-white/90">{user?.email}</p>
            <p className="text-xs text-gray-400 dark:text-white/40 mt-0.5">
              <span className="capitalize">{user?.role}</span>
              {user?.tenant_name && <> · {user.tenant_name}</>}
            </p>
          </div>
        </div>

        {/* Details */}
        {rows.map(({ label, value }) => (
          <div key={label} className="flex items-center justify-between px-5 py-3 text-[13px]">
            <span className="text-gray-500 dark:text-white/45">{label}</span>
            <span className="font-medium text-gray-900 dark:text-white/80">{value}</span>
          </div>
        ))}

        {/* Sign out */}
        <div className="p-5">
          <button onClick={handleLogout}
            className="glass-btn-danger flex w-full items-center justify-center gap-2 text-sm">
            <LogOut size={14} /> Sign out
          </button>
        </div>
      </div>
    </div>
  );
}
