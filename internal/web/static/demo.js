// The landing page demo: imports a sample PCPartPicker list into a machine
// board, then lets visitors mark parts bought and borrow a friend's spare.
// Nothing is saved. The board uses the machine page's markup, and board.js
// draws its wires and handles hovering, dragging, and clicking; this keeps
// the build and re-renders the board after each change. The server supplies
// the default slots, the part-type mapping, and the category colors (see
// landingPage in internal/handlers), so parts land where a real import would
// put them and look the way they would on a machine page.
(() => {
  "use strict";

  const root = document.getElementById("demo");
  const configEl = document.getElementById("demo-config");
  const config = configEl ? JSON.parse(configEl.textContent || "null") : null;
  if (!root || !config || !config.Slots || !window.RacketBoard) {
    return;
  }

  // A sample list as PCPartPicker's Markup > Text shows it. The prices are
  // illustrative.
  const SAMPLE_URL = "https://pcpartpicker.com/list/rAcK3t";
  const SAMPLE = [
    { type: "CPU", name: "AMD Ryzen 7 9800X3D", cents: 44900, store: "Amazon" },
    { type: "CPU Cooler", name: "Thermalright Peerless Assassin 120 SE", cents: 3490, store: "Amazon" },
    { type: "Motherboard", name: "MSI MAG B650 Tomahawk WiFi", cents: 17999, store: "Newegg" },
    { type: "Memory", name: "G.Skill Flare X5 32 GB DDR5-6000", cents: 10999, store: "Amazon" },
    { type: "Storage", name: "Samsung 990 Pro 2 TB NVMe", cents: 16999, store: "Best Buy" },
    { type: "Video Card", name: "Sapphire PULSE Radeon RX 9070 XT", cents: 64999, store: "Newegg" },
    { type: "Case", name: "Fractal Design North", cents: 13999, store: "Amazon" },
    { type: "Power Supply", name: "Corsair RM850e 850 W", cents: 9999, store: "Amazon" },
  ];

  // The friend's spare. A borrowed part keeps its owner and has no price
  // for the builder.
  const SPARE = { id: "spare", name: "Crucial P3 Plus 2 TB NVMe", owner: "sam", category: "Storage" };

  const reduceMotion = window.matchMedia("(prefers-reduced-motion: reduce)").matches;

  const $ = (sel) => root.querySelector(sel);
  const form = $(".demo-form");
  const pasteEl = $(".demo-paste");
  const goBtn = $(".demo-go");
  const wantedBox = $(".demo-wanted");
  const rowsEl = $(".demo-rows");
  const statusEl = $(".demo-status");
  const board = $(".demo-board");
  const trayEl = $(".demo-tray");
  const sparesEl = $(".demo-spares");

  let slots = [];
  let nextSlotID = 1;
  let spareLent = false;
  let running = false;
  let played = false;

  const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, reduceMotion ? Math.min(ms, 80) : ms));

  // el builds an element with a class and optional text.
  function el(tag, className, text) {
    const node = document.createElement(tag);
    node.className = className;
    if (text !== undefined) {
      node.textContent = text;
    }
    return node;
  }

  // money formats cents like the server's money template func: "1,234.56".
  function money(cents) {
    const sign = cents < 0 ? "-" : "";
    const abs = Math.abs(Math.round(cents));
    const whole = String(Math.floor(abs / 100)).replace(/\B(?=(\d{3})+(?!\d))/g, ",");
    return `${sign}${whole}.${String(abs % 100).padStart(2, "0")}`;
  }

  // slotCategory mirrors slotCategory in internal/handlers/import.go.
  function slotCategory(partType) {
    if (!partType) {
      return config.OtherSlot;
    }
    return (config.SlotMap || {})[partType.toLowerCase()] || partType;
  }

  // keyOf and styleOf mirror categoryKey and styleOf in
  // internal/handlers/board.go, for the categories the server sent.
  const keyOf = (category) => category.trim().toLowerCase();

  function styleOf(category) {
    const style = (config.Styles || {})[keyOf(category)];
    if (style) {
      return style;
    }
    const code = category.replace(/[^\p{L}\p{N}]/gu, "").toUpperCase().slice(0, 4) || "?";
    return { Code: code, Color: "#94a3b8" };
  }

  function stateOf(part) {
    if (!part) {
      return "empty";
    }
    if (part.from) {
      return "borrowed";
    }
    return part.wanted ? "wanted" : "owned";
  }

  function setStatus(text) {
    statusEl.textContent = text;
  }

  /* ---------- The board, in partials/slot_card.html's markup ---------- */

  function chipFor(part) {
    const state = stateOf(part);
    const label = { owned: "installed", wanted: "not bought yet", empty: "empty" }[state] ?? `from ${part.from}`;
    return el("span", `chip k-${state}`, label);
  }

  function metaFor(part) {
    if (!part) {
      return spareLent ? "Nothing in it yet" : "Drop a spare here";
    }
    if (part.from) {
      return "on loan";
    }
    return part.wanted ? `est. $${money(part.cents)}` : `$${money(part.cents)}`;
  }

  // actionFor says what clicking a slot's card does.
  function actionFor(part) {
    if (!part) {
      return "";
    }
    if (part.from) {
      return `Click to give it back to ${part.from}`;
    }
    return part.wanted ? "Click to mark it bought" : "Click to mark it not bought yet";
  }

  function cardFor(slot) {
    const state = stateOf(slot.part);
    const card = el("button", `slot-card k-${state}`);
    card.type = "button";
    card.dataset.slot = String(slot.id);
    card.dataset.cat = keyOf(slot.category);
    card.style.setProperty("--type", styleOf(slot.category).Color);
    card.title = actionFor(slot.part);
    const top = el("span", "slot-top");
    top.append(el("span", "slot-cat", slot.category), chipFor(slot.part));
    card.append(top, el("span", "slot-name", slot.part ? slot.part.name : "Empty slot"), el("span", "slot-meta", metaFor(slot.part)));
    return card;
  }

  function bayFor(slot) {
    const style = styleOf(slot.category);
    const bay = el("div", `bay k-${stateOf(slot.part)}`);
    bay.dataset.slot = String(slot.id);
    bay.style.setProperty("--type", style.Color);
    bay.title = slot.category;
    bay.append(el("i", "led"), el("span", "", style.Code));
    return bay;
  }

  function spareChip() {
    const chip = el("button", "spare k-owned");
    chip.type = "button";
    chip.dataset.part = SPARE.id;
    chip.dataset.cat = keyOf(SPARE.category);
    chip.style.setProperty("--type", styleOf(SPARE.category).Color);
    chip.setAttribute("aria-pressed", "false");
    const meta = el("span", "sp-meta", `${SPARE.category} · `);
    meta.append(el("b", "", `${SPARE.owner}'s`));
    chip.append(el("i", "sp-port"), el("span", "sp-name", SPARE.name), meta);
    return chip;
  }

  // countTo animates a dollar amount to its new value.
  function countTo(node, cents) {
    const from = Number(node.dataset.cents || 0);
    node.dataset.cents = String(cents);
    if (reduceMotion || from === cents) {
      node.textContent = money(cents);
      return;
    }
    const start = performance.now();
    const step = (now) => {
      if (node.dataset.cents !== String(cents)) {
        return; // A newer count took over.
      }
      const t = Math.min(1, (now - start) / 450);
      node.textContent = money(from + (cents - from) * (1 - (1 - t) ** 3));
      if (t < 1) {
        requestAnimationFrame(step);
      }
    };
    requestAnimationFrame(step);
  }

  // renderTotals sums the build like the machine page does.
  function renderTotals() {
    const counts = { owned: 0, borrowed: 0, wanted: 0, empty: 0 };
    let total = 0;
    let toBuy = 0;
    for (const slot of slots) {
      counts[stateOf(slot.part)]++;
      if (slot.part && slot.part.cents != null) {
        total += slot.part.cents;
        if (slot.part.wanted) {
          toBuy += slot.part.cents;
        }
      }
    }
    const filled = slots.length - counts.empty;
    $(".demo-filled").textContent = `${filled} / ${slots.length}`;
    $(".demo-foot").textContent = `${filled} of ${slots.length} slots filled`;
    for (const [state, n] of Object.entries(counts)) {
      $(`.demo-meter .k-${state}`).style.flexGrow = String(n);
    }
    countTo($(".demo-total"), total);
    countTo($(".demo-tobuy"), toBuy);
  }

  function renderLegend() {
    const seen = new Map();
    for (const slot of slots) {
      seen.set(keyOf(slot.category), slot.category);
    }
    const entries = [...seen.values()].map((category) => {
      const entry = el("span", "lg lg-types");
      const swatch = el("i", "lg-sw");
      swatch.style.setProperty("--type", styleOf(category).Color);
      entry.append(swatch, category);
      return entry;
    });
    $(".demo-types").replaceChildren(...entries);
  }

  // render redraws the board. seated names the slot a part just went into,
  // so board.js draws its wire in.
  function render(seated) {
    const half = Math.ceil(slots.length / 2);
    const [left, right] = [slots.slice(0, half), slots.slice(half)];
    $(".col-left").replaceChildren(...left.map(cardFor));
    $(".col-right").replaceChildren(...right.map(cardFor));
    $(".bay-col.left").replaceChildren(...left.map(bayFor));
    $(".bay-col.right").replaceChildren(...right.map(bayFor));
    if (spareLent) {
      sparesEl.replaceChildren(el("p", "none", `${SPARE.owner}'s SSD is in your build. Click its slot to give it back.`));
    } else {
      sparesEl.replaceChildren(spareChip());
    }
    renderTotals();
    renderLegend();
    if (seated != null) {
      board.dataset.seated = String(seated);
    } else {
      delete board.dataset.seated;
    }
    window.RacketBoard.mount(board);
  }

  function addSlot(category) {
    const slot = { id: nextSlotID++, category, part: null };
    slots.push(slot);
    return slot;
  }

  function resetBuild() {
    slots = [];
    nextSlotID = 1;
    spareLent = false;
    for (const category of config.Slots) {
      addSlot(category);
    }
    trayEl.hidden = true;
  }

  const slotByID = (id) => slots.find((s) => String(s.id) === String(id));

  /* ---------- What visitors can do once the list is in ---------- */

  // Clicking a part marks it bought or not, or gives a borrowed one back.
  board.addEventListener("board:slot", (e) => {
    const slot = slotByID(e.detail.slot);
    if (running || !slot) {
      return;
    }
    const part = slot.part;
    if (!part) {
      setStatus(spareLent ? "That slot is empty." : `That slot is empty. Put ${SPARE.owner}'s spare in it from the tray below.`);
      return;
    }
    if (part.from) {
      slot.part = null;
      spareLent = false;
      setStatus(`Gave the SSD back to ${SPARE.owner}. It's in the tray again.`);
    } else {
      part.wanted = !part.wanted;
      setStatus(part.wanted ? `${part.name} is back to not bought yet.` : `Marked ${part.name} bought.`);
    }
    render();
    board.querySelector(`.slot-card[data-slot="${slot.id}"]`)?.focus({ preventScroll: true });
  });

  // Dropping sam's spare on a slot, or clicking it and then a slot, lends it.
  board.addEventListener("board:place", (e) => {
    const slot = slotByID(e.detail.slot);
    if (running || !slot || spareLent) {
      return;
    }
    if (slot.part) {
      setStatus("That slot is full. Pick an empty one.");
      return;
    }
    slot.part = { name: SPARE.name, cents: null, wanted: false, from: SPARE.owner };
    spareLent = true;
    render(slot.id);
    setStatus(`${SPARE.owner}'s SSD is in your ${slot.category} slot. It stays ${SPARE.owner}'s, and either of you can take it out.`);
  });

  /* ---------- The import ---------- */

  async function play() {
    running = true;
    played = true;
    goBtn.disabled = true;
    goBtn.textContent = "Importing…";
    const wanted = wantedBox.checked;
    resetBuild();
    render();
    rowsEl.replaceChildren();

    setStatus("Reading the pasted list…");
    await sleep(400);
    const rows = [];
    for (const part of SAMPLE) {
      const row = el("li", "demo-row");
      const type = el("span", "demo-row-type", part.type);
      type.style.setProperty("--type", styleOf(slotCategory(part.type)).Color);
      row.append(type, el("span", "demo-row-name", part.name), el("span", "demo-row-price", `$${money(part.cents)}`), el("span", "demo-row-dest"));
      rowsEl.append(row);
      rows.push(row);
      await sleep(70);
    }

    setStatus(`Found ${SAMPLE.length} parts. Sorting them into slots…`);
    await sleep(300);
    const created = [];
    for (const [i, part] of SAMPLE.entries()) {
      const row = rows[i];
      const category = slotCategory(part.type);
      let slot = slots.find((s) => !s.part && keyOf(s.category) === keyOf(category));
      let dest = category;
      if (!slot) {
        slot = addSlot(category);
        created.push(category);
        dest = `new ${category} slot`;
      }
      row.classList.add("is-active");
      row.querySelector(".demo-row-dest").textContent = `→ ${dest}`;
      await sleep(200);
      slot.part = { name: part.name, cents: part.cents, wanted, from: null };
      render(slot.id);
      row.classList.replace("is-active", "is-done");
      await sleep(420);
    }

    const extra = created.length ? `, with a new slot for the ${created.join(", ")}` : "";
    const next = wanted ? "Click a part once you've bought it." : "Click a part to plan it instead.";
    setStatus(`Imported ${SAMPLE.length} parts${extra}. ${next}`);
    trayEl.hidden = false;
    goBtn.disabled = false;
    goBtn.textContent = "Replay";
    running = false;
  }

  form.addEventListener("submit", (e) => {
    e.preventDefault();
    // The demo shows what the feature cards below it say, so they go once a
    // visitor starts it themselves.
    document.querySelector(".feature-grid")?.remove();
    if (!running) {
      play();
    }
  });

  // The pasted text, built from the sample so the two can't disagree.
  // PCPartPicker writes prices without thousands separators.
  const plain = (cents) => (cents / 100).toFixed(2);
  pasteEl.textContent = [
    `PCPartPicker Part List: ${SAMPLE_URL}`,
    "",
    ...SAMPLE.map((p) => `${p.type}: ${p.name}  ($${plain(p.cents)} @ ${p.store})`),
    `Total: $${plain(SAMPLE.reduce((sum, p) => sum + p.cents, 0))}`,
  ].join("\n");

  root.hidden = false;
  resetBuild();
  render();

  // Play once when the board scrolls into view.
  if ("IntersectionObserver" in window) {
    const observer = new IntersectionObserver(
      (entries) => {
        if (entries.some((entry) => entry.isIntersecting)) {
          observer.disconnect();
          if (!played && !running) {
            play();
          }
        }
      },
      { threshold: 0.3 },
    );
    observer.observe(board);
  }
})();
