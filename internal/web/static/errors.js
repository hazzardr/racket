// htmx doesn't swap in error responses, so a failed action would otherwise
// do nothing visible. This shows the error page's message in a toast
// instead, or a generic one when the server couldn't be reached.
(() => {
  "use strict";

  const GENERIC = "Something went wrong. Please try again.";
  let toast = null;
  let timer = 0;

  function show(message) {
    if (!toast || !toast.isConnected) {
      toast = document.createElement("div");
      toast.className = "toast";
      toast.setAttribute("role", "alert");
      toast.addEventListener("click", () => (toast.hidden = true));
      document.body.append(toast);
    }
    toast.textContent = message;
    toast.hidden = false;
    clearTimeout(timer);
    timer = setTimeout(() => (toast.hidden = true), 6000);
  }

  // The error page puts its message in .status.error.
  function messageOf(xhr) {
    try {
      const doc = new DOMParser().parseFromString(xhr.responseText || "", "text/html");
      return doc.querySelector(".status.error")?.textContent.trim() || GENERIC;
    } catch {
      return GENERIC;
    }
  }

  document.addEventListener("htmx:responseError", (e) => show(messageOf(e.detail.xhr)));
  document.addEventListener("htmx:sendError", () => show("Couldn't reach Racket. Check your connection and try again."));
})();
