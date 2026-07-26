<script setup lang="ts">
import MachinesSection from "../components/MachinesSection.vue";
</script>

<template>
  <section class="hero">
    <h2 class="hero-title">What is Racket?</h2>
    <p class="lead">
      It's a notebook for your hardware. Make an entry for each machine —
      server, desktop, NAS, whatever's humming in the corner — then log
      the parts inside, so you never have to remember which stick of RAM
      is in which box.
    </p>
    <div class="feature-grid">
      <div class="feature-card">
        <h3>Machines</h3>
        <p>
          One entry per box: build date, purchase date, and where it lives
          (the closet counts).
        </p>
      </div>
      <div class="feature-card">
        <h3>Parts</h3>
        <p>
          Model, serial number, warranty, purchase info, and a guide URL —
          the stuff you always need exactly once, at 11pm.
        </p>
      </div>
      <div class="feature-card">
        <h3>Easily self-hostable</h3>
        <p>
          One Go binary and a SQLite file — download it, run it, done.
          No database server or other moving pieces to set up.
        </p>
      </div>
    </div>
  </section>

  <MachinesSection />
</template>

<style>
.hero {
  margin-bottom: 4rem;
}

.hero-title {
  font-size: 2rem;
  font-weight: 700;
  letter-spacing: -0.025em;
  margin-bottom: 1rem;
}

.lead {
  font-size: 1.125rem;
  color: var(--color-text-muted);
  max-width: 42rem;
  margin-bottom: 2.5rem;
}

.feature-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(280px, 1fr));
  gap: 1.5rem;
}

.feature-card {
  background: var(--color-surface);
  border: 1px solid var(--color-border);
  border-radius: 0.5rem;
  padding: 1.5rem;
}

.feature-card h3 {
  font-size: 1.125rem;
  margin-bottom: 0.5rem;
}

.feature-card p {
  color: var(--color-text-muted);
  font-size: 0.9375rem;
}
</style>
