// Arrow keys move between the buttons of a role="radiogroup" (the theme
// picker, "Color by"), choosing as they go, as a radio group should. Each
// button's own click handler does the choosing.
(() => {
  "use strict";

  const NEXT = { ArrowRight: 1, ArrowDown: 1, ArrowLeft: -1, ArrowUp: -1 };

  document.addEventListener("keydown", (e) => {
    const step = NEXT[e.key];
    const current = e.target.closest?.('[role="radiogroup"] [role="radio"]');
    if (!step || !current) {
      return;
    }
    const radios = [...current.closest('[role="radiogroup"]').querySelectorAll('[role="radio"]')];
    const next = radios[(radios.indexOf(current) + step + radios.length) % radios.length];
    e.preventDefault();
    next.focus();
    next.click();
  });
})();
