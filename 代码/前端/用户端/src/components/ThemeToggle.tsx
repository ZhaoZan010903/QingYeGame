import { useEffect, useState } from 'react';
import { Sun, MoonStar } from 'lucide-react';
export type Theme = 'dark' | 'light';
export const resolveTheme = (value: string | null): Theme => (value === 'light' ? 'light' : 'dark');
export function ThemeToggle() {
  const [theme, setTheme] = useState<Theme>(() =>
    document.documentElement.classList.contains('dark') ? 'dark' : 'light',
  );
  useEffect(() => {
    const sync = (event: StorageEvent) => {
      if (event.key === 'qingye-theme' || event.key === null)
        setTheme(resolveTheme(event.newValue));
    };
    window.addEventListener('storage', sync);
    return () => window.removeEventListener('storage', sync);
  }, []);
  useEffect(() => {
    const root = document.documentElement;
    root.classList.add('theme-changing');
    root.classList.toggle('dark', theme === 'dark');
    root.dataset.theme = theme;
    root.style.colorScheme = theme;
    document
      .querySelector('meta[name="theme-color"]')
      ?.setAttribute('content', theme === 'dark' ? '#09090B' : '#F6F6F4');
    try {
      localStorage.setItem('qingye-theme', theme);
    } catch {
      /* Storage may be disabled; the current page still switches. */
    }
    const timer = window.setTimeout(() => root.classList.remove('theme-changing'), 250);
    return () => window.clearTimeout(timer);
  }, [theme]);
  return (
    <button
      type="button"
      className="theme-toggle dark:shadow-none"
      role="switch"
      aria-checked={theme === 'dark'}
      aria-label="深色模式"
      title={theme === 'dark' ? '切换至画廊浅色' : '切换至黑曜石深色'}
      onClick={() => setTheme(theme === 'dark' ? 'light' : 'dark')}
    >
      <span className="theme-toggle-thumb" aria-hidden="true" />
      <Sun className="theme-sun" aria-hidden="true" />
      <MoonStar className="theme-moon" aria-hidden="true" />
    </button>
  );
}
