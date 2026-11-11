// Blocking, same-origin bootstrap: resolve the preference before the first paint.
(function () {
  var theme = 'dark';
  try {
    if (localStorage.getItem('qingye-theme') === 'light') theme = 'light';
  } catch (_) {}
  document.documentElement.classList.toggle('dark', theme === 'dark');
  document.documentElement.dataset.theme = theme;
  document.documentElement.style.colorScheme = theme;
  var meta = document.querySelector('meta[name="theme-color"]');
  if (meta) meta.setAttribute('content', theme === 'dark' ? '#09090B' : '#F6F6F4');
})();
