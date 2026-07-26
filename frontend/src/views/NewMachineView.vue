<script setup lang="ts">
import { ref } from "vue";
import { useRouter } from "vue-router";
import { createMachine } from "../api/machines";

const router = useRouter();
const name = ref("");
const description = ref("");
const location = ref("");
const builtOn = ref("");
const purchasedOn = ref("");
const error = ref<string | null>(null);
const saving = ref(false);

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
</script>

<template>
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
</template>
