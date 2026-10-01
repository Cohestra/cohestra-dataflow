import { Monitor, Moon, Settings, Sun } from 'lucide-react';
import { useTheme, type ThemeMode } from '../context/ThemeContext';

const tz = Intl.DateTimeFormat().resolvedOptions().timeZone;

function Row({ label, sublabel, children }: { label: string; sublabel?: string; children: React.ReactNode }) {
  return (
    <div className="flex items-center justify-between gap-4 px-5 py-4">
      <div className="min-w-0">
        <p className="text-[13px] font-medium text-gray-900 dark:text-white/90">{label}</p>
        {sublabel && <p className="text-[11px] text-gray-400 dark:text-white/40 mt-0.5">{sublabel}</p>}
      </div>
      <div className="flex-none">{children}</div>
    </div>
  );
}

export default function SettingsPage() {
  const { mode, setMode } = useTheme();

  const themeOptions: { value: ThemeMode; label: string; icon: React.ReactNode }[] = [
    { value: 'light',  label: 'Light',  icon: <Sun size={12} /> },
    { value: 'dark',   label: 'Dark',   icon: <Moon size={12} /> },
    { value: 'system', label: 'System', icon: <Monitor size={12} /> },
  ];

  return (
    <div className="mx-auto max-w-lg space-y-8 px-6 py-10">
      <div>
        <h2 className="page-heading flex items-center gap-2"><Settings size={20} /> Settings</h2>
        <p className="page-subtitle mt-1">Workspace and display preferences. Saved locally in this browser.</p>
      </div>

      {/* Appearance */}
      <section>
        <p className="mb-2 text-[10px] font-semibold uppercase tracking-[.12em] text-gray-400 dark:text-white/35 px-1">Appearance</p>
        <div className="glass-card divide-y divide-gray-100 dark:divide-white/[0.06] overflow-hidden">
          <Row label="Theme" sublabel="Controls light/dark mode across the UI">
            <div className="flex rounded-xl overflow-hidden border border-gray-200 dark:border-white/[0.09]">
              {themeOptions.map(({ value, label, icon }) => (
                <button key={value} onClick={() => setMode(value)}
                  className={`flex items-center gap-1.5 px-3 py-1.5 text-[12px] font-medium transition-colors ${
                    mode === value
                      ? 'bg-gray-900 text-white dark:bg-white/[0.15] dark:text-white'
                      : 'bg-white text-gray-500 hover:bg-gray-50 dark:bg-transparent dark:text-white/45 dark:hover:bg-white/[0.05] dark:hover:text-white/75'
                  }`}>
                  {icon}{label}
                </button>
              ))}
            </div>
          </Row>
        </div>
      </section>

      {/* Date & Time */}
      <section>
        <p className="mb-2 text-[10px] font-semibold uppercase tracking-[.12em] text-gray-400 dark:text-white/35 px-1">Date & Time</p>
        <div className="glass-card divide-y divide-gray-100 dark:divide-white/[0.06] overflow-hidden">
          <Row
            label="Timezone"
            sublabel="All dates and times use your browser's local timezone">
            <span className="rounded-lg bg-gray-100 dark:bg-white/[0.07] px-2.5 py-1 text-[12px] font-medium text-gray-700 dark:text-white/70">
              {tz}
            </span>
          </Row>
        </div>
      </section>

      {/* About */}
      <section>
        <p className="mb-2 text-[10px] font-semibold uppercase tracking-[.12em] text-gray-400 dark:text-white/35 px-1">About</p>
        <div className="glass-card divide-y divide-gray-100 dark:divide-white/[0.06] overflow-hidden">
          {[
            { label: 'Version', value: 'v0.1.0-dev' },
            { label: 'License', value: 'AGPL-3.0' },
            { label: 'Runtime', value: 'Go + Temporal' },
          ].map(({ label, value }) => (
            <div key={label} className="flex items-center justify-between px-5 py-3 text-[13px]">
              <span className="text-gray-500 dark:text-white/45">{label}</span>
              <span className="font-medium text-gray-700 dark:text-white/70">{value}</span>
            </div>
          ))}
        </div>
      </section>
    </div>
  );
}
