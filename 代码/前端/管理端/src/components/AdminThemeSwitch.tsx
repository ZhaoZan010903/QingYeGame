import { useEffect, useState } from "react";
import { MoonStar, Sun } from "lucide-react";

type Theme = "dark" | "light";
const key = "qingye-admin-theme";

export function AdminThemeSwitch() {
  const [theme, setTheme] = useState<Theme>(() =>
    document.documentElement.dataset.theme === "light" ? "light" : "dark",
  );

  function applyTheme(next: Theme) {
    const root = document.documentElement;
    root.dataset.theme = next;
    root.classList.toggle("dark", next === "dark");
    document
      .querySelector('meta[name="color-scheme"]')
      ?.setAttribute("content", next);
    const color = document.querySelector<HTMLMetaElement>(
      'meta[name="theme-color"]',
    );
    if (color?.dataset[next]) color.content = color.dataset[next];
    setTheme(next);
  }

  useEffect(() => {
    const syncPreference = (event?: StorageEvent) => {
      if (event && event.key !== key && event.key !== null) return;
      try {
        if (event && event.storageArea !== localStorage) return;
        applyTheme(localStorage.getItem(key) === "light" ? "light" : "dark");
      } catch {
        // An unavailable store still permits switching in the current tab.
      }
    };
    window.addEventListener("storage", syncPreference);
    syncPreference();
    return () => window.removeEventListener("storage", syncPreference);
  }, []);

  const light = theme === "light";
  function toggle() {
    const next = light ? "dark" : "light";
    applyTheme(next);
    try {
      localStorage.setItem(key, next);
    } catch {
      // The displayed preference stays in effect until this tab is closed.
    }
  }

  return (
    <button
      type="button"
      role="switch"
      aria-checked={light}
      aria-label="浅色主题"
      title={light ? "切换至深色主题" : "切换至浅色主题"}
      className="admin-theme-switch"
      onClick={toggle}
    >
      <span className="admin-theme-track" aria-hidden="true">
        <MoonStar size={15} />
        <Sun size={15} />
      </span>
    </button>
  );
}
