import { useCallback, useState } from "react";

export type ThemeChoice = "system" | "light" | "dark";

const KEY = "agent-stats-theme";

function stored(): ThemeChoice {
  try {
    const value = localStorage.getItem(KEY);
    if (value === "light" || value === "dark") return value;
  } catch {
    /* storage blocked: follow the system */
  }
  return "system";
}

/** The saved theme choice and a setter that applies and saves it. "system" leaves the choice to prefers-color-scheme. */
export function useTheme(): [ThemeChoice, (choice: ThemeChoice) => void] {
  const [choice, setChoice] = useState<ThemeChoice>(stored);
  const update = useCallback((next: ThemeChoice) => {
    setChoice(next);
    if (next === "system") delete document.documentElement.dataset.theme;
    else document.documentElement.dataset.theme = next;
    try {
      if (next === "system") localStorage.removeItem(KEY);
      else localStorage.setItem(KEY, next);
    } catch {
      /* the choice holds for this visit only */
    }
  }, []);
  return [choice, update];
}
