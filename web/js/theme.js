// Applies the theme chosen in DocMan's preferences before the page paints.
// The app records the choice in this browser; pages outside the app, such as
// the help, read it here so they match without waiting for a round trip.
try {
  const theme = localStorage.getItem('docman.theme');
  if (theme === 'light' || theme === 'dark') document.documentElement.setAttribute('data-theme', theme);
} catch { /* storage unavailable: keep the default theme */ }
