// The theme picker in the header. Auto follows the system's light or dark
// setting; Light and Dark override it, for this browser only. The inline
// script in layout.html applies a saved choice before the page draws.
(() => {
  "use strict";

  const KEY = "racket.theme";
  const picker = document.querySelector("[data-theme-toggle]");
  if (!picker) {
    return;
  }

  function read() {
    try {
      const theme = localStorage.getItem(KEY);
      return theme === "light" || theme === "dark" ? theme : "auto";
    } catch {
      return "auto";
    }
  }

  function apply(theme) {
    if (theme === "auto") {
      delete document.documentElement.dataset.theme;
    } else {
      document.documentElement.dataset.theme = theme;
    }
    for (const button of picker.querySelectorAll("[data-theme-choice]")) {
      button.setAttribute("aria-checked", String(button.dataset.themeChoice === theme));
    }
  }

  picker.addEventListener("click", (e) => {
    const button = e.target.closest("[data-theme-choice]");
    if (!button) {
      return;
    }
    const theme = button.dataset.themeChoice;
    try {
      if (theme === "auto") {
        localStorage.removeItem(KEY);
      } else {
        localStorage.setItem(KEY, theme);
      }
    } catch {
      // Private windows may refuse storage; the choice still holds for this page.
    }
    apply(theme);
  });

  apply(read());
  picker.hidden = false;
})();
