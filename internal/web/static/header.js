// The user menu in the header is a <details>; this closes it when you
// click outside it or press Escape, as a menu should.
(() => {
  "use strict";

  const menu = document.querySelector("[data-user-menu]");
  if (!menu) {
    return;
  }

  document.addEventListener("click", (e) => {
    if (menu.open && !menu.contains(e.target)) {
      menu.open = false;
    }
  });

  document.addEventListener("keydown", (e) => {
    if (e.key === "Escape" && menu.open) {
      menu.open = false;
      menu.querySelector("summary").focus();
    }
  });
})();
