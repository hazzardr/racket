<script setup lang="ts">
import { ref } from "vue";
import { useRouter } from "vue-router";
import { createMachine } from "../api/machines";
import { importFromURL, importCSV } from "../api/parts";

const router = useRouter();
const name = ref("");
const description = ref("");
const location = ref("");
const builtOn = ref("");
const purchasedOn = ref("");
const error = ref<string | null>(null);
const saving = ref(false);
const importURL = ref("");
const importError = ref<string | null>(null);
const importing = ref(false);

async function submit() {
  error.value = null;
  saving.value = true;
  try {
    await createMachine({
      name: name.value,
      description: description.value || undefined,
      location: location.value || undefined,
      built_on: builtOn.value || undefined,
      purchased_on: purchasedOn.value || undefined,
    });
    router.push("/machines");
  } catch (e) {
    error.value = e instanceof Error ? e.message : "failed to create machine";
  } finally {
    saving.value = false;
  }
}

async function createMachineIfNeeded() {
  if (!name.value.trim()) {
    importError.value = "Please fill in the machine name first.";
    return null;
  }
  const machine = await createMachine({
    name: name.value,
    description: description.value || undefined,
    location: location.value || undefined,
    built_on: builtOn.value || undefined,
    purchased_on: purchasedOn.value || undefined,
  });
  return machine;
}

async function handleURLImport() {
  importError.value = null;
  importing.value = true;
  try {
    const machine = await createMachineIfNeeded();
    if (!machine) return;
    await importFromURL(importURL.value, machine.id);
    router.push(`/machines/${machine.id}`);
  } catch (e) {
    importError.value = e instanceof Error ? e.message : "import failed";
  } finally {
    importing.value = false;
  }
}

async function handleCSVImport(e: Event) {
  const target = e.target as HTMLFormElement;
  const fileInput = target.elements.namedItem("csvFile") as HTMLInputElement;
  const file = fileInput?.files?.[0];
  if (!file) return;
  importError.value = null;
  importing.value = true;
  try {
    const machine = await createMachineIfNeeded();
    if (!machine) return;
    await importCSV(file, machine.id);
    router.push(`/machines/${machine.id}`);
  } catch (e) {
    importError.value = e instanceof Error ? e.message : "import failed";
  } finally {
    importing.value = false;
  }
}
</script>

<template>
  <div class="new-machine-layout">
    <section>
      <h2 class="section-title">New machine</h2>
      <form class="form" @submit.prevent="submit">
        <label>
          Name
          <input v-model="name" type="text" required />
        </label>
        <label>
          Description
          <input v-model="description" type="text" />
        </label>
        <label>
          Location
          <input v-model="location" type="text" />
        </label>
        <label>
          Build date
          <input v-model="builtOn" type="date" />
        </label>
        <label>
          Purchase date
          <input v-model="purchasedOn" type="date" />
        </label>
        <div v-if="error" class="status error">{{ error }}</div>
        <div class="form-actions">
          <button type="submit" class="btn" :disabled="saving">
            {{ saving ? "Saving..." : "Save machine" }}
          </button>
          <RouterLink to="/machines" class="btn-secondary">Cancel</RouterLink>
        </div>
      </form>
    </section>

    <section>
      <h2 class="section-title">
        Import from
        <a
          href="https://pcpartpicker.com"
          target="_blank"
          rel="noopener noreferrer"
          class="external-link"
        >
          PCPartPicker
          <span class="external-link-icon" aria-label="(opens in new tab)">↗</span>
        </a>
      </h2>
      <div class="form">
        <label>
          List URL
          <input v-model="importURL" type="url" placeholder="https://pcpartpicker.com/list/XXXXXX" />
        </label>
        <button type="button" class="btn" :disabled="importing || !importURL" @click="handleURLImport">
          {{ importing ? "Importing..." : "Import from URL" }}
        </button>
      </div>

      <div class="form">
        <form @submit.prevent="handleCSVImport">
          <label>
            CSV file
            <input name="csvFile" type="file" accept=".csv,.txt" />
          </label>
          <button type="submit" class="btn" :disabled="importing">
            {{ importing ? "Importing..." : "Import CSV" }}
          </button>
        </form>
      </div>

      <div v-if="importError" class="status error">{{ importError }}</div>
    </section>
  </div>
</template>

<style scoped>
.new-machine-layout {
  display: grid;
  gap: 2rem;
}

@media (min-width: 768px) {
  .new-machine-layout {
    grid-template-columns: 1fr 1fr;
  }
}

.new-machine-layout .form {
  max-width: none;
}

.external-link {
  color: var(--color-accent);
  text-decoration: none;
}

.external-link:hover {
  text-decoration: underline;
}

.external-link-icon {
  font-size: 0.75em;
  margin-left: 0.25rem;
  vertical-align: super;
}
</style>
