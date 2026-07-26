<script setup lang="ts">
import { onMounted, ref } from "vue";
import { listMachines } from "../api/machines";
import type { Machine } from "../types";

const machines = ref<Machine[]>([]);
const loading = ref(true);
const error = ref<string | null>(null);

onMounted(async () => {
  try {
    machines.value = await listMachines();
  } catch (e) {
    error.value = e instanceof Error ? e.message : "failed to load machines";
  } finally {
    loading.value = false;
  }
});
</script>

<template>
  <section class="machines">
    <div class="section-header">
      <h2 class="section-title">Your machines</h2>
      <RouterLink to="/machines/new" class="btn">New machine</RouterLink>
    </div>
    <div v-if="loading" class="status">Loading machines...</div>
    <div v-else-if="error" class="status error">{{ error }}</div>
    <div v-else-if="machines.length === 0" class="status">
      No machines yet. Add one to get started.
    </div>
    <div v-else class="machine-grid">
      <article v-for="machine in machines" :key="machine.id" class="card">
        <h2>{{ machine.name }}</h2>
        <p v-if="machine.description">{{ machine.description }}</p>
        <p v-if="machine.location" class="location">{{ machine.location }}</p>
      </article>
    </div>
  </section>
</template>

<style>
.section-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 1.5rem;
}

.section-title {
  font-size: 1.5rem;
  font-weight: 600;
}

.status {
  color: var(--color-text-muted);
  padding: 2rem 0;
}

.status.error {
  color: #f87171;
}

.machine-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(300px, 1fr));
  gap: 1.5rem;
  padding-bottom: 3rem;
}

.card {
  background: var(--color-surface);
  border: 1px solid var(--color-border);
  border-radius: 0.5rem;
  padding: 1.5rem;
}

.card h2 {
  font-size: 1.25rem;
  margin-bottom: 0.5rem;
}

.card p {
  color: var(--color-text-muted);
  font-size: 0.9375rem;
}

.location {
  margin-top: 0.5rem;
}
</style>
