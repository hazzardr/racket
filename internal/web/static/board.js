// The machine board (partials/machine_board.html): draws a wire from each
// slot's bay in the chassis to its part card, opens a slot's panel, and puts
// spare parts in slots by drag and drop or by clicking the part, then a
// slot. The server renders everything else. Slot actions swap the whole
// board, and this redraws after each swap.
//
// A board marked data-demo (the landing page's, see demo.js) saves nothing:
// placing a spare or clicking a slot fires a board:place or board:slot event
// on it instead, and its page re-renders it and calls RacketBoard.mount.
(() => {
  "use strict";

  const reduce = matchMedia("(prefers-reduced-motion: reduce)").matches;
  const narrowMQ = matchMedia("(max-width: 900px)");
  const COLOR_KEY = "racket.board.color";
  const RAIL_STEP = 7;
  const SVG_NS = "http://www.w3.org/2000/svg";

  const $ = (sel, from = document) => from.querySelector(sel);
  const $$ = (sel, from = document) => [...from.querySelectorAll(sel)];

  // These survive swaps: the open panel stays open across a slot action.
  let root = null;
  let openId = null;
  let focusId = null;
  let placing = null;
  let observer = null;
  let lastSize = "";
  let firstMount = true;

  const cardEl = (id) => $(`.slot-card[data-slot="${id}"]`, root);
  const bayEl = (id) => $(`.bay[data-slot="${id}"]`, root);
  const wireEl = (id) => $(`.wire[data-slot="${id}"]`, $(".wires", root));
  const spareEl = (id) => $(`.spare[data-part="${id}"]`, root);
  const stateOf = (el) => [...el.classList].find((c) => c.startsWith("k-")) || "k-empty";

  /* ---------- Color mode ---------- */

  function readMode() {
    try {
      return localStorage.getItem(COLOR_KEY) === "status" ? "status" : "type";
    } catch {
      return "type";
    }
  }

  function setMode(mode) {
    try {
      localStorage.setItem(COLOR_KEY, mode);
    } catch {
      // Private windows may refuse storage; the toggle still works for this page.
    }
    root.dataset.color = mode;
    $$("[data-color]", root).forEach((b) => b.setAttribute("aria-checked", String(b.dataset.color === mode)));
  }

  /* ---------- Wires ---------- */

  // pathD turns right-angled points into a path with rounded corners.
  function pathD(raw, radius = 10) {
    const pts = raw.filter((p, i) => i === 0 || Math.hypot(p.x - raw[i - 1].x, p.y - raw[i - 1].y) > 0.5);
    const f = (n) => Math.round(n * 10) / 10;
    let d = `M${f(pts[0].x)} ${f(pts[0].y)}`;
    for (let i = 1; i < pts.length - 1; i++) {
      const [p0, p1, p2] = [pts[i - 1], pts[i], pts[i + 1]];
      const d1 = Math.hypot(p1.x - p0.x, p1.y - p0.y);
      const d2 = Math.hypot(p2.x - p1.x, p2.y - p1.y);
      const r = Math.min(radius, d1 / 2, d2 / 2);
      const a = { x: p1.x - ((p1.x - p0.x) / d1) * r, y: p1.y - ((p1.y - p0.y) / d1) * r };
      const b = { x: p1.x + ((p2.x - p1.x) / d2) * r, y: p1.y + ((p2.y - p1.y) / d2) * r };
      d += ` L${f(a.x)} ${f(a.y)} Q${f(p1.x)} ${f(p1.y)} ${f(b.x)} ${f(b.y)}`;
    }
    const last = pts[pts.length - 1];
    return `${d} L${f(last.x)} ${f(last.y)}`;
  }

  const sizeKey = (r) => `${Math.round(r.width)}x${Math.round(r.height)}`;

  // routes works out each wire's points, card end first so the dots move
  // into the machine.
  function routes(board, cards) {
    const B = board.getBoundingClientRect();
    lastSize = sizeKey(B);
    const box = (el) => {
      const r = el.getBoundingClientRect();
      return { l: r.left - B.left, r: r.right - B.left, b: r.bottom - B.top, cy: (r.top + r.bottom) / 2 - B.top };
    };
    const ch = box($(".chassis", board));
    if (narrowMQ.matches) {
      // One column: each wire drops out of the chassis floor in its own lane,
      // the top card's lane innermost so no two wires cross.
      return cards.map((card, i) => {
        const c = box(card);
        const x = ch.l + 14 + (cards.length - 1 - i) * RAIL_STEP;
        return { card, pts: [{ x: c.l, y: c.cy }, { x, y: c.cy }, { x, y: ch.b }] };
      });
    }
    const out = [];
    for (const [col, dir] of [[".col-left", -1], [".col-right", 1]]) {
      const items = $$(".slot-card", $(col, board)).map((card) => {
        const bay = box(bayEl(card.dataset.slot));
        const c = box(card);
        return {
          card,
          start: { x: dir < 0 ? bay.l : bay.r, y: bay.cy },
          end: { x: dir < 0 ? c.r : c.l, y: c.cy },
        };
      });
      if (!items.length) {
        continue;
      }
      const inner = dir < 0 ? ch.l : ch.r;
      const gutter = Math.abs(inner - items[0].end.x);
      // Wires turning up take lanes from the chassis outward, top wire
      // first; wires turning down do the same from the bottom. That keeps
      // them from crossing.
      const up = items.filter((it) => it.end.y < it.start.y - 1);
      const down = items.filter((it) => it.end.y > it.start.y + 1).reverse();
      const count = Math.max(up.length, down.length, 1);
      const step = Math.min(13, (gutter - 28) / count);
      const offset = (gutter - count * step) / 2;
      const lane = (rank) => inner + dir * (offset + (rank + 0.5) * step);
      up.forEach((it, k) => (it.lane = lane(k)));
      down.forEach((it, k) => (it.lane = lane(k)));
      for (const it of items) {
        const pts = it.lane === undefined
          ? [it.end, it.start]
          : [it.end, { x: it.lane, y: it.end.y }, { x: it.lane, y: it.start.y }, it.start];
        out.push({ card: it.card, pts });
      }
    }
    return out;
  }

  function svg(tag, attrs, parent) {
    const el = document.createElementNS(SVG_NS, tag);
    for (const [k, v] of Object.entries(attrs)) {
      el.setAttribute(k, v);
    }
    parent.append(el);
    return el;
  }

  function drawWires() {
    const board = $(".board", root);
    const cards = $$(".slot-card", board);
    if (narrowMQ.matches) {
      board.style.setProperty("--rail", `${28 + cards.length * RAIL_STEP}px`);
    }
    const layer = $(".wires", board);
    layer.replaceChildren();
    for (const { card, pts } of routes(board, cards)) {
      const state = stateOf(card);
      const d = pathD(pts);
      const g = svg("g", { class: `wire ${state}`, "data-slot": card.dataset.slot, style: card.getAttribute("style") || "" }, layer);
      svg("path", { class: "glow", d }, g);
      svg("path", { class: "base", d }, g);
      if (state === "k-owned" || state === "k-borrowed") {
        svg("path", { class: "flow", d }, g);
      }
      const [a, z] = [pts[0], pts[pts.length - 1]];
      svg("circle", { class: "port-card", cx: a.x, cy: a.y, r: 4 }, g);
      svg("circle", { class: "port-bay", cx: z.x, cy: z.y, r: 2.4 }, g);
    }
    markPlacing();
    if (openId != null) {
      wireEl(openId)?.classList.add("is-open");
    }
    if (focusId != null) {
      setFocus(focusId, true);
    }
  }

  // drawIn draws a wire from its card to its bay, when a part goes in.
  function drawIn(g, delay = 0) {
    if (reduce || !g) {
      return;
    }
    const base = $(".base", g);
    const len = base.getTotalLength();
    const dash = `${len}px ${len}px`;
    base.animate(
      [{ strokeDasharray: dash, strokeDashoffset: `${len}px`, opacity: 1 }, { strokeDasharray: dash, strokeDashoffset: "0px", opacity: 1 }],
      { duration: 600, delay, easing: "cubic-bezier(.3,.7,.2,1)", fill: "backwards" },
    );
    for (const el of $$(".flow, circle", g)) {
      el.animate([{ opacity: 0 }, { opacity: 0, offset: 0.7 }, { opacity: 1 }], { duration: 800, delay, fill: "backwards" });
    }
  }

  /* ---------- Focus: hover or tab to a card or bay to trace its wire ---------- */

  function setFocus(id, quiet) {
    if (!quiet && id === focusId) {
      return;
    }
    focusId = id;
    root.classList.toggle("has-focus", id != null && placing == null);
    $$(".is-hl", root).forEach((el) => el.classList.remove("is-hl"));
    if (id != null) {
      [cardEl(id), bayEl(id), wireEl(id)].forEach((el) => el?.classList.add("is-hl"));
    }
  }

  /* ---------- Panel ---------- */

  function openPanel(id, focusHeading) {
    const panel = $(".slot-panel", root);
    const detail = $(`[data-detail="${id}"]`, panel);
    if (!detail) {
      closePanel(false);
      return;
    }
    openId = id;
    $$("[data-detail]", panel).forEach((d) => (d.hidden = d !== detail));
    panel.hidden = false;
    $$(".is-open", root).forEach((el) => el.classList.remove("is-open"));
    [cardEl(id), bayEl(id), wireEl(id)].forEach((el) => el?.classList.add("is-open"));
    if (focusHeading) {
      $(".pn-name", detail).focus({ preventScroll: true });
    }
  }

  function closePanel(restoreFocus) {
    const id = openId;
    openId = null;
    $(".slot-panel", root).hidden = true;
    $$(".is-open", root).forEach((el) => el.classList.remove("is-open"));
    if (restoreFocus && id != null) {
      cardEl(id)?.focus({ preventScroll: true });
    }
  }

  /* ---------- Placing a spare: drag it, or click it and then a slot ---------- */

  const dragLayer = svg("svg", { class: "drag-wire", "aria-hidden": "true" }, document.createDocumentFragment());
  const dragBase = svg("path", { class: "d-base" }, dragLayer);
  const dragFlow = svg("path", { class: "d-flow" }, dragLayer);
  const dragDot = svg("circle", { r: 3.5 }, dragLayer);
  // An <svg> has no .hidden property, so this toggles the attribute.
  const showDragWire = (on) => dragLayer.toggleAttribute("hidden", !on);
  showDragWire(false);

  const anchorOf = (card) => {
    const r = card.getBoundingClientRect();
    return { x: r.left + r.width / 2, y: r.bottom };
  };

  function drawDragWire(chip, to) {
    if (!chip) {
      return;
    }
    if (!dragLayer.isConnected) {
      document.body.append(dragLayer);
    }
    const r = chip.getBoundingClientRect();
    const from = { x: r.left + Math.min(r.width / 2, 40), y: r.top };
    const midY = to.y < from.y ? from.y - Math.max(18, (from.y - to.y) / 2) : (from.y + to.y) / 2;
    const d = pathD([from, { x: from.x, y: midY }, { x: to.x, y: midY }, to], 14);
    dragLayer.setAttribute("style", chip.getAttribute("style") || "");
    dragLayer.setAttribute("class", `drag-wire ${stateOf(chip)}`);
    dragLayer.dataset.color = root.dataset.color || "type";
    dragBase.setAttribute("d", d);
    dragFlow.setAttribute("d", d);
    dragDot.setAttribute("cx", to.x);
    dragDot.setAttribute("cy", to.y);
    showDragWire(true);
  }

  const clearTargets = () => $$(".is-target", root).forEach((el) => el.classList.remove("is-target"));

  function markPlacing() {
    const chip = placing == null ? null : spareEl(placing);
    root.classList.toggle("is-placing", !!chip);
    $$(".fits", root).forEach((el) => el.classList.remove("fits"));
    if (!chip) {
      return;
    }
    for (const card of $$(".slot-card", root)) {
      if (card.dataset.cat === chip.dataset.cat) {
        card.classList.add("fits");
        bayEl(card.dataset.slot)?.classList.add("fits");
        wireEl(card.dataset.slot)?.classList.add("fits");
      }
    }
  }

  function startPlacing(id) {
    placing = id;
    root.classList.remove("has-focus");
    const chip = spareEl(id);
    $$(".spare", root).forEach((el) => {
      const on = el === chip;
      el.classList.toggle("is-selected", on);
      el.setAttribute("aria-pressed", String(on));
    });
    const hint = $("[data-tray-hint]", root);
    hint.dataset.idle ??= hint.textContent;
    hint.textContent = `Placing ${$(".sp-name", chip).textContent}. Pick a slot, or press Esc.`;
    hint.classList.add("is-placing");
    markPlacing();
  }

  function stopPlacing() {
    placing = null;
    showDragWire(false);
    $$(".spare", root).forEach((el) => {
      el.classList.remove("is-selected", "is-lifted");
      el.setAttribute("aria-pressed", "false");
    });
    const hint = $("[data-tray-hint]", root);
    if (hint?.dataset.idle) {
      hint.textContent = hint.dataset.idle;
      hint.classList.remove("is-placing");
    }
    clearTargets();
    markPlacing();
  }

  const emit = (name, detail) => root.dispatchEvent(new CustomEvent(name, { detail }));

  function place(partId, slotId) {
    stopPlacing();
    if (root.hasAttribute("data-demo")) {
      emit("board:place", { part: partId, slot: slotId });
      return;
    }
    htmx.ajax("POST", `/slots/${slotId}/part/place`, {
      target: "#board-root",
      swap: "outerHTML",
      values: { part_id: String(partId) },
    });
  }

  let pending = null;
  let dragging = false;
  let ghost = null;
  let suppressClick = false;

  function endDrag(e) {
    if (!pending) {
      return;
    }
    if (dragging) {
      const card = e && document.elementFromPoint(e.clientX, e.clientY)?.closest(".slot-card");
      ghost?.remove();
      ghost = null;
      dragging = false;
      suppressClick = true;
      setTimeout(() => (suppressClick = false), 0);
      if (card && root.contains(card)) {
        place(pending.id, card.dataset.slot);
      } else {
        stopPlacing();
      }
    }
    pending = null;
  }

  document.addEventListener("pointerdown", (e) => {
    const chip = e.target.closest(".spare");
    if (chip && root?.contains(chip) && e.button === 0) {
      pending = { id: chip.dataset.part, x: e.clientX, y: e.clientY };
    }
  });

  document.addEventListener("pointermove", (e) => {
    if (pending && !dragging && Math.hypot(e.clientX - pending.x, e.clientY - pending.y) > 5) {
      dragging = true;
      startPlacing(pending.id);
      const chip = spareEl(pending.id);
      const r = chip.getBoundingClientRect();
      pending.dx = e.clientX - r.left;
      pending.dy = e.clientY - r.top;
      ghost = chip.cloneNode(true);
      ghost.classList.add("ghost");
      ghost.classList.remove("is-selected");
      ghost.style.width = `${r.width}px`;
      document.body.append(ghost);
      chip.classList.add("is-lifted");
    }
    if (!dragging) {
      return;
    }
    e.preventDefault();
    ghost.style.transform = `translate(${e.clientX - pending.dx}px, ${e.clientY - pending.dy}px) rotate(-1.5deg)`;
    const card = document.elementFromPoint(e.clientX, e.clientY)?.closest(".slot-card");
    $$(".is-target", root).forEach((el) => el !== card && el.classList.remove("is-target"));
    card?.classList.add("is-target");
    drawDragWire(spareEl(pending.id), card ? anchorOf(card) : { x: e.clientX, y: e.clientY });
  }, { passive: false });

  document.addEventListener("pointerup", endDrag);
  document.addEventListener("pointercancel", () => endDrag(null));

  /* ---------- Events ---------- */

  document.addEventListener("pointerover", (e) => {
    const board = root && $(".board", root);
    if (!board) {
      return;
    }
    const t = board.contains(e.target) ? e.target.closest("[data-slot]") : null;
    setFocus(t ? t.dataset.slot : null);
    if (placing == null || dragging) {
      return;
    }
    clearTargets();
    const card = t ? cardEl(t.dataset.slot) : null;
    if (card) {
      card.classList.add("is-target");
      drawDragWire(spareEl(placing), anchorOf(card));
    } else {
      showDragWire(false);
    }
  });

  document.addEventListener("focusin", (e) => {
    const t = root && $(".board", root).contains(e.target) ? e.target.closest("[data-slot]") : null;
    setFocus(t ? t.dataset.slot : null);
  });

  document.addEventListener("click", (e) => {
    if (!root || !root.contains(e.target)) {
      return;
    }
    const chip = e.target.closest(".spare");
    if (chip) {
      if (!suppressClick) {
        if (placing === chip.dataset.part) {
          stopPlacing();
        } else {
          startPlacing(chip.dataset.part);
        }
      }
      return;
    }
    const slot = e.target.closest(".slot-card, .bay");
    if (slot) {
      if (placing != null) {
        place(placing, slot.dataset.slot);
      } else if (root.hasAttribute("data-demo")) {
        emit("board:slot", { slot: slot.dataset.slot });
      } else {
        openPanel(slot.dataset.slot, true);
      }
      return;
    }
    if (e.target.closest("[data-panel-close]")) {
      closePanel(true);
      return;
    }
    const mode = e.target.closest("[data-color]");
    if (mode) {
      setMode(mode.dataset.color);
    }
  });

  document.addEventListener("keydown", (e) => {
    if (e.key !== "Escape" || !root) {
      return;
    }
    if (dragging) {
      ghost?.remove();
      ghost = null;
      dragging = false;
      pending = null;
      stopPlacing();
    } else if (placing != null) {
      stopPlacing();
    } else if (openId != null) {
      closePanel(true);
    }
  });

  narrowMQ.addEventListener("change", () => root && drawWires());

  /* ---------- Mount, on page load and after each swap ---------- */

  function mount(el) {
    root = el;
    placing = null;
    setMode(readMode());
    const toggle = $("[data-color-toggle]", root);
    if (toggle) {
      toggle.hidden = false;
    }
    observer?.disconnect();
    const board = $(".board", root);
    observer = new ResizeObserver(() => {
      if (sizeKey(board.getBoundingClientRect()) !== lastSize) {
        drawWires();
      }
    });
    observer.observe(board);
    drawWires();

    const lostFocus = !document.activeElement || document.activeElement === document.body;
    if (root.dataset.open) {
      openPanel(root.dataset.open, true);
    } else if (openId != null) {
      openPanel(openId, lostFocus);
    }
    if (root.dataset.seated) {
      drawIn(wireEl(root.dataset.seated));
    } else if (firstMount) {
      $$(".wire", $(".wires", root)).forEach((g, i) => drawIn(g, 120 + i * 60));
    }
    firstMount = false;
  }

  window.RacketBoard = { mount };

  if (window.htmx) {
    htmx.onLoad((el) => {
      const found = el.id === "board-root" ? el : el.querySelector?.("#board-root");
      if (found) {
        mount(found);
      }
    });
  }
})();
