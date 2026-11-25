// Apply the saved scheme before the stylesheet and the React entry point load.
(() => {
  let theme = "dark";
  try {
    if (localStorage.getItem("qingye-admin-theme") === "light") theme = "light";
  } catch {
    // Browsers with disabled storage keep the default on first paint.
  }
  const root = document.documentElement;
  root.dataset.theme = theme;
  root.classList.toggle("dark", theme === "dark");
  document.querySelector('meta[name="color-scheme"]').content = theme;
  const color = document.querySelector('meta[name="theme-color"]');
  color.content = color.dataset[theme];
})();
