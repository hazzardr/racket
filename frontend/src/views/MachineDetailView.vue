<script setup lang="ts">
import { computed, onMounted, ref } from "vue";
import { useRoute } from "vue-router";
import { getMachine } from "../api/machines";
import { createPart, deletePart } from "../api/parts";
import { createSlot, deleteSlot } from "../api/slots";
import type { MachineDetail } from "../types";

const route = useRoute();
const detail = ref<MachineDetail | null>(null);
const loading = ref(true);
const error = ref<string | null>(null);
const savingSlot = ref<number | null>(null);
const newSlotCategory = ref("");
const savingNewSlot = ref(false);

// Per-slot form state
const slotNames = ref<Record<number, string>>({});
const slotManufacturers = ref<Record<number, string>>({});
const slotModels = ref<Record<number, string>>({});

const unassignedParts = computed(() => {
  if (!detail.value) return [];
  return detail.value.parts.filter((p) => !detail.value?.slots.some((s) => s.part?.id === p.id));
});

async function fetchMachine() {
  const id = Number(route.params.id);
  if (isNaN(id)) {
    error.value = "invalid machine id";
    loading.value = false;
    return;
  }
  try {
    detail.value = await getMachine(id);
  } catch (e) {
    error.value = e instanceof Error ? e.message : "failed to load machine";
  } finally {
    loading.value = false;
  }
}

async function addPartToSlot(slotId: number) {
  const name = slotNames.value[slotId]?.trim();
  if (!name) return;
  savingSlot.value = slotId;
  try {
    await createPart({
      slot_id: slotId,
      name,
      manufacturer: slotManufacturers.value[slotId] || undefined,
      model: slotModels.value[slotId] || undefined,
    });
    await fetchMachine();
  } catch (e) {
    error.value = e instanceof Error ? e.message : "failed to add part";
  } finally {
    savingSlot.value = null;
  }
}

async function removeSlot(slotId: number) {
  try {
    await deleteSlot(slotId);
    await fetchMachine();
  } catch (e) {
    error.value = e instanceof Error ? e.message : "failed to remove slot";
  }
}

// common presets, mirrors defaultSlots in internal/handlers/machines.go
const commonSlots = ["Motherboard", "CPU", "RAM", "Storage", "GPU", "PSU", "Case"];

async function addSlot(category?: string) {
  const cat = (category ?? newSlotCategory.value).trim();
  if (!cat || !detail.value) return;
  savingNewSlot.value = true;
  try {
    await createSlot(detail.value.machine.id, cat);
    if (!category) newSlotCategory.value = "";
    await fetchMachine();
  } catch (e) {
    error.value = e instanceof Error ? e.message : "failed to add slot";
  } finally {
    savingNewSlot.value = false;
  }
}

async function removePart(partId: number) {
  try {
    await deletePart(partId);
    await fetchMachine();
  } catch (e) {
    error.value = e instanceof Error ? e.message : "failed to remove part";
  }
}

onMounted(fetchMachine);
</script>

