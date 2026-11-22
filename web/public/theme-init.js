try {
  var theme = localStorage.getItem("agent-stats-theme");
  if (theme === "light" || theme === "dark") document.documentElement.dataset.theme = theme;
} catch (e) {
  /* storage blocked: the system theme applies */
}
