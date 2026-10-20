// The new machine page's preview: shows the name, kind, and status as the
// form is filled in. Without it, the preview shows what the server rendered.
(() => {
  "use strict";

  const root = document.querySelector("[data-new-machine]");
  const form = root?.querySelector("form");
  if (!form) {
    return;
  }
  const $ = (sel) => root.querySelector(sel);

  function update() {
    const data = new FormData(form);
    const kind = String(data.get("kind") || "");
    $("[data-preview-name]").textContent = String(data.get("name") || "").trim() || "New machine";
    $("[data-preview-kind]").textContent = kind;
    $("[data-preview-status]").textContent = String(data.get("status") || "");
    for (const icon of root.querySelectorAll("[data-kind-icon]")) {
      icon.hidden = icon.dataset.kindIcon !== kind;
    }
  }

  form.addEventListener("input", update);
  form.addEventListener("change", update);
  // The browser may restore field values on reload or Back.
  update();
})();