<template>
  <section>
    <div class="section-header">
      <div>
        <RouterLink to="/machines" class="back-link">&larr; Machines</RouterLink>
        <h2 class="section-title">{{ detail?.machine.name }}</h2>
        <p v-if="detail?.machine.description" class="description">{{ detail.machine.description }}</p>
        <p v-if="detail?.machine.location" class="location">{{ detail.machine.location }}</p>
      </div>
    </div>

    <div v-if="loading" class="status">Loading...</div>
    <div v-else-if="error" class="status error">{{ error }}</div>
    <template v-else>
      <div class="slots-grid">
        <div v-for="slot in detail?.slots" :key="slot.id" class="card slot-card">
          <div class="slot-header">
            <h3 class="slot-category" :title="slot.part ? 'Remove slot — part will move to Other parts' : ''">{{ slot.category }}</h3>
            <button class="btn-remove-slot" @click="removeSlot(slot.id)" title="Remove slot">&times;</button>
          </div>
          <template v-if="slot.part">
            <div class="slot-part">
              <span class="part-name">{{ slot.part.name }}</span>
              <span v-if="slot.part.model" class="part-model">{{ slot.part.model }}</span>
              <button class="btn btn-small" @click="removePart(slot.part.id)">Remove</button>
            </div>
          </template>
          <form v-else class="slot-form" @submit.prevent="addPartToSlot(slot.id)">
            <input
              v-model="slotNames[slot.id]"
              type="text"
              placeholder="Part name"
              required
            />
            <input
              v-model="slotManufacturers[slot.id]"
              type="text"
              placeholder="Manufacturer"
            />
            <input
              v-model="slotModels[slot.id]"
              type="text"
              placeholder="Model"
            />
            <button type="submit" class="btn btn-small" :disabled="savingSlot === slot.id">
              {{ savingSlot === slot.id ? "Adding..." : "Add" }}
            </button>
          </form>
        </div>
        <div class="card slot-card add-slot-card">
          <div class="slot-chips">
            <button
              v-for="s in commonSlots"
              :key="s"
              type="button"
              class="btn btn-small"
              :disabled="savingNewSlot"
              @click="addSlot(s)"
            >
              + {{ s }}
            </button>
          </div>
          <form class="slot-form" @submit.prevent="addSlot()">
            <input
              v-model="newSlotCategory"
              type="text"
              placeholder="Slot category"
              required
            />
            <button type="submit" class="btn btn-small" :disabled="savingNewSlot || !newSlotCategory.trim()">
              {{ savingNewSlot ? "Adding..." : "Add slot" }}
            </button>
          </form>
        </div>
      </div>

      <div v-if="unassignedParts.length > 0" class="other-parts">
        <h3>Other parts</h3>
        <div class="card">
          <ul>
            <li v-for="part in unassignedParts" :key="part.id">
              {{ part.name }}
              <span v-if="part.category" class="part-category">{{ part.category }}</span>
            </li>
          </ul>
        </div>
      </div>
    </template>
  </section>
</template>

<style scoped>
.section-header {
  margin-bottom: 1.5rem;
}

.back-link {
  color: var(--color-text-muted);
  text-decoration: none;
  font-size: 0.9375rem;
}

.back-link:hover {
  color: var(--color-text);
}

.description,
.location {
  color: var(--color-text-muted);
  font-size: 0.9375rem;
  margin-top: 0.25rem;
}

.slots-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(280px, 1fr));
  gap: 1rem;
  padding-bottom: 2rem;
}

.slot-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 0.75rem;
}

.slot-header h3 {
  font-size: 1rem;
  margin: 0;
}

.btn-remove-slot {
  background: none;
  border: none;
  color: var(--color-text-muted);
  font-size: 1.25rem;
  line-height: 1;
  cursor: pointer;
  padding: 0.125rem 0.375rem;
  border-radius: 0.25rem;
}

.btn-remove-slot:hover {
  color: var(--color-text);
}

.add-slot-card {
  border-style: dashed;
}

.slot-chips {
  display: flex;
  flex-wrap: wrap;
  gap: 0.375rem;
  margin-bottom: 0.75rem;
}

.slot-part {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  flex-wrap: wrap;
}

.part-name {
  font-weight: 500;
}

.part-model {
  color: var(--color-text-muted);
  font-size: 0.875rem;
}

.slot-form {
  display: flex;
  flex-direction: column;
  gap: 0.5rem;
}

.slot-form input {
  padding: 0.5rem;
  border: 1px solid var(--color-border);
  border-radius: 0.25rem;
  background: var(--color-bg);
  color: var(--color-text);
  font-size: 0.875rem;
}

.btn-small {
  padding: 0.375rem 0.75rem;
  font-size: 0.875rem;
  align-self: flex-start;
}

.other-parts {
  margin-top: 1rem;
}

.other-parts h3 {
  font-size: 1.125rem;
  margin-bottom: 0.75rem;
}

.other-parts ul {
  list-style: none;
  padding: 0;
}

.other-parts li {
  padding: 0.5rem 0;
  border-bottom: 1px solid var(--color-border);
}

.part-category {
  color: var(--color-text-muted);
  font-size: 0.875rem;
  margin-left: 0.5rem;
}
</style>
