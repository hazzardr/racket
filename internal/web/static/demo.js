// The landing page demo: imports a sample PCPartPicker list into a build
// sheet, then lets visitors mark parts bought and borrow a friend's spare.
// Nothing is saved. The server supplies the default slots and the part-type
// mapping (see landingPage in internal/handlers), so parts land where a real
// import would put them.
(() => {
  "use strict";

  const root = document.getElementById("demo");
  const configEl = document.getElementById("demo-config");
  const config = configEl ? JSON.parse(configEl.textContent || "null") : null;
  if (!root || !config || !config.Slots) {
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
  const SPARE = { name: "Crucial P3 Plus 2 TB NVMe", owner: "sam", category: "Storage" };

  const reduceMotion = window.matchMedia("(prefers-reduced-motion: reduce)").matches;

  const $ = (sel) => root.querySelector(sel);
  const form = $(".demo-import");
  const pasteEl = $(".demo-paste");
  const goBtn = $(".demo-go");
  const wantedBox = $(".demo-wanted");
  const rowsEl = $(".demo-rows");
  const statusEl = $(".demo-status");
  const slotsEl = $(".demo-slots");
  const meterEl = $(".demo-meter span");
  const filledEl = $(".demo-filled");
  const totalEl = $(".demo-total");
  const toBuyEl = $(".demo-tobuy");
  const friendEl = $(".demo-friend");
  const spareBtn = $(".demo-spare");

  let slots = [];
  let nextSlotID = 1;
  let running = false;
  let played = false;
  let dragging = false;

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

  function setStatus(text) {
    statusEl.textContent = text;
  }

  function pop(node) {
    if (!reduceMotion) {
      node.animate([{ transform: "scale(0.94)" }, { transform: "scale(1)" }], { duration: 240, easing: "ease-out" });
    }
  }

  // fly animates a copy of the part from its list row into its slot.
  function fly(from, to, label) {
    if (reduceMotion) {
      return Promise.resolve();
    }
    const a = from.getBoundingClientRect();
    const b = to.getBoundingClientRect();
    const ghost = el("div", "demo-ghost", label);
    document.body.append(ghost);
    const box = (r) => ({ left: `${r.left}px`, top: `${r.top}px`, width: `${r.width}px`, height: `${r.height}px` });
    const anim = ghost.animate([{ ...box(a), opacity: 1 }, { ...box(b), opacity: 0.7 }], {
      duration: 560,
      easing: "cubic-bezier(0.5, 0, 0.2, 1)",
      fill: "forwards",
    });
    return anim.finished.catch(() => {}).finally(() => ghost.remove());
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

  // updateTotals sums the build like the machine page does.
  function updateTotals() {
    let filled = 0;
    let total = 0;
    let toBuy = 0;
    for (const slot of slots) {
      if (!slot.part) {
        continue;
      }
      filled++;
      if (slot.part.cents != null) {
        total += slot.part.cents;
        if (slot.part.wanted) {
          toBuy += slot.part.cents;
        }
      }
    }
    filledEl.textContent = `${filled} / ${slots.length}`;
    meterEl.style.width = slots.length ? `${(filled * 100) / slots.length}%` : "0";
    countTo(totalEl, total);
    countTo(toBuyEl, toBuy);
  }

  function renderSlot(slot) {
    const node = slot.el;
    const part = slot.part;
    node.replaceChildren(el("span", "demo-slot-cat", slot.category));
    node.classList.toggle("is-empty", !part);
    node.classList.toggle("is-wanted", Boolean(part && part.wanted));
    node.classList.toggle("is-borrowed", Boolean(part && part.from));
    if (!part) {
      node.append(el("span", "demo-slot-empty", "Empty"));
      return;
    }
    node.append(el("span", "demo-slot-part", part.name));
    const foot = el("span", "demo-slot-foot");
    if (part.cents != null) {
      foot.append(el("span", "part-price", `$${money(part.cents)}`));
    }
    if (part.from) {
      const back = el("button", "btn-link demo-return", "Return");
      back.type = "button";
      back.addEventListener("click", () => returnSpare(slot));
      foot.append(el("span", "part-badge badge-borrowed", `from ${part.from}`), back);
    } else {
      const toggle = el("button", "part-badge demo-toggle", part.wanted ? "not bought yet" : "bought");
      toggle.type = "button";
      toggle.title = part.wanted ? "Mark as bought" : "Mark as not bought yet";
      toggle.addEventListener("click", () => {
        part.wanted = !part.wanted;
        renderSlot(slot);
        pop(slot.el);
        updateTotals();
        slot.el.querySelector(".demo-toggle").focus();
      });
      foot.append(toggle);
    }
    node.append(foot);
  }

  function addSlot(category, isNew) {
    const slot = { id: nextSlotID++, category, part: null, el: el("div", "demo-slot") };
    slot.el.classList.toggle("is-new", Boolean(isNew));
    slot.el.addEventListener("dragover", (e) => {
      if (dragging && !slot.part) {
        e.preventDefault();
        slot.el.classList.add("is-over");
      }
    });
    slot.el.addEventListener("dragleave", () => slot.el.classList.remove("is-over"));
    slot.el.addEventListener("drop", (e) => {
      e.preventDefault();
      slot.el.classList.remove("is-over");
      if (!slot.part) {
        lendSpare(slot);
      }
    });
    slots.push(slot);
    renderSlot(slot);
    slotsEl.append(slot.el);
    return slot;
  }

  function resetBuild() {
    slots = [];
    nextSlotID = 1;
    slotsEl.replaceChildren();
    for (const category of config.Slots) {
      addSlot(category, false);
    }
    friendEl.hidden = true;
    spareBtn.hidden = false;
    updateTotals();
  }

  function lendSpare(slot) {
    slot.part = { name: SPARE.name, cents: null, wanted: false, from: SPARE.owner };
    spareBtn.hidden = true;
    renderSlot(slot);
    pop(slot.el);
    updateTotals();
    setStatus(`${SPARE.owner}'s SSD is in your ${slot.category} slot. It stays ${SPARE.owner}'s, and either of you can take it out.`);
  }

  function returnSpare(slot) {
    slot.part = null;
    spareBtn.hidden = false;
    renderSlot(slot);
    updateTotals();
    setStatus(`Returned the SSD to ${SPARE.owner}.`);
    spareBtn.focus();
  }

  // emptySlotFor picks where a clicked spare goes: an empty slot of its
  // category, else any empty slot.
  function emptySlotFor(category) {
    const empty = slots.filter((s) => !s.part);
    return empty.find((s) => s.category.toLowerCase() === category.toLowerCase()) || empty[0];
  }

  spareBtn.addEventListener("click", () => {
    const slot = emptySlotFor(SPARE.category);
    if (slot) {
      lendSpare(slot);
    } else {
      setStatus("Every slot is full. Remove a slot's part first.");
    }
  });
  spareBtn.addEventListener("dragstart", (e) => {
    dragging = true;
    e.dataTransfer.setData("text/plain", SPARE.name);
    e.dataTransfer.effectAllowed = "move";
    root.classList.add("is-dragging");
  });
  spareBtn.addEventListener("dragend", () => {
    dragging = false;
    root.classList.remove("is-dragging");
  });

  async function play() {
    running = true;
    played = true;
    goBtn.disabled = true;
    goBtn.textContent = "Importing…";
    const wanted = wantedBox.checked;
    resetBuild();
    rowsEl.replaceChildren();

    setStatus("Reading the pasted list…");
    await sleep(500);
    const rows = [];
    for (const part of SAMPLE) {
      const row = el("li", "demo-row");
      row.append(
        el("span", "demo-row-type", part.type),
        el("span", "demo-row-name", part.name),
        el("span", "demo-row-price", `$${money(part.cents)}`),
        el("span", "demo-row-dest"),
      );
      rowsEl.append(row);
      rows.push(row);
      await sleep(90);
    }

    setStatus(`Found ${SAMPLE.length} parts. Sorting them into slots…`);
    await sleep(400);
    const created = [];
    for (const [i, part] of SAMPLE.entries()) {
      const row = rows[i];
      const category = slotCategory(part.type);
      let slot = slots.find((s) => !s.part && s.category.toLowerCase() === category.toLowerCase());
      let dest = category;
      if (!slot) {
        slot = addSlot(category, true);
        created.push(category);
        dest = `new ${category} slot`;
      }
      row.classList.add("is-active");
      row.querySelector(".demo-row-dest").textContent = `→ ${dest}`;
      await sleep(180);
      await fly(row, slot.el, part.name);
      slot.part = { name: part.name, cents: part.cents, wanted, from: null };
      renderSlot(slot);
      pop(slot.el);
      updateTotals();
      row.classList.replace("is-active", "is-done");
      await sleep(100);
    }

    const extra = created.length ? `, with a new slot for the ${created.join(", ")}` : "";
    const next = wanted
      ? "Click “not bought yet” on a part once you've bought it."
      : "Click “bought” on a part to plan it instead.";
    setStatus(`Imported ${SAMPLE.length} parts${extra}. ${next}`);
    friendEl.hidden = false;
    pop(friendEl);
    goBtn.disabled = false;
    goBtn.textContent = "Replay";
    running = false;
  }

  // hideFeatures removes the feature cards below the demo once a visitor
  // starts it themselves; the demo shows the same things.
  function hideFeatures() {
    const features = document.querySelector(".feature-grid");
    if (!features) {
      return;
    }
    if (reduceMotion) {
      features.remove();
      return;
    }
    features
      .animate([{ opacity: 1 }, { opacity: 0, transform: "translateY(8px)" }], { duration: 250, easing: "ease-in" })
      .finished.catch(() => {})
      .finally(() => features.remove());
  }

  form.addEventListener("submit", (e) => {
    e.preventDefault();
    hideFeatures();
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

  // Play once when the demo scrolls into view.
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
      { threshold: 0.35 },
    );
    observer.observe(root);
  }
})();
